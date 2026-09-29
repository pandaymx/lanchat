package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
)

// stubAPI 是可配置的 appapi.API 假实现。
type stubAPI struct {
	state appapi.State

	connectErr    error
	sendTextID    string
	sendTextErr   error
	offerID       string
	offerErr      error
	respondErr    error
	pauseErr      error
	resumeErr     error
	cancelErr     error
	nicknameErr   error
	downloadErr   error
	channelID     string
	channelCErr   error
	channelJErr   error
	servers       []appapi.Server
	channels      []appapi.Channel
	gotConnect    connectArgs
	gotRespond    respondArgs
	gotTransferID string
	gotName       string
	gotPath       string
}

type (
	connectArgs struct{ addr, psk string }
	respondArgs struct {
		id     string
		accept bool
		dest   string
	}
)

func (s *stubAPI) GetState() appapi.State { return s.state }
func (s *stubAPI) Connect(addr, psk string) error {
	s.gotConnect = connectArgs{addr, psk}
	return s.connectErr
}
func (s *stubAPI) BrowseServers() []appapi.Server { return s.servers }
func (s *stubAPI) SendText(to, text, group string) (string, error) {
	return s.sendTextID, s.sendTextErr
}

func (s *stubAPI) SendSticker(to, path string) (string, error) {
	return s.sendTextID, s.sendTextErr
}

func (s *stubAPI) OfferFile(to, path string) (string, error) {
	return s.offerID, s.offerErr
}

func (s *stubAPI) OfferFileToGroup(group, path string) (string, error) {
	return s.offerID, s.offerErr
}

func (s *stubAPI) RespondFile(id string, accept bool, dest string) error {
	s.gotRespond = respondArgs{id, accept, dest}
	return s.respondErr
}
func (s *stubAPI) PauseFile(id string) error  { s.gotTransferID = id; return s.pauseErr }
func (s *stubAPI) ResumeFile(id string) error { s.gotTransferID = id; return s.resumeErr }
func (s *stubAPI) CancelFile(id string) error { s.gotTransferID = id; return s.cancelErr }
func (s *stubAPI) SetNickname(name string) error {
	s.gotName = name
	return s.nicknameErr
}

func (s *stubAPI) PickDownloadDir(path string) error {
	s.gotPath = path
	return s.downloadErr
}

func (s *stubAPI) ChannelCreate(name string) (string, error) {
	s.gotName = name
	return s.channelID, s.channelCErr
}

func (s *stubAPI) ChannelJoin(id string) error {
	s.gotTransferID = id
	return s.channelJErr
}
func (s *stubAPI) ChannelList() []appapi.Channel { return s.channels }

// startServer 在临时本地传输（Unix socket / 命名管道）上启动服务端，
// 返回服务端与客户端连接。
func startServer(t *testing.T, api appapi.API) (*Server, net.Conn) {
	t.Helper()
	srv := NewServer(api)
	addr := testAddr(t)

	ready := make(chan error, 1)
	go func() { ready <- srv.ListenAndServe(addr) }()

	// 等待本地传输就绪后拨号。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := dialTest(addr, 200*time.Millisecond)
		if err == nil {
			return srv, c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("服务端未能在超时内就绪")
	return nil, nil
}

func call(t *testing.T, c net.Conn, method string, params interface{}) Response {
	t.Helper()
	var p json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		p = data
	}
	req := Request{JSONRPC: rpcVersion, ID: json.RawMessage(`1`), Method: method, Params: p}
	data, err := encode(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(data); err != nil {
		t.Fatal(err)
	}
	return readResponse(t, c)
}

func readResponse(t *testing.T, c net.Conn) Response {
	t.Helper()
	reader := bufio.NewReader(c)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal("读取响应失败:", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatal("响应非合法 JSON:", err)
	}
	return resp
}

func TestGetState(t *testing.T) {
	api := &stubAPI{state: appapi.State{
		Conn:     appapi.ConnConnected,
		SelfID:   "self-1",
		Nickname: "alice",
		Peers:    []appapi.Peer{{ID: "p1", Nickname: "bob"}},
	}}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "GetState", nil)
	if resp.Error != nil {
		t.Fatalf("意外错误: %v", resp.Error)
	}
	var st appapi.State
	if err := json.Unmarshal(mustMarshal(resp.Result), &st); err != nil {
		t.Fatal(err)
	}
	if st.SelfID != "self-1" || len(st.Peers) != 1 || st.Peers[0].Nickname != "bob" {
		t.Fatalf("State 不符: %+v", st)
	}
}

