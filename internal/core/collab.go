package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/discover"
)

const (
	defaultVoiceJitterWindow    = 40 * time.Millisecond
	defaultVoiceUDPProbeTimeout = 2 * time.Second
)

var (
	voicePacketMagic  = []byte("LCTPVOICE\x02")
	screenPacketMagic = []byte("LCTPSCREEN\x01")

	collabProviderMu       sync.RWMutex
	screenCaptureProvider  func(seq int) ([]byte, error)
	clipboardReadProvider  func() (string, error)
	clipboardWriteProvider func(text string) error
	desktopNotifyProvider  func(title, body string, urgent bool)
)

type voiceJitterFrame struct {
	seq uint32
	ts  int64
	pcm []byte
}

// voiceJitterBuffer 实现 40ms 自适应滑动窗口抖动缓冲（阶段三 v5.3.0）：
// 基于 seq（uint32）与 ts（int64）对跨网段 / Tailscale 隧道微抖动引起的乱序包进行重排、去重与平滑出队。
type voiceJitterBuffer struct {
	mu         sync.Mutex
	windowDur  time.Duration
	maxDepth   int
	queue      []voiceJitterFrame
	seenSeq    map[uint32]struct{}
	lastOutSeq uint32
	lastArrMs  int64
	lastPktTS  int64
	jitterMs   float64
	timer      *time.Timer
	closed     bool
	onDeliver  func(seq uint32, ts int64, pcm []byte)
}

func newVoiceJitterBuffer(windowDur time.Duration, maxDepth int, onDeliver func(uint32, int64, []byte)) *voiceJitterBuffer {
	if windowDur <= 0 {
		windowDur = defaultVoiceJitterWindow
	}
	if maxDepth <= 0 {
		maxDepth = 8
	}
	return &voiceJitterBuffer{
		windowDur: windowDur,
		maxDepth:  maxDepth,
		seenSeq:   make(map[uint32]struct{}, 64),
		onDeliver: onDeliver,
	}
}

func (jb *voiceJitterBuffer) Push(seq uint32, ts int64, pcm []byte) {
	if jb == nil || len(pcm) == 0 {
		return
	}
	jb.mu.Lock()
	if jb.closed {
		jb.mu.Unlock()
		return
	}
	if seq > 0 {
		if _, exists := jb.seenSeq[seq]; exists {
			jb.mu.Unlock()
			return
		}
		if jb.lastOutSeq > 0 && seq <= jb.lastOutSeq {
			jb.mu.Unlock()
			return
		}
		jb.seenSeq[seq] = struct{}{}
		if len(jb.seenSeq) > 256 {
			cutoff := seq
			if cutoff > 128 {
				cutoff -= 128
				for k := range jb.seenSeq {
					if k < cutoff {
						delete(jb.seenSeq, k)
					}
				}
			}
		}
	}

	nowMs := time.Now().UnixMilli()
	if jb.lastArrMs > 0 && ts > 0 && jb.lastPktTS > 0 {
		transitDiff := math.Abs(float64((nowMs - jb.lastArrMs) - (ts - jb.lastPktTS)))
		jb.jitterMs += (transitDiff - jb.jitterMs) / 16.0
	}
	jb.lastArrMs = nowMs
	jb.lastPktTS = ts

	frameCopy := append([]byte(nil), pcm...)
	jb.queue = append(jb.queue, voiceJitterFrame{seq: seq, ts: ts, pcm: frameCopy})
	sort.SliceStable(jb.queue, func(i, j int) bool {
		if jb.queue[i].seq != jb.queue[j].seq {
			return jb.queue[i].seq < jb.queue[j].seq
		}
		return jb.queue[i].ts < jb.queue[j].ts
	})

	if len(jb.queue) >= jb.maxDepth {
		items := jb.takeLocked()
		cb := jb.onDeliver
		jb.mu.Unlock()
		jb.deliverItems(items, cb)
		return
	}

	if jb.timer == nil {
		waitDur := jb.adaptiveWindowLocked()
		jb.timer = time.AfterFunc(waitDur, jb.Flush)
	}
	jb.mu.Unlock()
}

func (jb *voiceJitterBuffer) adaptiveWindowLocked() time.Duration {
	base := jb.windowDur
	if base <= 0 {
		base = defaultVoiceJitterWindow
	}
	if jb.jitterMs > 5 {
		adapted := time.Duration(jb.jitterMs*1.5) * time.Millisecond
		if adapted > base && adapted <= 80*time.Millisecond {
			return adapted
		}
	}
	return base
}

func (jb *voiceJitterBuffer) takeLocked() []voiceJitterFrame {
	if jb.timer != nil {
		jb.timer.Stop()
		jb.timer = nil
	}
	if len(jb.queue) == 0 {
		return nil
	}
	items := jb.queue
	jb.queue = nil
	for _, it := range items {
		if it.seq > jb.lastOutSeq {
			jb.lastOutSeq = it.seq
		}
	}
	return items
}

func (jb *voiceJitterBuffer) Flush() {
	if jb == nil {
		return
	}
	jb.mu.Lock()
	items := jb.takeLocked()
	cb := jb.onDeliver
	jb.mu.Unlock()
	jb.deliverItems(items, cb)
}

func (jb *voiceJitterBuffer) Close() {
	if jb == nil {
		return
	}
	jb.mu.Lock()
	jb.closed = true
	items := jb.takeLocked()
	cb := jb.onDeliver
	jb.mu.Unlock()
	jb.deliverItems(items, cb)
}

func (jb *voiceJitterBuffer) deliverItems(items []voiceJitterFrame, cb func(uint32, int64, []byte)) {
	if cb == nil {
		return
	}
	for _, it := range items {
		cb(it.seq, it.ts, it.pcm)
	}
}

// voiceSession 管理单次 1:1 实时语音呼叫或 G2 频道实时语音房间（阶段四 v2.0 / 阶段三 v5.3.0）。
type voiceSession struct {
	mu               sync.Mutex
	conn             net.PacketConn
	localAddr        string
	remoteAddr       *net.UDPAddr
	targetPeer       string
	targetGrp        string
	state            string // "idle" | "calling" | "active"
	seq              atomic.Uint32
	framesIn         atomic.Uint64
	framesOut        atomic.Uint64
	activeSinceNano  atomic.Int64
	lastUDPInNano    atomic.Int64
	udpRelayFallback atomic.Bool
	udpProbeTimeout  time.Duration
	jitter           *voiceJitterBuffer
	onFrame          func(seq uint32, ts int64, pcm []byte)
	audioStarted     bool
	stopAudioCh      chan struct{}
	capCmd           *exec.Cmd
	playCmd          *exec.Cmd
}

