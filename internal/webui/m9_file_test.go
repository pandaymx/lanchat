package webui

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// hubFileServer 模拟 hub 的文件端点：POST 收 multipart 返回 FileRef JSON，
// GET 返回内容。
func hubFileServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/files":
			f, h, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "no file", http.StatusBadRequest)
				return
			}
			defer func() { _ = f.Close() }()
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, content) {
				http.Error(w, "content mismatch", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(protocol.FileRef{
				FileID: strings.Repeat("a", 32),
				Name:   h.Filename,
				Size:   int64(len(content)),
				Mime:   "text/plain",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/files/"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// newFileTestHandler 起一个 Handler（真实 Manager + stub dialer + hub 文件
// 服务端点），HubURL 指向 hub 的 HTTP 地址（HTTPBaseFromWS 直接透传 http://）。
func newFileTestHandler(t *testing.T, hub *httptest.Server) (*Handler, *stubDialer) {
	t.Helper()
	d := &stubDialer{}
	m := NewManager(ManagerConfig{
		User:          "alice",
		ConvID:        "lobby",
		HubURL:        hub.URL,
		DialTimeout:   time.Second,
		SessionTTL:    time.Minute,
		SweepInterval: time.Hour,
		Translator:    testTranslator(),
	}, d.dial)
	h := NewHandler(Config{Version: "test", Translator: testTranslator()}, m)
	return h, d
}

// lastClient 从 stubDialer 拿最近一次拨出的 stubClient（首页渲染可能
// 已拨过号，上传/下载会话用的是同一 cookie 下的最新会话）。
func lastClient(t *testing.T, d *stubDialer) *stubClient {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cs := d.clients; len(cs) > 0 {
			return cs[len(cs)-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no client dialed")
	return nil
}

func TestFileUpload_ProxyAndSendMessage(t *testing.T) {
	content := []byte("file-upload-proxy-test")
	hub := hubFileServer(t, content)
	h, d := newFileTestHandler(t, hub)

	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// 先访问首页触发 cookie session 建立（ensureSession 依赖 cookie）。
	if _, err := srv.Client().Get(srv.URL + "/"); err != nil {
		t.Fatalf("home: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()

	resp, err := srv.Client().Post(srv.URL+"/api/files", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("upload post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	var got protocol.FileRef
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.FileID != strings.Repeat("a", 32) || got.Name != "report.txt" || got.Size != int64(len(content)) {
		t.Errorf("ref = %+v", got)
	}

	// 上传成功后经 SendFileMessage 发了一条附件消息（上传会话是最近拨号）。
	cli := lastClient(t, d)
	cli.mu.Lock()
	refs := append([]protocol.FileRef(nil), cli.fileRefs...)
	cli.mu.Unlock()
	if len(refs) != 1 || refs[0].FileID != strings.Repeat("a", 32) {
		t.Errorf("SendFileMessage refs = %+v (dialCount=%d)", refs, d.dialCount())
	}
}

func TestFileDownload_Proxy(t *testing.T) {
	content := []byte("download-proxy-body")
	hub := hubFileServer(t, content)
	h, _ := newFileTestHandler(t, hub)

	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/files/" + strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("body = %q, want %q", got, content)
	}
	if resp.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
}

var _ = sync.Mutex{}