func TestConnectOK(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "Connect", map[string]string{"addr": "127.0.0.1:19090", "psk": "secret"})
	if resp.Error != nil {
		t.Fatalf("意外错误: %v", resp.Error)
	}
	if api.gotConnect.addr != "127.0.0.1:19090" || api.gotConnect.psk != "secret" {
		t.Fatalf("参数未透传: %+v", api.gotConnect)
	}
}

func TestConnectError(t *testing.T) {
	api := &stubAPI{connectErr: errors.New("auth failed")}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "Connect", map[string]string{"addr": "x", "psk": "y"})
	if resp.Error == nil || resp.Error.Code != codeInternal || resp.Error.Message != "auth failed" {
		t.Fatalf("期望 internal 错误，得到 %+v", resp.Error)
	}
}

func TestInvalidParams(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "Connect", nil)
	if resp.Error == nil || resp.Error.Code != codeInvalidParam {
		t.Fatalf("期望 invalid params，得到 %+v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "Nope", map[string]string{})
	if resp.Error == nil || resp.Error.Code != codeNoSuchMethod {
		t.Fatalf("期望 no such method，得到 %+v", resp.Error)
	}
}

func TestParseError(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	if _, err := c.Write([]byte("{not json\n")); err != nil {
		t.Fatal(err)
	}
	resp := readResponse(t, c)
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Fatalf("期望 parse error，得到 %+v", resp.Error)
	}
}