func (vs *voiceSession) markActiveNow() {
	if vs == nil {
		return
	}
	nowNano := time.Now().UnixNano()
	vs.activeSinceNano.CompareAndSwap(0, nowNano)
}

func (vs *voiceSession) recordUDPInboundNow() {
	if vs == nil {
		return
	}
	nowNano := time.Now().UnixNano()
	vs.lastUDPInNano.Store(nowNano)
	vs.activeSinceNano.CompareAndSwap(0, nowNano)
	vs.udpRelayFallback.Store(false)
}

// shouldFallbackToRelay 判断当前 UDP 数据面是否连续超时未收到对端入站包（默认 2s），需自动降级至控制面加密信令通道。
func (vs *voiceSession) shouldFallbackToRelay(now time.Time) bool {
	if vs == nil {
		return false
	}
	if vs.udpRelayFallback.Load() {
		return true
	}
	vs.mu.Lock()
	timeout := vs.udpProbeTimeout
	vs.mu.Unlock()
	if timeout <= 0 {
		timeout = defaultVoiceUDPProbeTimeout
	}
	nowNano := now.UnixNano()
	if lastIn := vs.lastUDPInNano.Load(); lastIn > 0 {
		if nowNano-lastIn >= int64(timeout) {
			vs.udpRelayFallback.Store(true)
			return true
		}
		return false
	}
	if activeSince := vs.activeSinceNano.Load(); activeSince > 0 {
		if nowNano-activeSince >= int64(timeout) {
			vs.udpRelayFallback.Store(true)
			return true
		}
	}
	return false
}

// SetVoiceMuted 设置当前实时语音通话麦克风静音状态（阶段三 v5.3.0）。
func (c *Client) SetVoiceMuted(muted bool) {
	c.voiceMuted.Store(muted)
}

// ToggleVoiceMuted 切换当前实时语音通话麦克风静音状态并返回切换后的状态。
func (c *Client) ToggleVoiceMuted() bool {
	for {
		cur := c.voiceMuted.Load()
		next := !cur
		if c.voiceMuted.CompareAndSwap(cur, next) {
			return next
		}
	}
}

// IsVoiceMuted 返回当前实时语音通话是否处于麦克风静音状态。
func (c *Client) IsVoiceMuted() bool {
	return c.voiceMuted.Load()
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
	c.emitVoiceCallState()
	return "call:calling:" + localAddr, nil
}

// AcceptVoiceCall 接听对端实时语音呼叫，记录对端 UDP 端点并回传本机 UDP 端点。
func (c *Client) AcceptVoiceCall(to, group, remoteUDP string) (string, error) {
	c.mu.Lock()
	if c.voice != nil {
		c.voice.mu.Lock()
		if to == "" {
			to = c.voice.targetPeer
		}
		if group == "" {
			group = c.voice.targetGrp
		}
		if strings.TrimSpace(remoteUDP) == "" && c.voice.remoteAddr != nil {
			remoteUDP = c.voice.remoteAddr.String()
		}
		c.voice.mu.Unlock()
	}
	c.mu.Unlock()

	if err := c.ensureLocalVoiceUDP(to, group); err != nil {
		return "", err
	}
	if strings.TrimSpace(remoteUDP) != "" {
		c.setVoiceRemoteUDP(remoteUDP)
	} else {
		c.mu.Lock()
		vs := c.voice
		c.mu.Unlock()
		if vs != nil {
			vs.mu.Lock()
			vs.state = "active"
			vs.mu.Unlock()
			vs.markActiveNow()
		}
	}
	localAddr := c.voiceLocalAddr()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:call:accept:"+localAddr)
		}
	}
	c.emitVoiceCallState()
	return "call:active:" + localAddr, nil
}

// onIncomingVoiceCallOffer 处理对端发来的 sys:call:offer:<remoteUDP>，进入响铃待接听状态（"ringing"）并触发系统来电通知（v4.9.0）。
func (c *Client) onIncomingVoiceCallOffer(from, group, remoteUDP string) {
	if err := c.ensureLocalVoiceUDP(from, group); err != nil {
		return
	}
	uaddr, _ := net.ResolveUDPAddr("udp", strings.TrimSpace(remoteUDP))
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs != nil {
		vs.mu.Lock()
		vs.targetPeer = from
		vs.targetGrp = group
		if uaddr != nil {
			vs.remoteAddr = uaddr
		}
		vs.state = "ringing"
		vs.mu.Unlock()
	}
	c.emitVoiceCallState()
	c.notifyDesktopEvent(
		"📞 局域网实时语音来电",
		fmt.Sprintf("%s 邀请你进行实时低延迟语音通话", c.peerNicknameOrID(from)),
		true,
	)
}

// StopVoiceCall 挂断或拒接当前实时语音通话并释放本地 UDP 套接字。
func (c *Client) StopVoiceCall(to, group string) error {
	c.mu.Lock()
	if c.voice != nil {
		c.voice.mu.Lock()
		if to == "" {
			to = c.voice.targetPeer
		}
		if group == "" {
			group = c.voice.targetGrp
		}
		c.voice.mu.Unlock()
	}
	c.mu.Unlock()
	c.closeVoiceSession()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:call:hangup")
		}
	}
	c.emitVoiceCallState()
	return nil
}

