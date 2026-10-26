package core

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/internal/discover"
)

var voicePacketMagic = []byte("LCTPVOICE\x02")

// voiceSession 管理单次 1:1 实时语音呼叫或 G2 频道实时语音房间（阶段四 v2.0）。
type voiceSession struct {
	mu         sync.Mutex
	conn       net.PacketConn
	localAddr  string
	remoteAddr *net.UDPAddr
	targetPeer string
	targetGrp  string
	state      string // "idle" | "calling" | "active"
	seq        atomic.Uint32
	framesIn   atomic.Uint64
	framesOut  atomic.Uint64
	onFrame    func(seq uint32, ts int64, pcm []byte)
}

// StartVoiceCall 发起局域网实时语音呼叫（1:1 或 G2 语音频道），绑定本地 UDP 端口并通过信令下发邀约。
func (c *Client) StartVoiceCall(to, group string) (string, error) {
	if err := c.ensureLocalVoiceUDP(to, group); err != nil {
		return "", err
	}
	localAddr := c.voiceLocalAddr()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:call:offer:"+localAddr)
		}
	}
	return "call:calling:" + localAddr, nil
}

// AcceptVoiceCall 接听对端实时语音呼叫，记录对端 UDP 端点并回传本机 UDP 端点。
func (c *Client) AcceptVoiceCall(to, group, remoteUDP string) (string, error) {
	if err := c.ensureLocalVoiceUDP(to, group); err != nil {
		return "", err
	}
	c.setVoiceRemoteUDP(remoteUDP)
	localAddr := c.voiceLocalAddr()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:call:accept:"+localAddr)
		}
	}
	return "call:active:" + localAddr, nil
}

// StopVoiceCall 挂断当前实时语音通话并释放本地 UDP 套接字。
func (c *Client) StopVoiceCall(to, group string) error {
	c.closeVoiceSession()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:call:hangup")
		}
	}
	return nil
}

// SendVoiceFrame 通过实时语音 UDP 通道发送一帧 PCM/Opus 音频数据。
func (c *Client) SendVoiceFrame(pcm []byte) error {
	if len(pcm) == 0 {
		return errEmptyText
	}
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return errInvalidState
	}
	vs.mu.Lock()
	conn := vs.conn
	raddr := vs.remoteAddr
	vs.mu.Unlock()
	if conn == nil || raddr == nil {
		return errInvalidState
	}

	seq := vs.seq.Add(1)
	pkt := make([]byte, len(voicePacketMagic)+4+8+len(pcm))
	copy(pkt, voicePacketMagic)
	binary.BigEndian.PutUint32(pkt[len(voicePacketMagic):], seq)
	binary.BigEndian.PutUint64(pkt[len(voicePacketMagic)+4:], uint64(time.Now().UnixMilli()))
	copy(pkt[len(voicePacketMagic)+12:], pcm)

	if _, err := conn.WriteTo(pkt, raddr); err != nil {
		return err
	}
	vs.framesOut.Add(1)
	return nil
}

// VoiceCallStatus 返回当前实时语音通话状态摘要。
func (c *Client) VoiceCallStatus() string {
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return "idle"
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	remote := ""
	if vs.remoteAddr != nil {
		remote = vs.remoteAddr.String()
	}
	return fmt.Sprintf("%s local=%s remote=%s in=%d out=%d",
		vs.state, vs.localAddr, remote, vs.framesIn.Load(), vs.framesOut.Load())
}