func TestInvalidRequest(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	if _, err := c.Write([]byte(`{"jsonrpc":"1.0","id":1,"method":"GetState"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	resp := readResponse(t, c)
	if resp.Error == nil || resp.Error.Code != codeInvalidReq {
		t.Fatalf("期望 invalid request，得到 %+v", resp.Error)
	}
}

func TestNotificationNoResponse(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	if _, err := c.Write([]byte(`{"jsonrpc":"2.0","method":"something"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	reader := bufio.NewReader(c)
	_, err := reader.ReadBytes('\n')
	if err == nil {
		t.Fatal("notification 不应产生回包")
	}
}

func TestSendTextResult(t *testing.T) {
	api := &stubAPI{sendTextID: "m-42"}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "SendText", map[string]string{"to": "p1", "text": "hi", "group": ""})
	if resp.Error != nil {
		t.Fatalf("意外错误: %v", resp.Error)
	}
	var r struct {
		MsgID string `json:"msgID"`
	}
	if err := json.Unmarshal(mustMarshal(resp.Result), &r); err != nil {
		t.Fatal(err)
	}
	if r.MsgID != "m-42" {
		t.Fatalf("msgID = %q", r.MsgID)
	}
}

func TestRespondFilePassthrough(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "RespondFile", map[string]interface{}{
		"transferID": "t1", "accept": true, "dest": "/tmp/a",
	})
	if resp.Error != nil {
		t.Fatalf("意外错误: %v", resp.Error)
	}
	if api.gotRespond.id != "t1" || !api.gotRespond.accept || api.gotRespond.dest != "/tmp/a" {
		t.Fatalf("参数未透传: %+v", api.gotRespond)
	}
}

func TestTransferIDMethods(t *testing.T) {
	cases := []struct {
		method string
		setErr func(*stubAPI, error)
		got    func(*stubAPI) string
	}{
		{"PauseFile", func(a *stubAPI, e error) { a.pauseErr = e }, func(a *stubAPI) string { return a.gotTransferID }},
		{"ResumeFile", func(a *stubAPI, e error) { a.resumeErr = e }, func(a *stubAPI) string { return a.gotTransferID }},
		{"CancelFile", func(a *stubAPI, e error) { a.cancelErr = e }, func(a *stubAPI) string { return a.gotTransferID }},
	}
	for _, tc := range cases {
		api := &stubAPI{}
		srv, c := startServer(t, api)
		resp := call(t, c, tc.method, map[string]string{"transferID": "tt"})
		if resp.Error != nil {
			t.Fatalf("%s 意外错误: %v", tc.method, resp.Error)
		}
		if tc.got(api) != "tt" {
			t.Fatalf("%s 参数未透传", tc.method)
		}
		srv.Close()
		c.Close()
	}
}

func TestOfferMethods(t *testing.T) {
	api := &stubAPI{offerID: "tr-1"}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	for _, m := range []string{"OfferFile", "OfferFileToGroup"} {
		resp := call(t, c, m, map[string]string{"to": "p", "path": "/f", "group": "g"})
		if resp.Error != nil {
			t.Fatalf("%s 意外错误: %v", m, resp.Error)
		}
		var r struct {
			TransferID string `json:"transferID"`
		}
		if err := json.Unmarshal(mustMarshal(resp.Result), &r); err != nil {
			t.Fatal(err)
		}
		if r.TransferID != "tr-1" {
			t.Fatalf("%s transferID = %q", m, r.TransferID)
		}
	}
}

func TestBrowseAndChannelList(t *testing.T) {
	api := &stubAPI{
		servers:  []appapi.Server{{ID: "s1"}},
		channels: []appapi.Channel{{ID: "ch1"}},
	}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	for _, m := range []string{"BrowseServers", "ChannelList"} {
		resp := call(t, c, m, nil)
		if resp.Error != nil {
			t.Fatalf("%s 意外错误: %v", m, resp.Error)
		}
		arr, ok := resp.Result.([]interface{})
		if !ok || len(arr) != 1 {
			t.Fatalf("%s 结果不符: %+v", m, resp.Result)
		}
	}
}

func TestSetNicknameAndDownloadDir(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "SetNickname", map[string]string{"name": "newname"})
	if resp.Error != nil || api.gotName != "newname" {
		t.Fatalf("SetNickname 失败: %+v", resp.Error)
	}
	resp = call(t, c, "PickDownloadDir", map[string]string{"path": "/dl"})
	if resp.Error != nil || api.gotPath != "/dl" {
		t.Fatalf("PickDownloadDir 失败: %+v", resp.Error)
	}
}

func TestChannelCreateAndJoin(t *testing.T) {
	api := &stubAPI{channelID: "cid-9"}
	srv, c := startServer(t, api)
	defer srv.Close()
	defer c.Close()

	resp := call(t, c, "ChannelCreate", map[string]string{"name": "room"})
	if resp.Error != nil {
		t.Fatalf("ChannelCreate 意外错误: %v", resp.Error)
	}
	var r struct {
		ChannelID string `json:"channelID"`
	}
	if err := json.Unmarshal(mustMarshal(resp.Result), &r); err != nil || r.ChannelID != "cid-9" {
		t.Fatalf("channelID 不符: %+v", resp.Result)
	}
	resp = call(t, c, "ChannelJoin", map[string]string{"channelID": "cid-9"})
	if resp.Error != nil || api.gotTransferID != "cid-9" {
		t.Fatalf("ChannelJoin 失败: %+v", resp.Error)
	}
}