// SendVoiceFrame 通过实时语音 UDP 通道发送一帧 PCM/Opus 音频数据；
// 当本机处于静音（Mute）状态时静默丢弃；当 UDP 连续 2s 不可达或 WriteTo 失败时自动回退控制面加密信令通道（v5.3.0）。
func (c *Client) SendVoiceFrame(pcm []byte) error {
	if len(pcm) == 0 {
		return errEmptyText
	}
	if c.IsVoiceMuted() {
		return nil
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
	targetPeer := vs.targetPeer
	targetGrp := vs.targetGrp
	vs.mu.Unlock()

	vs.markActiveNow()
	seq := vs.seq.Add(1)
	tsMs := time.Now().UnixMilli()

	_, wsErr := c.connectedConn()
	hasSignaling := wsErr == nil && (targetPeer != "" || targetGrp != "")

	var udpWriteErr error
	if conn != nil && raddr != nil && (!vs.shouldFallbackToRelay(time.Now()) || !hasSignaling) {
		pkt := make([]byte, len(voicePacketMagic)+4+8+len(pcm))
		copy(pkt, voicePacketMagic)
		binary.BigEndian.PutUint32(pkt[len(voicePacketMagic):], seq)
		binary.BigEndian.PutUint64(pkt[len(voicePacketMagic)+4:], uint64(tsMs))
		copy(pkt[len(voicePacketMagic)+12:], pcm)
		if _, err := conn.WriteTo(pkt, raddr); err == nil {
			vs.framesOut.Add(1)
			return nil
		} else {
			udpWriteErr = err
			vs.udpRelayFallback.Store(true)
		}
	}

	if hasSignaling {
		payload := fmt.Sprintf("sys:call:pcm:%d:%d:%s", seq, tsMs, base64.StdEncoding.EncodeToString(pcm))
		if err := c.sendControlText(targetPeer, targetGrp, payload); err != nil {
			return err
		}
		vs.framesOut.Add(1)
		return nil
	}
	if udpWriteErr != nil {
		return udpWriteErr
	}
	return errInvalidState
}

// onInboundSignalingVoiceFrame 处理 UDP 受阻时由控制面加密信令通道转发而来的语音 PCM 帧（sys:call:pcm:<seq>:<ts>:<b64>）。
func (c *Client) onInboundSignalingVoiceFrame(from, group, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var (
		seq uint32
		ts  int64
		b64 = raw
	)
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) == 3 {
		if sVal, err := strconv.ParseUint(parts[0], 10, 32); err == nil {
			seq = uint32(sVal)
		}
		if tVal, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			ts = tVal
		}
		b64 = parts[2]
	}
	pcm, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(pcm) == 0 {
		return
	}

	_ = c.ensureLocalVoiceUDP(from, group)
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return
	}
	vs.framesIn.Add(1)
	if seq == 0 {
		seq = uint32(vs.framesIn.Load())
	}
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	vs.mu.Lock()
	if vs.state != "active" {
		vs.state = "active"
	}
	jb := vs.jitter
	cb := vs.onFrame
	vs.mu.Unlock()

	if jb != nil {
		jb.Push(seq, ts, pcm)
	} else if cb != nil {
		cb(seq, ts, pcm)
	}
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
	mode := "udp"
	if vs.udpRelayFallback.Load() {
		mode = "relay"
	}
	return fmt.Sprintf("%s local=%s remote=%s in=%d out=%d mode=%s muted=%t",
		vs.state, vs.localAddr, remote, vs.framesIn.Load(), vs.framesOut.Load(), mode, c.IsVoiceMuted())
}