func (c *Client) ensureLocalVoiceUDP(to, group string) error {
	c.mu.Lock()
	if c.voice != nil && c.voice.conn != nil {
		c.voice.mu.Lock()
		c.voice.targetPeer = to
		c.voice.targetGrp = group
		if c.voice.state == "idle" {
			c.voice.state = "calling"
		}
		c.voice.mu.Unlock()
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		pc, err = net.ListenPacket("udp", ":0")
		if err != nil {
			return err
		}
	}
	localUDP := pc.LocalAddr().String()
	if host, portStr, sErr := net.SplitHostPort(localUDP); sErr == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		if cands := discover.OverlayCandidateAddrs(0); len(cands) > 0 {
			if ch, _, cErr := net.SplitHostPort(cands[0]); cErr == nil && ch != "" {
				localUDP = net.JoinHostPort(ch, portStr)
			}
		} else {
			localUDP = net.JoinHostPort("127.0.0.1", portStr)
		}
	}

	vs := &voiceSession{
		conn:       pc,
		localAddr:  localUDP,
		targetPeer: to,
		targetGrp:  group,
		state:      "calling",
	}
	c.mu.Lock()
	if c.voice != nil && c.voice.conn != nil {
		_ = pc.Close()
		c.mu.Unlock()
		return nil
	}
	c.voice = vs
	c.mu.Unlock()

	go c.readVoiceLoop(vs)
	return nil
}

func (c *Client) readVoiceLoop(vs *voiceSession) {
	buf := make([]byte, 64*1024)
	headerLen := len(voicePacketMagic) + 12
	for {
		n, addr, err := vs.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n <= headerLen || string(buf[:len(voicePacketMagic)]) != string(voicePacketMagic) {
			continue
		}
		seq := binary.BigEndian.Uint32(buf[len(voicePacketMagic):])
		ts := int64(binary.BigEndian.Uint64(buf[len(voicePacketMagic)+4:]))
		payload := append([]byte(nil), buf[headerLen:n]...)
		vs.framesIn.Add(1)

		vs.mu.Lock()
		if vs.remoteAddr == nil {
			if udpAddr, ok := addr.(*net.UDPAddr); ok {
				vs.remoteAddr = udpAddr
				vs.state = "active"
			}
		}
		cb := vs.onFrame
		vs.mu.Unlock()
		if cb != nil {
			cb(seq, ts, payload)
		}
	}
}

func (c *Client) setVoiceRemoteUDP(remoteUDP string) {
	uaddr, err := net.ResolveUDPAddr("udp", strings.TrimSpace(remoteUDP))
	if err != nil {
		return
	}
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return
	}
	vs.mu.Lock()
	vs.remoteAddr = uaddr
	vs.state = "active"
	vs.mu.Unlock()
}

func (c *Client) voiceLocalAddr() string {
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return ""
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.localAddr
}

func (c *Client) closeVoiceSession() {
	c.mu.Lock()
	vs := c.voice
	c.voice = nil
	c.mu.Unlock()
	if vs != nil && vs.conn != nil {
		_ = vs.conn.Close()
	}
}

// StartScreenShare 开启局域网低延迟屏幕/窗口共享会话（阶段四 v2.0）。
func (c *Client) StartScreenShare(to, group string, fps int) (string, error) {
	if fps <= 0 || fps > 60 {
		fps = 15
	}
	c.mu.Lock()
	c.screenSharing = true
	c.screenShareFPS = fps
	c.mu.Unlock()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, fmt.Sprintf("sys:screen:start:%d", fps))
		}
	}
	return fmt.Sprintf("screenshare:active:%dfps", fps), nil
}

// SendScreenFrame 发送一帧屏幕/窗口共享画面（data:image/jpeg;base64,...）。
func (c *Client) SendScreenFrame(to, group, frameData string) error {
	frameData = strings.TrimSpace(frameData)
	if frameData == "" {
		return errEmptyText
	}
	return c.sendControlText(to, group, "sys:screen:frame:"+frameData)
}

// StopScreenShare 结束当前屏幕/窗口共享会话。
func (c *Client) StopScreenShare(to, group string) error {
	c.mu.Lock()
	c.screenSharing = false
	c.screenShareFPS = 0
	c.mu.Unlock()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:screen:stop")
		}
	}
	return nil
}

// AddGossipEndpoints 向本地跨网段 / Tailscale Gossip 地址簿添加候选中心节点。
func (c *Client) AddGossipEndpoints(endpoints ...string) int {
	return c.ensureGossipBook().Add(endpoints...)
}