// TestEventBroadcast 验证 Listener 事件被推送给已连接 UI。
func TestEventBroadcast(t *testing.T) {
	api := &stubAPI{}
	srv := NewServer(api)
	l := srv.Listener()

	addr := testAddr(t)
	go func() { _ = srv.ListenAndServe(addr) }()

	var conns []net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for len(conns) < 2 && time.Now().Before(deadline) {
		if c, err := dialTest(addr, 200*time.Millisecond); err == nil {
			conns = append(conns, c)
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if len(conns) != 2 {
		t.Fatal("未能建立两条 UI 连接")
	}
	defer srv.Close()
	for _, c := range conns {
		defer c.Close()
	}

	// 每条连接一个持续读取的 goroutine：
	//  - 先完成一次同步 GetState 往返作为就绪屏障（Dial 成功不代表服务端已
	//    Accept 并把连接登记进广播表）；
	//  - 屏障通过后立即持续读取后续 notification，避免广播方因命名管道 /
	//    socket 缓冲写满而阻塞。
	type readResult struct {
		line []byte
		err  error
	}
	results := make([]chan readResult, len(conns))
	ready := make(chan error, len(conns))
	for i, c := range conns {
		results[i] = make(chan readResult, 16)
		go func(i int, c net.Conn) {
			reader := bufio.NewReader(c)

			// 就绪请求。
			req := Request{JSONRPC: rpcVersion, ID: json.RawMessage(`1`), Method: "GetState"}
			data, err := encode(req)
			if err != nil {
				ready <- err
				return
			}
			if _, err := c.Write(data); err != nil {
				ready <- err
				return
			}
			if _, err := reader.ReadBytes('\n'); err != nil {
				ready <- err
				return
			}
			ready <- nil

			// 持续收集后续帧直到读取出错（连接关闭）。
			for {
				line, err := reader.ReadBytes('\n')
				if len(line) > 0 {
					results[i] <- readResult{line: line}
				}
				if err != nil {
					results[i] <- readResult{err: err}
					return
				}
			}
		}(i, c)
	}
	for i := range conns {
		if err := <-ready; err != nil {
			t.Fatalf("连接 %d 就绪失败: %v", i, err)
		}
	}

	l.OnConnChanged(appapi.ConnConnected, "")
	l.OnPeerJoined(appapi.Peer{ID: "p1"})
	l.OnPeerLeft("p1")
	l.OnMessageReceived("from", "", "m1", "text", "hello")
	l.OnTransferProgress(appapi.Transfer{ID: "t1", BytesDone: 10})
	l.OnTransferDone("t1")
	l.OnTransferFailed("t1", "boom")
	l.OnGroupMatrix("g1", "t1", []byte{0x01})
	l.OnChannelUpdated([]appapi.Channel{{ID: "c1"}})

	wantMethods := map[string]bool{
		"conn.changed": false, "peer.joined": false, "peer.left": false,
		"msg.received": false, "transfer.progress": false, "transfer.done": false,
		"transfer.failed": false, "group.matrix": false, "channel.updated": false,
	}
	// 每条连接都应收到全部 9 个 notification。
	for i, ch := range results {
		got := map[string]bool{}
		for n := 0; n < 9; n++ {
			var res readResult
			select {
			case res = <-ch:
			case <-time.After(3 * time.Second):
				t.Fatalf("连接 %d 等待 notification %d 超时", i, n)
			}
			if res.err != nil {
				t.Fatalf("连接 %d 读取 notification %d 失败: %v", i, n, res.err)
			}
			var notif struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(res.line, &notif); err != nil {
				t.Fatal(err)
			}
			got[notif.Method] = true
		}
		for m := range wantMethods {
			if !got[m] {
				t.Fatalf("连接 %d 缺少事件 %s", i, m)
			}
		}
	}
}

func TestServerClose(t *testing.T) {
	api := &stubAPI{}
	srv, c := startServer(t, api)

	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close 卡住")
	}
	// 已连接 UI 应被断开。
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 16)
	if _, err := c.Read(buf); err == nil {
		t.Fatal("期望连接被关闭")
	}
}

func mustMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