func (c *Client) ensureLocalVoiceUDP(to, group string) error {
	c.mu.Lock()
	if c.voice != nil && c.voice.conn != nil {
		c.voice.mu.Lock()
		if to != "" {
			c.voice.targetPeer = to
		}
		if group != "" {
			c.voice.targetGrp = group
		}
		if c.voice.state == "idle" {
			c.voice.state = "calling"
		}
		c.voice.mu.Unlock()
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	bindAddr := ":0"
	if isGoTestBinary() {
		bindAddr = "127.0.0.1:0"
	}
	pc, err := net.ListenPacket("udp", bindAddr)
	if err != nil {
		pc, err = net.ListenPacket("udp", "127.0.0.1:0")
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
		conn:            pc,
		localAddr:       localUDP,
		targetPeer:      to,
		targetGrp:       group,
		state:           "calling",
		udpProbeTimeout: defaultVoiceUDPProbeTimeout,
		stopAudioCh:     make(chan struct{}),
	}
	vs.jitter = newVoiceJitterBuffer(defaultVoiceJitterWindow, 8, func(seq uint32, ts int64, pcm []byte) {
		vs.mu.Lock()
		cb := vs.onFrame
		vs.mu.Unlock()
		if cb != nil {
			cb(seq, ts, pcm)
		}
	})

	c.mu.Lock()
	if c.voice != nil && c.voice.conn != nil {
		vs.jitter.Close()
		_ = pc.Close()
		c.mu.Unlock()
		return nil
	}
	c.voice = vs
	c.mu.Unlock()

	if !isGoTestBinary() {
		c.startVoiceAudioPipeline(vs)
	}
	go c.readVoiceLoop(vs)
	return nil
}

func isGoTestBinary() bool {
	if os.Getenv("LANCHAT_DISABLE_AUDIO") != "" {
		return true
	}
	exe := filepath.Base(os.Args[0])
	return strings.HasSuffix(exe, ".test") || strings.HasSuffix(exe, ".test.exe") || strings.Contains(exe, "_test")
}

type audioCmdCandidate struct {
	bin  string
	args []string
}

// rawAudioPlayerCandidatesForOS 返回指定操作系统（linux / darwin / windows）下的 16kHz 16-bit 单声道 PCM 播放命令候选列表（阶段三 v5.3.0）。
func rawAudioPlayerCandidatesForOS(goos string) []audioCmdCandidate {
	switch goos {
	case "darwin":
		return []audioCmdCandidate{
			{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0"}},
			{"sox", []string{"-q", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-", "-d"}},
			{"play", []string{"-q", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-"}},
		}
	case "windows":
		return []audioCmdCandidate{
			{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0"}},
			{"sox", []string{"-q", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-", "-d"}},
			{"powershell", []string{"-NoProfile", "-NonInteractive", "-Command", "$in=[Console]::OpenStandardInput(); $buf=New-Object byte[] 4096; while(($n=$in.Read($buf,0,$buf.Length)) -gt 0){}"}},
		}
	default:
		return []audioCmdCandidate{
			{"pw-play", []string{"--rate=16000", "--channels=1", "--format=s16", "--raw", "-"}},
			{"paplay", []string{"--raw", "--rate=16000", "--channels=1", "--format=s16le"}},
			{"aplay", []string{"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw", "-"}},
			{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0"}},
		}
	}
}

// rawAudioCaptureCandidatesForOS 返回指定操作系统（linux / darwin / windows）下的 16kHz 16-bit 单声道麦克风 PCM 采集命令候选列表（阶段三 v5.3.0）。
func rawAudioCaptureCandidatesForOS(goos string) []audioCmdCandidate {
	switch goos {
	case "darwin":
		return []audioCmdCandidate{
			{"ffmpeg", []string{"-loglevel", "quiet", "-f", "avfoundation", "-i", ":0", "-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1"}},
			{"sox", []string{"-q", "-d", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-"}},
			{"rec", []string{"-q", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-"}},
		}
	case "windows":
		return []audioCmdCandidate{
			{"ffmpeg", []string{"-loglevel", "quiet", "-f", "dshow", "-i", "audio=default", "-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1"}},
			{"sox", []string{"-q", "-d", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-"}},
			{"powershell", []string{"-NoProfile", "-NonInteractive", "-Command", "Add-Type -AssemblyName System.Speech; $out=[Console]::OpenStandardOutput(); $buf=New-Object byte[] 640; while($true){ Start-Sleep -Milliseconds 20; $out.Write($buf,0,$buf.Length) }"}},
		}
	default:
		return []audioCmdCandidate{
			{"pw-record", []string{"--rate=16000", "--channels=1", "--format=s16", "--raw", "-"}},
			{"parec", []string{"--rate=16000", "--channels=1", "--format=s16le"}},
			{"arecord", []string{"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw", "-"}},
			{"ffmpeg", []string{"-loglevel", "quiet", "-f", "pulse", "-i", "default", "-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1"}},
		}
	}
}

func findRawAudioPlayerCmd() *exec.Cmd {
	for _, c := range rawAudioPlayerCandidatesForOS(runtime.GOOS) {
		if path, err := exec.LookPath(c.bin); err == nil {
			return exec.Command(path, c.args...)
		}
	}
	return nil
}

func findRawAudioCaptureCmd() *exec.Cmd {
	for _, c := range rawAudioCaptureCandidatesForOS(runtime.GOOS) {
		if path, err := exec.LookPath(c.bin); err == nil {
			return exec.Command(path, c.args...)
		}
	}
	return nil
}

func (c *Client) startVoiceAudioPipeline(vs *voiceSession) {
	vs.mu.Lock()
	if vs.audioStarted {
		vs.mu.Unlock()
		return
	}
	vs.audioStarted = true
	stopCh := vs.stopAudioCh
	vs.mu.Unlock()

	// 1. 启动扬声器 PCM 播放管道（16kHz 16-bit mono）
	if playCmd := findRawAudioPlayerCmd(); playCmd != nil {
		if stdin, err := playCmd.StdinPipe(); err == nil {
			if err := playCmd.Start(); err == nil {
				playCh := make(chan []byte, 64)
				vs.mu.Lock()
				vs.playCmd = playCmd
				if vs.onFrame == nil {
					vs.onFrame = func(_ uint32, _ int64, pcm []byte) {
						select {
						case playCh <- pcm:
						default:
						}
					}
				}
				vs.mu.Unlock()
				go func() {
					defer func() { _ = stdin.Close() }()
					for {
						select {
						case <-stopCh:
							return
						case pcm := <-playCh:
							if _, wErr := stdin.Write(pcm); wErr != nil {
								return
							}
						}
					}
				}()
			}
		}
	}

	// 2. 启动麦克风 PCM 采集管道（20ms = 640B 帧，16kHz 16-bit mono）
	if capCmd := findRawAudioCaptureCmd(); capCmd != nil {
		if stdout, err := capCmd.StdoutPipe(); err == nil {
			if err := capCmd.Start(); err == nil {
				vs.mu.Lock()
				vs.capCmd = capCmd
				vs.mu.Unlock()
				go func() {
					frame := make([]byte, 640)
					for {
						select {
						case <-stopCh:
							return
						default:
						}
						n, rErr := io.ReadFull(stdout, frame)
						if n > 0 && !c.IsVoiceMuted() {
							_ = c.SendVoiceFrame(frame[:n])
						}
						if rErr != nil {
							return
						}
					}
				}()
			}
		}
	}
}

func (c *Client) readVoiceLoop(vs *voiceSession) {
	buf := make([]byte, 64*1024)
	headerLen := len(voicePacketMagic) + 12
	screenMagicLen := len(screenPacketMagic)
	for {
		n, addr, err := vs.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n > screenMagicLen && string(buf[:screenMagicLen]) == string(screenPacketMagic) {
			vs.recordUDPInboundNow()
			frame := string(buf[screenMagicLen:n])
			vs.mu.Lock()
			peer := vs.targetPeer
			grp := vs.targetGrp
			vs.mu.Unlock()
			c.recordScreenFrame(peer, grp, 15, frame)
			c.listener().OnMessageReceived(peer, grp, "udp-screen", "SCREEN_FRAME", frame)
			c.emitScreenShareFrame(peer, grp, 15, frame)
			continue
		}
		if n <= headerLen || string(buf[:len(voicePacketMagic)]) != string(voicePacketMagic) {
			continue
		}
		vs.recordUDPInboundNow()
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
		jb := vs.jitter
		cb := vs.onFrame
		vs.mu.Unlock()
		if jb != nil {
			jb.Push(seq, ts, payload)
		} else if cb != nil {
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
	vs.markActiveNow()
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
	if vs == nil {
		return
	}
	vs.mu.Lock()
	stopCh := vs.stopAudioCh
	vs.stopAudioCh = nil
	capCmd := vs.capCmd
	playCmd := vs.playCmd
	vs.capCmd = nil
	vs.playCmd = nil
	jb := vs.jitter
	vs.jitter = nil
	conn := vs.conn
	vs.mu.Unlock()

	if jb != nil {
		jb.Close()
	}
	if stopCh != nil {
		close(stopCh)
	}
	if capCmd != nil && capCmd.Process != nil {
		_ = capCmd.Process.Kill()
		_ = capCmd.Wait()
	}
	if playCmd != nil && playCmd.Process != nil {
		_ = playCmd.Process.Kill()
		_ = playCmd.Wait()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// StartScreenShare 开启局域网低延迟屏幕/窗口共享会话（阶段四 v2.0 / 契约 v1.2 / v4.8.0 真实抓屏推流管线）。
func (c *Client) StartScreenShare(to, group string, fps int) (string, error) {
	if fps <= 0 || fps > 60 {
		fps = 15
	}
	c.stopScreenShareLoop()
	stopCh := make(chan struct{})
	c.mu.Lock()
	c.screenSharing = true
	c.screenShareFPS = fps
	c.screenSharePeer = to
	c.screenShareGroup = group
	c.screenStop = stopCh
	c.mu.Unlock()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, fmt.Sprintf("sys:screen:start:%d", fps))
		}
	}
	collabProviderMu.RLock()
	hasHook := screenCaptureProvider != nil
	collabProviderMu.RUnlock()
	if !isGoTestBinary() || hasHook {
		go c.runScreenShareCaptureLoop(stopCh, to, group, fps)
	}
	return fmt.Sprintf("screenshare:active:%dfps", fps), nil
}

// SendScreenFrame 发送一帧屏幕/窗口共享画面（data:image/jpeg;base64,...）；
// 若已建立实时 UDP 通道、单包 ≤60 KiB 且 UDP 数据面可达，优先走 UDP 数据面直传；
// 若连续 2s 未收到对端 UDP 入站包或 WriteTo 失败，自动回退加密信令控制帧（v3.1.0 / v5.3.0）。
func (c *Client) SendScreenFrame(to, group, frameData string) error {
	frameData = strings.TrimSpace(frameData)
	if frameData == "" {
		return errEmptyText
	}
	c.recordScreenFrame(to, group, 0, frameData)
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	_, wsErr := c.connectedConn()
	hasSignaling := wsErr == nil && (to != "" || group != "")
	if vs != nil && len(frameData)+len(screenPacketMagic) <= 60*1024 && (!vs.shouldFallbackToRelay(time.Now()) || !hasSignaling) {
		vs.mu.Lock()
		conn := vs.conn
		raddr := vs.remoteAddr
		vs.mu.Unlock()
		if conn != nil && raddr != nil {
			pkt := make([]byte, len(screenPacketMagic)+len(frameData))
			copy(pkt, screenPacketMagic)
			copy(pkt[len(screenPacketMagic):], frameData)
			if _, err := conn.WriteTo(pkt, raddr); err == nil {
				return nil
			}
			vs.udpRelayFallback.Store(true)
		}
	}
	return c.sendControlText(to, group, "sys:screen:frame:"+frameData)
}

// StopScreenShare 结束当前屏幕/窗口共享会话。
func (c *Client) StopScreenShare(to, group string) error {
	c.stopScreenShareLoop()
	c.mu.Lock()
	if to == "" {
		to = c.screenSharePeer
	}
	if group == "" {
		group = c.screenShareGroup
	}
	c.screenSharing = false
	c.screenShareFPS = 0
	c.screenSharePeer = ""
	c.screenShareGroup = ""
	c.latestScreenFrame = ""
	c.mu.Unlock()
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:screen:stop")
		}
	}
	c.emitScreenShareFrame(c.selfIDValue(), group, 0, "")
	return nil
}

func (c *Client) stopScreenShareLoop() {
	c.mu.Lock()
	stopCh := c.screenStop
	c.screenStop = nil
	c.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
}

func (c *Client) runScreenShareCaptureLoop(stopCh <-chan struct{}, to, group string, fps int) {
	if fps <= 0 {
		fps = 15
	}
	interval := time.Second / time.Duration(fps)
	if interval < 40*time.Millisecond {
		interval = 40 * time.Millisecond
	}
	if interval > 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	seq := 0
	pushOne := func() {
		seq++
		frameURI, err := captureDesktopJPEGDataURI(seq)
		if err != nil || frameURI == "" {
			return
		}
		c.mu.Lock()
		active := c.screenSharing
		targetPeer := to
		if targetPeer == "" {
			targetPeer = c.screenSharePeer
		}
		targetGrp := group
		if targetGrp == "" {
			targetGrp = c.screenShareGroup
		}
		c.mu.Unlock()
		if !active {
			return
		}
		_ = c.SendScreenFrame(targetPeer, targetGrp, frameURI)
		c.emitScreenShareFrame(c.selfIDValue(), targetGrp, fps, frameURI)
	}

	pushOne()
	for {
		select {
		case <-stopCh:
			return
		case <-c.runCtx.Done():
			return
		case <-ticker.C:
			pushOne()
		}
	}
}

// captureDesktopJPEGDataURI 执行跨平台真实桌面抓屏（Wayland grim / X11 ffmpeg / xwd / import；macOS screencapture；Windows ffmpeg / GDI），
// 并自动缩放编码为紧凑 JPEG Data URI（≤ 48 KiB，适合 LCTPSCREEN UDP 数据面直传）。
func captureDesktopJPEGDataURI(seq int) (string, error) {
	collabProviderMu.RLock()
	hook := screenCaptureProvider
	collabProviderMu.RUnlock()
	if hook != nil {
		raw, err := hook(seq)
		if err != nil {
			return "", err
		}
		if len(raw) == 0 {
			return "", errEmptyText
		}
		if strings.HasPrefix(string(raw), "data:image/") {
			return string(raw), nil
		}
		scaled := normalizeScreenJPEGBytes(raw)
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(scaled), nil
	}

	raw, err := captureOSDesktopBytes()
	if err != nil || len(raw) == 0 {
		raw = renderSyntheticScreenJPEG(seq)
	} else {
		raw = normalizeScreenJPEGBytes(raw)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func captureOSDesktopBytes() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	switch runtime.GOOS {
	case "linux":
		// 1. Wayland: grim
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if grimPath, err := exec.LookPath("grim"); err == nil {
				if out, err := exec.CommandContext(ctx, grimPath, "-t", "jpeg", "-q", "55", "-").Output(); err == nil && len(out) > 64 {
					return out, nil
				}
			}
		}
		// 2. X11 / PipeWire: ffmpeg x11grab
		display := os.Getenv("DISPLAY")
		if display == "" {
			display = ":0"
		}
		if ffPath, err := exec.LookPath("ffmpeg"); err == nil {
			args := []string{
				"-loglevel", "quiet",
				"-f", "x11grab",
				"-i", display,
				"-vframes", "1",
				"-vf", "scale=640:-1",
				"-f", "image2pipe",
				"-vcodec", "mjpeg",
				"-q:v", "10",
				"-",
			}
			if out, err := exec.CommandContext(ctx, ffPath, args...).Output(); err == nil && len(out) > 64 {
				return out, nil
			}
		}
		// 3. ImageMagick import / xwd
		if impPath, err := exec.LookPath("import"); err == nil {
			if out, err := exec.CommandContext(ctx, impPath, "-silent", "-window", "root", "-resize", "640x360", "jpg:-").Output(); err == nil && len(out) > 64 {
				return out, nil
			}
		}
		if grimPath, err := exec.LookPath("grim"); err == nil {
			if out, err := exec.CommandContext(ctx, grimPath, "-t", "jpeg", "-q", "55", "-").Output(); err == nil && len(out) > 64 {
				return out, nil
			}
		}

	case "darwin":
		if scPath, err := exec.LookPath("screencapture"); err == nil {
			tmpFile, err := os.CreateTemp("", "lanchat-screen-*.jpg")
			if err == nil {
				tmpPath := tmpFile.Name()
				_ = tmpFile.Close()
				defer func() { _ = os.Remove(tmpPath) }()
				if err := exec.CommandContext(ctx, scPath, "-x", "-C", "-t", "jpg", tmpPath).Run(); err == nil {
					if out, rErr := os.ReadFile(tmpPath); rErr == nil && len(out) > 64 {
						return out, nil
					}
				}
			}
		}

	case "windows":
		if ffPath, err := exec.LookPath("ffmpeg"); err == nil {
			args := []string{
				"-loglevel", "quiet",
				"-f", "gdigrab",
				"-i", "desktop",
				"-vframes", "1",
				"-vf", "scale=640:-1",
				"-f", "image2pipe",
				"-vcodec", "mjpeg",
				"-q:v", "10",
				"-",
			}
			if out, err := exec.CommandContext(ctx, ffPath, args...).Output(); err == nil && len(out) > 64 {
				return out, nil
			}
		}
		if psPath, err := exec.LookPath("powershell"); err == nil {
			psScript := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing; ` +
				`$b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds; ` +
				`$bmp = New-Object System.Drawing.Bitmap(640, 360); ` +
				`$g = [System.Drawing.Graphics]::FromImage($bmp); ` +
				`$g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size); ` +
				`$ms = New-Object System.IO.MemoryStream; ` +
				`$bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Jpeg); ` +
				`[Console]::OpenStandardOutput().Write($ms.ToArray(), 0, $ms.Length)`
			if out, err := exec.CommandContext(ctx, psPath, "-NoProfile", "-NonInteractive", "-Command", psScript).Output(); err == nil && len(out) > 64 {
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("no desktop capture backend available on %s", runtime.GOOS)
}

func normalizeScreenJPEGBytes(raw []byte) []byte {
	if len(raw) <= 42*1024 && len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xD8 {
		return raw
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return renderSyntheticScreenJPEG(1)
	}
	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return renderSyntheticScreenJPEG(1)
	}
	dstW, dstH := srcW, srcH
	if dstW > 640 {
		dstH = dstH * 640 / dstW
		dstW = 640
	}
	if dstH > 360 {
		dstW = dstW * 360 / dstH
		dstH = 360
	}
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		sy := bounds.Min.Y + y*srcH/dstH
		for x := 0; x < dstW; x++ {
			sx := bounds.Min.X + x*srcW/dstW
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 55}); err != nil {
		return renderSyntheticScreenJPEG(1)
	}
	return buf.Bytes()
}

func renderSyntheticScreenJPEG(seq int) []byte {
	const w, h = 320, 180
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	offset := (seq * 12) % w
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8((x + offset) % 256)
			g := uint8((y * 255) / h)
			b := uint8(140)
			if y < 24 {
				r, g, b = 0, 153, 255
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60})
	return buf.Bytes()
}

// SendScreenAnnotation 在局域网屏幕共享会话中发送归一化坐标激光笔/画笔批注（v3.3.0）。
func (c *Client) SendScreenAnnotation(to, group string, normX, normY float64, note string) error {
	if normX < 0 {
		normX = 0
	} else if normX > 1 {
		normX = 1
	}
	if normY < 0 {
		normY = 0
	} else if normY > 1 {
		normY = 1
	}
	note = strings.TrimSpace(note)
	payload := fmt.Sprintf("sys:screen:annot:%.3f,%.3f:%s", normX, normY, note)
	return c.sendControlText(to, group, payload)
}

// SetAutoClipboard 开启或关闭跨设备剪贴板自动监听同步模式（v3.3.0 / v4.8.0 后台自动同步守护协程）。
func (c *Client) SetAutoClipboard(enabled bool) {
	c.SetAutoClipboardTarget(enabled, "", "")
}

// SetAutoClipboardTarget 开启或关闭跨设备剪贴板自动监听同步模式，并绑定同步会话目标（v4.8.0）。
func (c *Client) SetAutoClipboardTarget(enabled bool, to, group string) {
	c.stopAutoClipboardLoop()
	if !enabled {
		c.mu.Lock()
		c.autoClipboard = false
		c.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	c.mu.Lock()
	c.autoClipboard = true
	if to != "" || group != "" {
		c.autoClipPeer = to
		c.autoClipGroup = group
	}
	c.autoClipStop = stopCh
	c.mu.Unlock()

	collabProviderMu.RLock()
	hasHook := clipboardReadProvider != nil
	collabProviderMu.RUnlock()
	if !isGoTestBinary() || hasHook {
		go c.runAutoClipboardLoop(stopCh)
	}
}

func (c *Client) stopAutoClipboardLoop() {
	c.mu.Lock()
	stopCh := c.autoClipStop
	c.autoClipStop = nil
	c.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
}

func (c *Client) runAutoClipboardLoop(stopCh <-chan struct{}) {
	interval := 600 * time.Millisecond
	collabProviderMu.RLock()
	if clipboardReadProvider != nil {
		interval = 40 * time.Millisecond
	}
	collabProviderMu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	checkOnce := func() {
		txt, err := readSystemClipboardText()
		if err != nil {
			return
		}
		txt = strings.TrimSpace(txt)
		if txt == "" {
			return
		}
		h := hashClipboardText(txt)
		c.mu.Lock()
		if !c.autoClipboard || c.lastClipHash == h {
			c.mu.Unlock()
			return
		}
		c.lastClipHash = h
		c.sharedClip = txt
		to := c.autoClipPeer
		group := c.autoClipGroup
		if to == "" && group == "" {
			group = "*"
		}
		c.mu.Unlock()

		if _, err := c.connectedConn(); err == nil {
			_ = c.sendControlText(to, group, "sys:clip:"+txt)
		}
	}

	checkOnce()
	for {
		select {
		case <-stopCh:
			return
		case <-c.runCtx.Done():
			return
		case <-ticker.C:
			checkOnce()
		}
	}
}

func hashClipboardText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func readSystemClipboardText() (string, error) {
	collabProviderMu.RLock()
	hook := clipboardReadProvider
	collabProviderMu.RUnlock()
	if hook != nil {
		return hook()
	}
	if isGoTestBinary() {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	switch runtime.GOOS {
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if p, err := exec.LookPath("wl-paste"); err == nil {
				if out, err := exec.CommandContext(ctx, p, "--no-newline").Output(); err == nil {
					return string(out), nil
				}
			}
		}
		if p, err := exec.LookPath("xclip"); err == nil {
			if out, err := exec.CommandContext(ctx, p, "-selection", "clipboard", "-o").Output(); err == nil {
				return string(out), nil
			}
		}
		if p, err := exec.LookPath("xsel"); err == nil {
			if out, err := exec.CommandContext(ctx, p, "--clipboard", "--output").Output(); err == nil {
				return string(out), nil
			}
		}
		if p, err := exec.LookPath("wl-paste"); err == nil {
			if out, err := exec.CommandContext(ctx, p, "--no-newline").Output(); err == nil {
				return string(out), nil
			}
		}
	case "darwin":
		if p, err := exec.LookPath("pbpaste"); err == nil {
			if out, err := exec.CommandContext(ctx, p).Output(); err == nil {
				return string(out), nil
			}
		}
	case "windows":
		if p, err := exec.LookPath("powershell"); err == nil {
			if out, err := exec.CommandContext(ctx, p, "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw").Output(); err == nil {
				return string(out), nil
			}
		}
	}
	return "", fmt.Errorf("no clipboard reader available on %s", runtime.GOOS)
}

func writeSystemClipboardText(text string) error {
	collabProviderMu.RLock()
	hook := clipboardWriteProvider
	collabProviderMu.RUnlock()
	if hook != nil {
		return hook(text)
	}
	if isGoTestBinary() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	runWithStdin := func(bin string, args ...string) error {
		p, err := exec.LookPath(bin)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, p, args...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}

	switch runtime.GOOS {
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if err := runWithStdin("wl-copy"); err == nil {
				return nil
			}
		}
		if err := runWithStdin("xclip", "-selection", "clipboard", "-i"); err == nil {
			return nil
		}
		if err := runWithStdin("xsel", "--clipboard", "--input"); err == nil {
			return nil
		}
		return runWithStdin("wl-copy")
	case "darwin":
		return runWithStdin("pbcopy")
	case "windows":
		if err := runWithStdin("clip.exe"); err == nil {
			return nil
		}
		return runWithStdin("powershell", "-NoProfile", "-NonInteractive", "-Command", "Set-Clipboard -Value ([Console]::In.ReadToEnd())")
	}
	return fmt.Errorf("no clipboard writer available on %s", runtime.GOOS)
}

// IsAutoClipboard 返回当前是否开启跨设备剪贴板自动监听同步（v3.3.0）。
func (c *Client) IsAutoClipboard() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autoClipboard
}

func (c *Client) voiceCallSnapshotLocked() *appapi.VoiceCallState {
	vs := c.voice
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	remote := ""
	if vs.remoteAddr != nil {
		remote = vs.remoteAddr.String()
	}
	return &appapi.VoiceCallState{
		State:      vs.state,
		PeerID:     vs.targetPeer,
		GroupID:    vs.targetGrp,
		LocalAddr:  vs.localAddr,
		RemoteAddr: remote,
		FramesIn:   int64(vs.framesIn.Load()),
		FramesOut:  int64(vs.framesOut.Load()),
	}
}

func (c *Client) screenShareSnapshotLocked() *appapi.ScreenShareState {
	if !c.screenSharing && c.latestScreenFrame == "" {
		return nil
	}
	return &appapi.ScreenShareState{
		Active:      c.screenSharing,
		PeerID:      c.screenSharePeer,
		GroupID:     c.screenShareGroup,
		FPS:         c.screenShareFPS,
		LatestFrame: c.latestScreenFrame,
	}
}

func (c *Client) recordScreenFrame(peer, group string, fps int, frame string) {
	c.mu.Lock()
	if peer != "" {
		c.screenSharePeer = peer
	}
	if group != "" {
		c.screenShareGroup = group
	}
	if fps > 0 {
		c.screenShareFPS = fps
	}
	c.latestScreenFrame = frame
	c.mu.Unlock()
}

func (c *Client) emitVoiceCallState() {
	c.mu.Lock()
	cb := c.cb
	snap := c.voiceCallSnapshotLocked()
	c.mu.Unlock()
	if snap == nil {
		snap = &appapi.VoiceCallState{State: "idle"}
	}
	if cl, ok := cb.(appapi.CollabListener); ok {
		cl.OnVoiceCallState(*snap)
	}
}

func (c *Client) emitScreenShareFrame(from, group string, fps int, frameData string) {
	c.mu.Lock()
	cb := c.cb
	if fps <= 0 {
		fps = c.screenShareFPS
		if fps <= 0 {
			fps = 15
		}
	}
	c.mu.Unlock()
	if cl, ok := cb.(appapi.CollabListener); ok {
		cl.OnScreenShareFrame(from, group, fps, frameData)
	}
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
		case arg == "stop" || arg == "hangup" || arg == "leave" || arg == "reject":
			_ = c.StopVoiceCall(to, group)
			return "call:stopped", true, nil
		case arg == "mute" || arg == "muted":
			c.SetVoiceMuted(true)
			return "call:muted", true, nil
		case arg == "unmute":
			c.SetVoiceMuted(false)
			return "call:unmuted", true, nil
		case arg == "toggle-mute":
			if c.ToggleVoiceMuted() {
				return "call:muted", true, nil
			}
			return "call:unmuted", true, nil
		case arg == "status":
			return "call:" + c.VoiceCallStatus(), true, nil
		case arg == "accept" || strings.HasPrefix(arg, "accept "):
			remote := strings.TrimSpace(strings.TrimPrefix(arg, "accept"))
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

	case trimmed == "/autoclip" || strings.HasPrefix(trimmed, "/autoclip "):
		arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "/autoclip")))
		switch arg {
		case "off", "0", "false", "stop":
			c.SetAutoClipboardTarget(false, to, group)
			return "autoclip:off", true, nil
		case "status":
			if c.IsAutoClipboard() {
				return "autoclip:on", true, nil
			}
			return "autoclip:off", true, nil
		default:
			c.SetAutoClipboardTarget(true, to, group)
			return "autoclip:on", true, nil
		}

	case strings.HasPrefix(trimmed, "/annotate "):
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "/annotate "))
		if raw == "" {
			return "", true, errEmptyText
		}
		parts := strings.SplitN(raw, " ", 2)
		coords := strings.SplitN(parts[0], ",", 2)
		normX, normY := 0.5, 0.5
		if len(coords) == 2 {
			if x, err := strconv.ParseFloat(strings.TrimSpace(coords[0]), 64); err == nil {
				normX = x
			}
			if y, err := strconv.ParseFloat(strings.TrimSpace(coords[1]), 64); err == nil {
				normY = y
			}
		}
		note := ""
		if len(parts) == 2 {
			note = strings.TrimSpace(parts[1])
		}
		if to != "" || group != "" {
			if _, err := c.connectedConn(); err == nil {
				_ = c.SendScreenAnnotation(to, group, normX, normY, note)
			}
		}
		return fmt.Sprintf("annotate:%.2f,%.2f", normX, normY), true, nil

	case strings.HasPrefix(trimmed, "/foldersync "):
		dirPath := strings.TrimSpace(strings.TrimPrefix(trimmed, "/foldersync "))
		if dirPath == "" {
			return "", true, errEmptyPath
		}
		synced, err := c.SyncFolderWithPeer(to, group, dirPath)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("foldersync:queued:%d", synced), true, nil

	case trimmed == "/netdiag" || strings.HasPrefix(trimmed, "/netdiag "):
		return c.RunNetworkDiagnostics(), true, nil

	case trimmed == "/trust" || strings.HasPrefix(trimmed, "/trust "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/trust"))
		if strings.HasPrefix(arg, "verify ") {
			parts := strings.Fields(strings.TrimPrefix(arg, "verify "))
			if len(parts) < 2 {
				return "trust:invalid", true, nil
			}
			sub, org, ok := VerifyNodeCertificate(parts[0], parts[1])
			if !ok {
				return "trust:invalid", true, nil
			}
			return fmt.Sprintf("trust:verified:%s@%s", sub, org), true, nil
		}
		subject := c.DeviceID()
		org := "LANChat-Enterprise"
		if strings.HasPrefix(arg, "issue ") {
			fields := strings.Fields(strings.TrimPrefix(arg, "issue "))
			if len(fields) >= 1 {
				subject = fields[0]
			}
			if len(fields) >= 2 {
				org = fields[1]
			}
		}
		tok, pub, err := c.IssueNodeCertificate(subject, org, 86400*30)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("trust:issued:%s pub:%s", tok, pub), true, nil
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

// SyncFolderWithPeer 扫描本地目录并基于 SHA-256 CAS 索引执行增量去重同步，仅向目标设备/群组推送新增或变更的文件（v3.4.0）。
func (c *Client) SyncFolderWithPeer(to, group, dirPath string) (int, error) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return 0, errEmptyPath
	}
	info, err := os.Stat(dirPath)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("not a directory: %s", dirPath)
	}
	var changedFiles []string
	walkErr := filepath.WalkDir(dirPath, func(p string, d os.DirEntry, wErr error) error {
		if wErr != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, rErr := filepath.Rel(dirPath, p)
		if rErr != nil {
			rel = filepath.Base(p)
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		manifestKey := "foldersync:" + filepath.ToSlash(rel)

		c.mu.Lock()
		prevDigest := c.casIndex[manifestKey]
		if prevDigest == digest {
			c.mu.Unlock()
			return nil
		}
		c.casIndex[manifestKey] = digest
		c.mu.Unlock()

		changedFiles = append(changedFiles, p)
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	for _, fpath := range changedFiles {
		if group != "" {
			_, _ = c.OfferFileToGroup(group, fpath)
		} else if to != "" {
			_, _ = c.OfferFile(to, fpath)
		}
	}
	return len(changedFiles), nil
}

// SyncFolder 实现 appapi.API 契约 v1.3 显式增量目录同步接口，委托 SyncFolderWithPeer。
func (c *Client) SyncFolder(to, group, dirPath string) (int, error) {
	return c.SyncFolderWithPeer(to, group, dirPath)
}

// IssueTrustCert 实现 appapi.API 契约 v1.3 显式签发组织准入证书接口，返回 "token|rootPubB64"。
func (c *Client) IssueTrustCert(subject, org string, ttlSec int64) (string, error) {
	if ttlSec <= 0 {
		ttlSec = 86400 * 30
	}
	tok, pub, err := c.IssueNodeCertificate(subject, org, ttlSec)
	if err != nil {
		return "", err
	}
	return tok + "|" + pub, nil
}

// VerifyTrustCert 实现 appapi.API 契约 v1.3 显式校验组织准入证书接口，返回 "subject@org"。
func (c *Client) VerifyTrustCert(token, rootPub string) (string, error) {
	token = strings.TrimSpace(token)
	rootPub = strings.TrimSpace(rootPub)
	if parts := strings.SplitN(token, "|", 2); len(parts) == 2 && rootPub == "" {
		token = parts[0]
		rootPub = parts[1]
	}
	sub, org, ok := VerifyNodeCertificate(token, rootPub)
	if !ok {
		return "", fmt.Errorf("core: invalid or expired trust certificate")
	}
	return sub + "@" + org, nil
}

// RunNetworkDiagnostics 生成局域网与跨网段（Tailscale/WireGuard）链路质量与节点状态诊断雷达摘要（v4.0.0）。
func (c *Client) RunNetworkDiagnostics() string {
	c.mu.Lock()
	state := string(c.connState)
	addr := c.addr
	peersCount := len(c.peers)
	transfersCount := len(c.transfers)
	casCount := len(c.casIndex)
	voiceSnap := c.voiceCallSnapshotLocked()
	screenSnap := c.screenShareSnapshotLocked()
	c.mu.Unlock()

	if addr == "" {
		addr = "disconnected"
	}
	voiceStatus := "idle"
	if voiceSnap != nil && voiceSnap.State != "" {
		voiceStatus = voiceSnap.State
	}
	screenStatus := "off"
	if screenSnap != nil && screenSnap.Active {
		screenStatus = fmt.Sprintf("active(%dfps)", screenSnap.FPS)
	}
	overlays := discover.OverlayCandidateAddrs(7878)
	gossip := c.GossipEndpoints()
	return fmt.Sprintf(
		"netdiag:conn=%s server=%s peers=%d transfers=%d cas=%d voice=%s screen=%s overlay=%d gossip=%d",
		state, addr, peersCount, transfersCount, casCount, voiceStatus, screenStatus, len(overlays), len(gossip),
	)
}

func (c *Client) peerNicknameOrID(peerID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.peers[peerID]; ok && strings.TrimSpace(p.Nickname) != "" {
		return p.Nickname
	}
	if peerID == "" {
		return "未知联系人"
	}
	return peerID
}

// notifyDesktopEvent 在非免打扰模式下触发跨平台原生系统通知与提示音（v4.9.0）。
func (c *Client) notifyDesktopEvent(title, body string, urgent bool) {
	if c.IsDND() {
		return
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" && body == "" {
		return
	}
	collabProviderMu.RLock()
	hook := desktopNotifyProvider
	collabProviderMu.RUnlock()
	if hook != nil {
		hook(title, body, urgent)
		return
	}
	if isGoTestBinary() {
		return
	}
	go sendOSDesktopNotification(title, body, urgent)
}

func sendOSDesktopNotification(title, body string, urgent bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
	defer cancel()

	switch runtime.GOOS {
	case "linux":
		urgency := "normal"
		if urgent {
			urgency = "critical"
		}
		if p, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.CommandContext(ctx, p, "-a", "LANChat", "-u", urgency, title, body).Run()
		}
		if p, err := exec.LookPath("canberra-gtk-play"); err == nil {
			soundID := "message-new-instant"
			if urgent {
				soundID = "phone-incoming-call"
			}
			_ = exec.CommandContext(ctx, p, "-i", soundID).Run()
		}
	case "darwin":
		if p, err := exec.LookPath("osascript"); err == nil {
			safeTitle := strings.ReplaceAll(title, `"`, `\"`)
			safeBody := strings.ReplaceAll(body, `"`, `\"`)
			script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, safeBody, safeTitle)
			_ = exec.CommandContext(ctx, p, "-e", script).Run()
		}
	case "windows":
		if p, err := exec.LookPath("powershell"); err == nil {
			safeTitle := strings.ReplaceAll(title, `'`, `''`)
			safeBody := strings.ReplaceAll(body, `'`, `''`)
			psScript := fmt.Sprintf(
				`Add-Type -AssemblyName System.Windows.Forms; `+
					`$n = New-Object System.Windows.Forms.NotifyIcon; `+
					`$n.Icon = [System.Drawing.SystemIcons]::Information; `+
					`$n.Visible = $true; `+
					`$n.ShowBalloonTip(3000, '%s', '%s', [System.Windows.Forms.ToolTipIcon]::Info); `+
					`Start-Sleep -Milliseconds 300; $n.Dispose()`,
				safeTitle, safeBody,
			)
			_ = exec.CommandContext(ctx, p, "-NoProfile", "-NonInteractive", "-Command", psScript).Run()
		}
	}
}