// GossipEndpoints 返回当前已知的全部跨网段 / Tailscale Gossip 节点列表。
func (c *Client) GossipEndpoints() []string {
	return c.ensureGossipBook().Endpoints()
}

func (c *Client) ensureGossipBook() *discover.GossipPeerBook {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gossipBook == nil {
		path := ""
		if c.downloadDir != "" {
			path = filepath.Join(c.downloadDir, "gossip_peers.json")
		}
		c.gossipBook = discover.NewGossipPeerBook(path)
	}
	return c.gossipBook
}

func (c *Client) handleCollabCommand(trimmed, to, group string) (string, bool, error) {
	switch {
	case trimmed == "/call" || strings.HasPrefix(trimmed, "/call ") || trimmed == "/voiceroom" || strings.HasPrefix(trimmed, "/voiceroom "):
		arg := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "/voiceroom"), "/call"))
		switch {
		case arg == "stop" || arg == "hangup" || arg == "leave":
			_ = c.StopVoiceCall(to, group)
			return "call:stopped", true, nil
		case arg == "status":
			return "call:" + c.VoiceCallStatus(), true, nil
		case strings.HasPrefix(arg, "accept "):
			remote := strings.TrimSpace(strings.TrimPrefix(arg, "accept "))
			res, err := c.AcceptVoiceCall(to, group, remote)
			return res, true, err
		default:
			res, err := c.StartVoiceCall(to, group)
			return res, true, err
		}

	case trimmed == "/screenshare" || strings.HasPrefix(trimmed, "/screenshare "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/screenshare"))
		switch {
		case arg == "stop" || arg == "off":
			_ = c.StopScreenShare(to, group)
			return "screenshare:stopped", true, nil
		case strings.HasPrefix(arg, "frame "):
			frame := strings.TrimSpace(strings.TrimPrefix(arg, "frame "))
			if err := c.SendScreenFrame(to, group, frame); err != nil {
				return "", true, err
			}
			return "screenshare:frame", true, nil
		case strings.HasPrefix(arg, "snap "):
			imgPath := strings.TrimSpace(strings.TrimPrefix(arg, "snap "))
			raw, err := os.ReadFile(imgPath)
			if err != nil {
				return "", true, err
			}
			mime := "image/jpeg"
			if strings.HasSuffix(strings.ToLower(imgPath), ".png") {
				mime = "image/png"
			}
			frame := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(raw))
			if err := c.SendScreenFrame(to, group, frame); err != nil {
				return "", true, err
			}
			return "screenshare:frame", true, nil
		default:
			fps := 15
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "start ")); err == nil && n > 0 {
				fps = n
			}
			res, err := c.StartScreenShare(to, group, fps)
			return res, true, err
		}

	case trimmed == "/gossip" || strings.HasPrefix(trimmed, "/gossip "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/gossip"))
		if strings.HasPrefix(arg, "add ") {
			raw := strings.TrimSpace(strings.TrimPrefix(arg, "add "))
			parts := strings.Split(raw, ",")
			added := c.AddGossipEndpoints(parts...)
			if to != "" || group != "" {
				if _, err := c.connectedConn(); err == nil {
					_ = c.sendControlText(to, group, "sys:gossip:"+strings.Join(c.GossipEndpoints(), ","))
				}
			}
			return fmt.Sprintf("gossip:added:%d total:%d", added, len(c.GossipEndpoints())), true, nil
		}
		eps := c.GossipEndpoints()
		if arg == "sync" && len(eps) > 0 && (to != "" || group != "") {
			if _, err := c.connectedConn(); err == nil {
				_ = c.sendControlText(to, group, "sys:gossip:"+strings.Join(eps, ","))
			}
		}
		return fmt.Sprintf("gossip:%d [%s]", len(eps), strings.Join(eps, ",")), true, nil

	case trimmed == "/roomcode" || strings.HasPrefix(trimmed, "/roomcode "):
		return "roomcode:" + c.ExportRoomInviteURI(), true, nil

	case trimmed == "/failover" || strings.HasPrefix(trimmed, "/failover "):
		res, err := c.TriggerMeshFailover()
		return res, true, err

	case trimmed == "/sync" || strings.HasPrefix(trimmed, "/sync "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/sync"))
		var sinceTs int64
		if arg != "" {
			if n, err := strconv.ParseInt(arg, 10, 64); err == nil && n > 0 {
				sinceTs = n
			}
		}
		pushed, err := c.SyncHistoryWithPeer(to, group, sinceTs)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("sync:pushed:%d", pushed), true, nil

	case strings.HasPrefix(trimmed, "/album "):
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "/album "))
		if raw == "" {
			return "", true, errEmptyPath
		}
		parts := strings.Split(raw, ",")
		var valid []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				valid = append(valid, p)
			}
		}
		if len(valid) == 0 {
			return "", true, errEmptyPath
		}
		for _, p := range valid {
			if group != "" {
				_, _ = c.OfferFileToGroup(group, p)
			} else if to != "" {
				_, _ = c.OfferFile(to, p)
			}
		}
		return fmt.Sprintf("album:sent:%d", len(valid)), true, nil
	}
	return "", false, nil
}

// ExportRoomInviteURI 生成包含当前中心节点地址、跨网段/Tailscale 候选节点与可选 PSK 的二维码快连 URI（v2.4.0）。
func (c *Client) ExportRoomInviteURI() string {
	c.mu.Lock()
	addr := c.addr
	psk := c.psk
	c.mu.Unlock()

	if addr == "" {
		if cands := discover.OverlayCandidateAddrs(7878); len(cands) > 0 {
			addr = cands[0]
		} else {
			addr = "auto"
		}
	}
	q := url.Values{}
	q.Set("addr", addr)
	if psk != "" {
		q.Set("psk", psk)
	}
	gossip := append([]string(nil), c.GossipEndpoints()...)
	for _, cand := range discover.OverlayCandidateAddrs(7878) {
		found := false
		for _, g := range gossip {
			if g == cand {
				found = true
				break
			}
		}
		if !found && len(gossip) < 8 {
			gossip = append(gossip, cand)
		}
	}
	if len(gossip) > 0 {
		q.Set("gossip", strings.Join(gossip, ","))
	}
	return "lanchat://join?" + q.Encode()
}

// ParseRoomInviteURI 解析 lanchat://join?addr=...&psk=...&gossip=... 二维码快连链接（v2.4.0）。
func ParseRoomInviteURI(raw string) (addr, psk string, gossip []string, ok bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(s), "lanchat://") {
		return "", "", nil, false
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", "", nil, false
	}
	q := u.Query()
	addr = strings.TrimSpace(q.Get("addr"))
	if addr == "" && u.Host != "" && !strings.EqualFold(u.Host, "join") {
		addr = u.Host
	}
	if addr == "" {
		addr = "auto"
	}
	psk = q.Get("psk")
	if gRaw := strings.TrimSpace(q.Get("gossip")); gRaw != "" {
		for _, p := range strings.Split(gRaw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				gossip = append(gossip, p)
			}
		}
	}
	return addr, psk, gossip, true
}

// SyncHistoryWithPeer 向目标设备或群组发起个人多端加密历史漫游增量同步（v3.0.0）。
func (c *Client) SyncHistoryWithPeer(to, group string, sinceTs int64) (int, error) {
	msgs := c.history.exportSince(sinceTs)
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, fmt.Sprintf("sys:sync:req:%d", sinceTs))
			if len(msgs) > 0 {
				if raw, mErr := json.Marshal(msgs); mErr == nil {
					b64 := base64.StdEncoding.EncodeToString(raw)
					_ = c.sendControlText(to, group, "sys:sync:push:"+b64)
				}
			}
		}
	}
	return len(msgs), nil
}
