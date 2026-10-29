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

	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
)

const (
	defaultVoiceJitterWindow    = 40 * time.Millisecond
	defaultVoiceUDPProbeTimeout = 2 * time.Second
	maxVoiceMeshPeers           = 8
	voiceSpeakingRMSThreshold   = 0.015
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

// voiceSession 管理单次 1:1 实时语音呼叫或 G2 频道多人 Mesh 实时语音房间（阶段四 v2.0 / v5.3.0 / v7.2.0）。
type voiceSession struct {
	mu               sync.Mutex
	conn             net.PacketConn
	localAddr        string
	remoteAddr       *net.UDPAddr
	meshPeers        map[string]*net.UDPAddr
	peerMuted        map[string]bool
	peerSpeaking     map[string]float64
	peerLastSpeakMs  map[string]int64
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

// mixPCM16LEFrames 对多路（≤8 路）16-bit 小端有符号单声道 PCM 帧执行软混音与 [-32768, 32767] 饱和防破音裁剪（v7.2.0）。
func mixPCM16LEFrames(frames [][]byte) []byte {
	var valid [][]byte
	maxLen := 0
	for _, f := range frames {
		if len(f) == 0 {
			continue
		}
		valid = append(valid, f)
		if len(f) > maxLen {
			maxLen = len(f)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	if len(valid) == 1 {
		return append([]byte(nil), valid[0]...)
	}
	if len(valid) > maxVoiceMeshPeers {
		valid = valid[:maxVoiceMeshPeers]
	}
	out := make([]byte, maxLen)
	samplePairs := maxLen / 2
	for i := 0; i < samplePairs; i++ {
		off := i * 2
		var sum int32
		for _, f := range valid {
			if off+1 < len(f) {
				s := int16(binary.LittleEndian.Uint16(f[off : off+2]))
				sum += int32(s)
			}
		}
		if sum > 32767 {
			sum = 32767
		} else if sum < -32768 {
			sum = -32768
		}
		binary.LittleEndian.PutUint16(out[off:off+2], uint16(int16(sum)))
	}
	if maxLen%2 == 1 {
		lastIdx := maxLen - 1
		var bSum int
		for _, f := range valid {
			if lastIdx < len(f) {
				bSum += int(f[lastIdx])
			}
		}
		out[lastIdx] = byte(bSum / len(valid))
	}
	return out
}

// computePCMRMSLevel 计算一帧 16-bit 小端有符号 PCM 音频的归一化均方根音量（[0.0, 1.0]，用于发言人音量指示 Speaking Indicator，v7.2.0）。
func computePCMRMSLevel(pcm []byte) float64 {
	if len(pcm) == 0 {
		return 0
	}
	samplePairs := len(pcm) / 2
	if samplePairs == 0 {
		return float64(pcm[0]) / 255.0
	}
	var sumSq float64
	for i := 0; i < samplePairs; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		norm := float64(s) / 32768.0
		sumSq += norm * norm
	}
	rms := math.Sqrt(sumSq / float64(samplePairs))
	if rms > 1.0 {
		rms = 1.0
	}
	return rms
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

// SetVoiceMuted 设置当前实时语音通话麦克风静音状态并向对端/语音频道实时广播静音标记（v5.3.0 / v7.2.0）。
func (c *Client) SetVoiceMuted(muted bool) {
	c.voiceMuted.Store(muted)
	selfID := c.selfIDValue()
	var to, group string
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs != nil {
		vs.mu.Lock()
		if vs.peerMuted == nil {
			vs.peerMuted = make(map[string]bool)
		}
		if vs.peerSpeaking == nil {
			vs.peerSpeaking = make(map[string]float64)
		}
		if selfID != "" {
			vs.peerMuted[selfID] = muted
			if muted {
				vs.peerSpeaking[selfID] = 0
			}
		}
		to = vs.targetPeer
		group = vs.targetGrp
		vs.mu.Unlock()
	}
	if to != "" || group != "" {
		if _, err := c.connectedConn(); err == nil {
			flag := "0"
			if muted {
				flag = "1"
			}
			_ = c.sendControlText(to, group, "sys:call:mute:"+flag)
		}
	}
	if vs != nil {
		c.emitVoiceCallState()
	}
}

// ToggleVoiceMuted 切换当前实时语音通话麦克风静音状态并返回切换后的状态。
func (c *Client) ToggleVoiceMuted() bool {
	next := !c.voiceMuted.Load()
	c.SetVoiceMuted(next)
	return next
}

// IsVoiceMuted 返回当前实时语音通话是否处于麦克风静音状态。
func (c *Client) IsVoiceMuted() bool {
	return c.voiceMuted.Load()
}

// onInboundVoiceMute 处理对端或频道成员广播的 sys:call:mute:1|0 静音状态变更（v7.2.0）。
func (c *Client) onInboundVoiceMute(from, group string, muted bool) {
	if from == "" {
		return
	}
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return
	}
	vs.mu.Lock()
	if vs.peerMuted == nil {
		vs.peerMuted = make(map[string]bool)
	}
	if vs.peerSpeaking == nil {
		vs.peerSpeaking = make(map[string]float64)
	}
	vs.peerMuted[from] = muted
	if muted {
		vs.peerSpeaking[from] = 0
	}
	vs.mu.Unlock()
	c.emitVoiceCallState()
}

// AddVoiceMeshPeer 将频道成员加入本地多人语音 Mesh 拓扑（最多支持 8 名成员并发混音，v7.2.0）。
func (c *Client) AddVoiceMeshPeer(peerID, remoteUDP string) bool {
	peerID = strings.TrimSpace(peerID)
	remoteUDP = strings.TrimSpace(remoteUDP)
	if peerID == "" && remoteUDP == "" {
		return false
	}
	var uaddr *net.UDPAddr
	if remoteUDP != "" {
		uaddr, _ = net.ResolveUDPAddr("udp", remoteUDP)
	}
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return false
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.meshPeers == nil {
		vs.meshPeers = make(map[string]*net.UDPAddr)
	}
	if vs.peerMuted == nil {
		vs.peerMuted = make(map[string]bool)
	}
	if vs.peerSpeaking == nil {
		vs.peerSpeaking = make(map[string]float64)
	}
	if vs.peerLastSpeakMs == nil {
		vs.peerLastSpeakMs = make(map[string]int64)
	}
	if peerID != "" {
		if _, exists := vs.meshPeers[peerID]; !exists && len(vs.meshPeers) >= maxVoiceMeshPeers {
			return false
		}
		if uaddr != nil {
			vs.meshPeers[peerID] = uaddr
		} else if _, exists := vs.meshPeers[peerID]; !exists {
			vs.meshPeers[peerID] = nil
		}
	}
	if uaddr != nil && vs.remoteAddr == nil {
		vs.remoteAddr = uaddr
	}
	if vs.state != "active" && uaddr != nil {
		vs.state = "active"
	}
	vs.markActiveNow()
	return true
}

// VoiceMeshPeers 返回当前语音频道 Mesh 房间内已绑定的全部成员 ID（按字典序排序，≤8 人，v7.2.0）。
func (c *Client) VoiceMeshPeers() []string {
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	out := make([]string, 0, len(vs.meshPeers))
	for pid := range vs.meshPeers {
		out = append(out, pid)
	}
	sort.Strings(out)
	return out
}

// IsPeerVoiceMuted 查询语音频道中指定成员当前是否处于静音状态（v7.2.0）。
func (c *Client) IsPeerVoiceMuted(peerID string) bool {
	if peerID == "" || peerID == c.selfIDValue() {
		return c.IsVoiceMuted()
	}
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return false
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.peerMuted[peerID]
}

// PeerSpeakingLevel 返回指定成员最近的归一化发言音量（[0.0, 1.0]，v7.2.0）。
func (c *Client) PeerSpeakingLevel(peerID string) float64 {
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return 0
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.peerMuted[peerID] {
		return 0
	}
	return vs.peerSpeaking[peerID]
}

// ActiveVoiceSpeakers 返回当前正在发言（RMS 音量超过阈值且未静音）的成员 ID 列表（v7.2.0）。
func (c *Client) ActiveVoiceSpeakers() []string {
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	var speakers []string
	for pid, lvl := range vs.peerSpeaking {
		if !vs.peerMuted[pid] && lvl >= voiceSpeakingRMSThreshold {
			speakers = append(speakers, pid)
		}
	}
	sort.Strings(speakers)
	return speakers
}

// MixIncomingVoiceFrames 对频道内多成员并发到达的 PCM 音频帧执行静音过滤、发言音量测算与 16-bit 软混音（v7.2.0）。
func (c *Client) MixIncomingVoiceFrames(peerFrames map[string][]byte) []byte {
	if len(peerFrames) == 0 {
		return nil
	}
	_ = c.ensureLocalVoiceUDP("", "")
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs == nil {
		return nil
	}
	nowMs := time.Now().UnixMilli()
	var activeFrames [][]byte

	vs.mu.Lock()
	if vs.peerMuted == nil {
		vs.peerMuted = make(map[string]bool)
	}
	if vs.peerSpeaking == nil {
		vs.peerSpeaking = make(map[string]float64)
	}
	if vs.peerLastSpeakMs == nil {
		vs.peerLastSpeakMs = make(map[string]int64)
	}
	if vs.meshPeers == nil {
		vs.meshPeers = make(map[string]*net.UDPAddr)
	}
	peers := make([]string, 0, len(peerFrames))
	for pid := range peerFrames {
		peers = append(peers, pid)
	}
	sort.Strings(peers)
	for _, pid := range peers {
		pcm := peerFrames[pid]
		if len(pcm) == 0 {
			continue
		}
		if _, exists := vs.meshPeers[pid]; !exists && len(vs.meshPeers) < maxVoiceMeshPeers {
			vs.meshPeers[pid] = nil
		}
		if vs.peerMuted[pid] {
			vs.peerSpeaking[pid] = 0
			continue
		}
		rms := computePCMRMSLevel(pcm)
		vs.peerSpeaking[pid] = rms
		if rms >= voiceSpeakingRMSThreshold {
			vs.peerLastSpeakMs[pid] = nowMs
		}
		if len(activeFrames) < maxVoiceMeshPeers {
			activeFrames = append(activeFrames, pcm)
		}
	}
	cb := vs.onFrame
	vs.mu.Unlock()

	if len(activeFrames) == 0 {
		return nil
	}
	mixed := mixPCM16LEFrames(activeFrames)
	seq := uint32(vs.framesIn.Add(1))
	if cb != nil {
		cb(seq, nowMs, mixed)
	}
	return mixed
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
		c.setVoiceRemoteUDPForPeer(to, remoteUDP)
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

// onIncomingVoiceCallOffer 处理对端发来的 sys:call:offer:<remoteUDP>，支持 1:1 响铃与 G2 频道多人语音房间自动 Mesh 组网（v4.9.0 / v7.2.0）。
func (c *Client) onIncomingVoiceCallOffer(from, group, remoteUDP string) {
	c.mu.Lock()
	alreadyInSameGroup := false
	if c.voice != nil && group != "" {
		c.voice.mu.Lock()
		alreadyInSameGroup = c.voice.targetGrp == group && (c.voice.state == "calling" || c.voice.state == "active")
		c.voice.mu.Unlock()
	}
	c.mu.Unlock()

	if err := c.ensureLocalVoiceUDP(from, group); err != nil {
		return
	}
	uaddr, _ := net.ResolveUDPAddr("udp", strings.TrimSpace(remoteUDP))
	c.mu.Lock()
	vs := c.voice
	c.mu.Unlock()
	if vs != nil {
		vs.mu.Lock()
		if vs.targetPeer == "" || group == "" {
			vs.targetPeer = from
		}
		if group != "" {
			vs.targetGrp = group
		}
		if uaddr != nil {
			if vs.meshPeers == nil {
				vs.meshPeers = make(map[string]*net.UDPAddr)
			}
			if _, exists := vs.meshPeers[from]; exists || len(vs.meshPeers) < maxVoiceMeshPeers {
				vs.meshPeers[from] = uaddr
			}
			if vs.remoteAddr == nil || group == "" {
				vs.remoteAddr = uaddr
			}
		}
		if alreadyInSameGroup {
			vs.state = "active"
			vs.markActiveNow()
		} else {
			vs.state = "ringing"
		}
		vs.mu.Unlock()
	}
	if alreadyInSameGroup {
		localAddr := c.voiceLocalAddr()
		if _, err := c.connectedConn(); err == nil && localAddr != "" {
			_ = c.sendControlText(from, group, "sys:call:accept:"+localAddr)
		}
		c.emitVoiceCallState()
		return
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
// 支持 G2 频道多人语音房间（≤8 人）Mesh 多路并发广播；当本机处于静音状态时静默丢弃；
// 当 UDP 连续 2s 不可达或 WriteTo 失败时自动回退控制面加密信令通道（v5.3.0 / v7.2.0）。
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
	selfID := c.selfIDValue()
	rms := computePCMRMSLevel(pcm)
	nowMs := time.Now().UnixMilli()

	vs.mu.Lock()
	conn := vs.conn
	raddr := vs.remoteAddr
	targetPeer := vs.targetPeer
	targetGrp := vs.targetGrp
	if vs.peerSpeaking == nil {
		vs.peerSpeaking = make(map[string]float64)
	}
	if vs.peerLastSpeakMs == nil {
		vs.peerLastSpeakMs = make(map[string]int64)
	}
	if selfID != "" {
		vs.peerSpeaking[selfID] = rms
		if rms >= voiceSpeakingRMSThreshold {
			vs.peerLastSpeakMs[selfID] = nowMs
		}
	}
	var meshDest []*net.UDPAddr
	seenAddr := make(map[string]bool)
	for _, addr := range vs.meshPeers {
		if addr != nil {
			key := addr.String()
			if !seenAddr[key] && len(meshDest) < maxVoiceMeshPeers {
				seenAddr[key] = true
				meshDest = append(meshDest, addr)
			}
		}
	}
	if raddr != nil && !seenAddr[raddr.String()] && len(meshDest) < maxVoiceMeshPeers {
		meshDest = append(meshDest, raddr)
	}
	vs.mu.Unlock()

	vs.markActiveNow()
	seq := vs.seq.Add(1)
	tsMs := nowMs

	_, wsErr := c.connectedConn()
	hasSignaling := wsErr == nil && (targetPeer != "" || targetGrp != "")

	var udpWriteErr error
	if conn != nil && len(meshDest) > 0 && (!vs.shouldFallbackToRelay(time.Now()) || !hasSignaling) {
		pkt := make([]byte, len(voicePacketMagic)+4+8+len(pcm))
		copy(pkt, voicePacketMagic)
		binary.BigEndian.PutUint32(pkt[len(voicePacketMagic):], seq)
		binary.BigEndian.PutUint64(pkt[len(voicePacketMagic)+4:], uint64(tsMs))
		copy(pkt[len(voicePacketMagic)+12:], pcm)
		wroteAny := false
		for _, dest := range meshDest {
			if _, err := conn.WriteTo(pkt, dest); err == nil {
				wroteAny = true
			} else {
				udpWriteErr = err
			}
		}
		if wroteAny {
			vs.framesOut.Add(1)
			return nil
		}
		vs.udpRelayFallback.Store(true)
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
	rms := computePCMRMSLevel(pcm)
	vs.mu.Lock()
	if vs.state != "active" {
		vs.state = "active"
	}
	if vs.peerSpeaking == nil {
		vs.peerSpeaking = make(map[string]float64)
	}
	if vs.peerLastSpeakMs == nil {
		vs.peerLastSpeakMs = make(map[string]int64)
	}
	if vs.peerMuted != nil && vs.peerMuted[from] {
		vs.peerSpeaking[from] = 0
		vs.mu.Unlock()
		return
	}
	if from != "" {
		vs.peerSpeaking[from] = rms
		if rms >= voiceSpeakingRMSThreshold {
			vs.peerLastSpeakMs[from] = ts
		}
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

// VoiceCallStatus 返回当前实时语音通话状态摘要（含 Mesh 成员数与活跃发言人数）。
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
	meshCount := len(vs.meshPeers)
	if meshCount == 0 && vs.remoteAddr != nil {
		meshCount = 1
	}
	speakers := 0
	for pid, lvl := range vs.peerSpeaking {
		if !vs.peerMuted[pid] && lvl >= voiceSpeakingRMSThreshold {
			speakers++
		}
	}
	return fmt.Sprintf("%s local=%s remote=%s in=%d out=%d mode=%s muted=%t mesh=%d speakers=%d",
		vs.state, vs.localAddr, remote, vs.framesIn.Load(), vs.framesOut.Load(), mode, c.IsVoiceMuted(), meshCount, speakers)
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
			composed := c.recordScreenFrame(peer, grp, 15, frame)
			c.listener().OnMessageReceived(peer, grp, "udp-screen", "SCREEN_FRAME", composed)
			c.emitScreenShareFrame(peer, grp, 15, composed)
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
		var senderID string
		if udpAddr, ok := addr.(*net.UDPAddr); ok {
			if vs.remoteAddr == nil {
				vs.remoteAddr = udpAddr
				vs.state = "active"
			}
			for pid, mAddr := range vs.meshPeers {
				if mAddr != nil && mAddr.String() == udpAddr.String() {
					senderID = pid
					break
				}
			}
		}
		if senderID != "" && vs.peerMuted != nil && vs.peerMuted[senderID] {
			if vs.peerSpeaking != nil {
				vs.peerSpeaking[senderID] = 0
			}
			vs.mu.Unlock()
			continue
		}
		if senderID != "" {
			if vs.peerSpeaking == nil {
				vs.peerSpeaking = make(map[string]float64)
			}
			if vs.peerLastSpeakMs == nil {
				vs.peerLastSpeakMs = make(map[string]int64)
			}
			rms := computePCMRMSLevel(payload)
			vs.peerSpeaking[senderID] = rms
			if rms >= voiceSpeakingRMSThreshold {
				vs.peerLastSpeakMs[senderID] = ts
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
	c.setVoiceRemoteUDPForPeer("", remoteUDP)
}

func (c *Client) setVoiceRemoteUDPForPeer(peerID, remoteUDP string) {
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
	if peerID == "" {
		peerID = vs.targetPeer
	}
	if peerID != "" {
		if vs.meshPeers == nil {
			vs.meshPeers = make(map[string]*net.UDPAddr)
		}
		if _, exists := vs.meshPeers[peerID]; exists || len(vs.meshPeers) < maxVoiceMeshPeers {
			vs.meshPeers[peerID] = uaddr
		}
	}
	if vs.remoteAddr == nil || vs.targetGrp == "" {
		vs.remoteAddr = uaddr
	}
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

// SetScreenShareRegion 配置屏幕共享的目标显示器/窗口标识与归一化裁剪区域（0..1，v7.2.0）。
// target 支持 "display:0"、"display:1"、"window:active" 或自定义区域描述；cropW/cropH <= 0 表示全屏不裁剪。
func (c *Client) SetScreenShareRegion(target string, cropX, cropY, cropW, cropH float64) {
	if cropX < 0 {
		cropX = 0
	} else if cropX > 0.95 {
		cropX = 0.95
	}
	if cropY < 0 {
		cropY = 0
	} else if cropY > 0.95 {
		cropY = 0.95
	}
	if cropW < 0 {
		cropW = 0
	}
	if cropH < 0 {
		cropH = 0
	}
	if cropW > 0 && cropX+cropW > 1.0 {
		cropW = 1.0 - cropX
	}
	if cropH > 0 && cropY+cropH > 1.0 {
		cropH = 1.0 - cropY
	}
	c.mu.Lock()
	c.screenTarget = strings.TrimSpace(target)
	c.screenCropX = cropX
	c.screenCropY = cropY
	c.screenCropW = cropW
	c.screenCropH = cropH
	c.screenHasTileHash = false
	c.mu.Unlock()
}

// ScreenShareRegion 返回当前屏幕共享配置的显示器/窗口目标及归一化裁剪区域（v7.2.0）。
func (c *Client) ScreenShareRegion() (target string, x, y, w, h float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.screenTarget
	if t == "" {
		t = "display:0"
	}
	return t, c.screenCropX, c.screenCropY, c.screenCropW, c.screenCropH
}

// ScreenShareStats 返回最近一帧的脏矩形块数（0..64）、累计静止跳帧数与采集目标（v7.2.0）。
func (c *Client) ScreenShareStats() (dirtyTiles int, skippedFrames uint64, target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.screenTarget
	if t == "" {
		t = "display:0"
	}
	return c.screenDirtyTiles, c.screenSkipped, t
}

// computeScreenTileHashes 将图像划分为 8×8 共 64 个网格块，并计算每块的 64-bit FNV-1a 像素特征哈希（v7.2.0 脏矩形检测）。
func computeScreenTileHashes(img image.Image) [64]uint64 {
	var hashes [64]uint64
	if img == nil {
		return hashes
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return hashes
	}
	for ty := 0; ty < 8; ty++ {
		y0 := b.Min.Y + ty*h/8
		y1 := b.Min.Y + (ty+1)*h/8
		if y1 <= y0 {
			y1 = y0 + 1
		}
		stepY := (y1 - y0) / 4
		if stepY < 1 {
			stepY = 1
		}
		for tx := 0; tx < 8; tx++ {
			x0 := b.Min.X + tx*w/8
			x1 := b.Min.X + (tx+1)*w/8
			if x1 <= x0 {
				x1 = x0 + 1
			}
			stepX := (x1 - x0) / 4
			if stepX < 1 {
				stepX = 1
			}
			var fnv uint64 = 14695981039346656037
			for y := y0; y < y1; y += stepY {
				for x := x0; x < x1; x += stepX {
					r, g, bl, _ := img.At(x, y).RGBA()
					// 量化高 5 位以过滤微小 JPEG 压缩噪点
					q := uint64(((r >> 11) << 10) | ((g >> 11) << 5) | (bl >> 11))
					fnv ^= q
					fnv *= 1099511628211
				}
			}
			hashes[ty*8+tx] = fnv
		}
	}
	return hashes
}

// diffScreenTileHashes 比较前后两帧的 8×8 块哈希，返回发生变化的脏块数（0..64）及外接包围网格（minTX, minTY, maxTX, maxTY）。
func diffScreenTileHashes(prev, cur [64]uint64) (dirtyTiles int, minTX, minTY, maxTX, maxTY int) {
	minTX, minTY = 8, 8
	maxTX, maxTY = -1, -1
	for idx := 0; idx < 64; idx++ {
		if prev[idx] != cur[idx] {
			dirtyTiles++
			tx := idx % 8
			ty := idx / 8
			if tx < minTX {
				minTX = tx
			}
			if ty < minTY {
				minTY = ty
			}
			if tx > maxTX {
				maxTX = tx
			}
			if ty > maxTY {
				maxTY = ty
			}
		}
	}
	if dirtyTiles == 0 {
		return 0, 0, 0, 0, 0
	}
	return dirtyTiles, minTX, minTY, maxTX, maxTY
}

func decodeDataURIImage(frameDataURI string) (image.Image, bool) {
	trimmed := strings.TrimSpace(frameDataURI)
	b64Idx := strings.Index(trimmed, ";base64,")
	if b64Idx < 0 {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(trimmed[b64Idx+len(";base64,"):]))
	if err != nil || len(raw) < 16 {
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	return img, true
}

// SendScreenFrameIfDirty 对抓屏帧执行 8×8 块哈希脏矩形比对：
// 若画面完全静止（dirtyTiles == 0）且距上次发送未超过保活周期（1.5s），自动跳过冗余帧传输以将静止带宽降至接近零（v7.2.0）。
func (c *Client) SendScreenFrameIfDirty(to, group, frameData string) (bool, int, error) {
	frameData = strings.TrimSpace(frameData)
	if frameData == "" {
		return false, 0, errEmptyText
	}
	nowMS := time.Now().UnixMilli()
	dirty := 64
	if img, ok := decodeDataURIImage(frameData); ok {
		curHashes := computeScreenTileHashes(img)
		c.mu.Lock()
		if c.screenHasTileHash {
			dirty, _, _, _, _ = diffScreenTileHashes(c.screenTileHashes, curHashes)
			if dirty == 0 && c.screenLastSentMS > 0 && nowMS-c.screenLastSentMS < 1500 {
				c.screenDirtyTiles = 0
				c.screenSkipped++
				c.mu.Unlock()
				return false, 0, nil
			}
		}
		c.screenTileHashes = curHashes
		c.screenHasTileHash = true
		c.screenDirtyTiles = dirty
		c.screenLastSentMS = nowMS
		c.mu.Unlock()
	} else {
		c.mu.Lock()
		if c.screenHasTileHash && c.rawScreenFrame == frameData && c.screenLastSentMS > 0 && nowMS-c.screenLastSentMS < 1500 {
			c.screenDirtyTiles = 0
			c.screenSkipped++
			c.mu.Unlock()
			return false, 0, nil
		}
		c.screenHasTileHash = true
		c.screenDirtyTiles = dirty
		c.screenLastSentMS = nowMS
		c.mu.Unlock()
	}
	if err := c.SendScreenFrame(to, group, frameData); err != nil {
		return false, dirty, err
	}
	return true, dirty, nil
}

// StartScreenShare 开启局域网低延迟屏幕/窗口共享会话（阶段四 v2.0 / 契约 v1.2 / v4.8.0 / v7.2.0 脏矩形与自适应帧率管线）。
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
	c.screenHasTileHash = false
	c.screenLastSentMS = 0
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
// 若已建立实时 UDP 通道、单包 ≤60 KiB 且 UDP 数据面可达，优先走 UDP 数据面直传（支持多人语音频道 Mesh 多路广播）；
// 若连续 2s 未收到对端 UDP 入站包或 WriteTo 失败，自动回退加密信令控制帧（v3.1.0 / v5.3.0 / v7.2.0）。
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
		var meshDest []*net.UDPAddr
		seen := make(map[string]bool)
		for _, mAddr := range vs.meshPeers {
			if mAddr != nil && !seen[mAddr.String()] && len(meshDest) < maxVoiceMeshPeers {
				seen[mAddr.String()] = true
				meshDest = append(meshDest, mAddr)
			}
		}
		if raddr != nil && !seen[raddr.String()] && len(meshDest) < maxVoiceMeshPeers {
			meshDest = append(meshDest, raddr)
		}
		vs.mu.Unlock()
		if conn != nil && len(meshDest) > 0 {
			pkt := make([]byte, len(screenPacketMagic)+len(frameData))
			copy(pkt, screenPacketMagic)
			copy(pkt[len(screenPacketMagic):], frameData)
			wroteAny := false
			for _, dest := range meshDest {
				if _, err := conn.WriteTo(pkt, dest); err == nil {
					wroteAny = true
				}
			}
			if wroteAny {
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
	c.rawScreenFrame = ""
	c.latestScreenFrame = ""
	c.screenAnnotX = 0
	c.screenAnnotY = 0
	c.screenAnnotNote = ""
	c.screenAnnotTS = 0
	c.screenHasTileHash = false
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
	if interval < 33*time.Millisecond {
		interval = 33 * time.Millisecond
	}
	if interval > 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	seq := 0
	pushOne := func() {
		seq++
		c.mu.Lock()
		cropX, cropY, cropW, cropH := c.screenCropX, c.screenCropY, c.screenCropW, c.screenCropH
		c.mu.Unlock()
		frameURI, err := captureDesktopJPEGDataURIWithCrop(seq, cropX, cropY, cropW, cropH)
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
		sent, dirty, _ := c.SendScreenFrameIfDirty(targetPeer, targetGrp, frameURI)
		if !sent {
			return
		}
		effFPS := fps
		if dirty >= 8 && effFPS < 24 {
			effFPS = 24
		}
		c.mu.Lock()
		c.screenShareFPS = effFPS
		c.mu.Unlock()
		c.emitScreenShareFrame(c.selfIDValue(), targetGrp, effFPS, frameURI)
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

// captureDesktopJPEGDataURI 执行跨平台真实桌面抓屏，并自动缩放编码为紧凑 JPEG Data URI（≤ 48 KiB）。
func captureDesktopJPEGDataURI(seq int) (string, error) {
	return captureDesktopJPEGDataURIWithCrop(seq, 0, 0, 0, 0)
}

func captureDesktopJPEGDataURIWithCrop(seq int, cropX, cropY, cropW, cropH float64) (string, error) {
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
		scaled := cropAndNormalizeScreenJPEGBytes(raw, cropX, cropY, cropW, cropH, 55)
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(scaled), nil
	}

	raw, err := captureOSDesktopBytes()
	if err != nil || len(raw) == 0 {
		raw = renderSyntheticScreenJPEG(seq)
		if cropW > 0 && cropH > 0 {
			raw = cropAndNormalizeScreenJPEGBytes(raw, cropX, cropY, cropW, cropH, 55)
		}
	} else {
		raw = cropAndNormalizeScreenJPEGBytes(raw, cropX, cropY, cropW, cropH, 55)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func captureOSDesktopBytes() ([]byte, error) {
	if isGoTestBinary() {
		return nil, fmt.Errorf("desktop capture disabled in go test binary")
	}
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

func cropAndNormalizeScreenJPEGBytes(raw []byte, cropX, cropY, cropW, cropH float64, quality int) []byte {
	hasCrop := cropW > 0 && cropH > 0 && (cropX > 0 || cropY > 0 || cropW < 1.0 || cropH < 1.0)
	if !hasCrop && len(raw) <= 42*1024 && len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xD8 {
		return raw
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return renderSyntheticScreenJPEG(1)
	}
	bounds := img.Bounds()
	srcMinX, srcMinY := bounds.Min.X, bounds.Min.Y
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return renderSyntheticScreenJPEG(1)
	}
	if hasCrop {
		cx := srcMinX + int(cropX*float64(srcW))
		cy := srcMinY + int(cropY*float64(srcH))
		cw := int(cropW * float64(srcW))
		ch := int(cropH * float64(srcH))
		if cw < 1 {
			cw = 1
		}
		if ch < 1 {
			ch = 1
		}
		if cx+cw > bounds.Max.X {
			cw = bounds.Max.X - cx
		}
		if cy+ch > bounds.Max.Y {
			ch = bounds.Max.Y - cy
		}
		if cw > 0 && ch > 0 {
			srcMinX, srcMinY = cx, cy
			srcW, srcH = cw, ch
		}
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
		sy := srcMinY + y*srcH/dstH
		for x := 0; x < dstW; x++ {
			sx := srcMinX + x*srcW/dstW
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	if quality <= 0 || quality > 95 {
		quality = 55
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return renderSyntheticScreenJPEG(1)
	}
	return buf.Bytes()
}

func renderSyntheticScreenJPEG(seq int) []byte {
	const w, h = 120, 68
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	offset := (seq * 12) % w
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8((x + offset) % 256)
			g := uint8((y * 255) / h)
			b := uint8(140)
			if y < 12 {
				r, g, b = 0, 153, 255
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 45})
	return buf.Bytes()
}

// SendScreenAnnotation 在局域网屏幕共享会话中发送归一化坐标激光笔/画笔批注（v3.3.0 / v6.2.0 实时红点画布叠加）。
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
	c.applyScreenAnnotation(to, group, normX, normY, note)
	payload := fmt.Sprintf("sys:screen:annot:%.3f,%.3f:%s", normX, normY, note)
	return c.sendControlText(to, group, payload)
}

func (c *Client) onInboundScreenAnnotation(from, group, rawAnnot string) {
	normX, normY, note, ok := parseScreenAnnotationPayload(rawAnnot)
	if !ok {
		return
	}
	c.applyScreenAnnotation(from, group, normX, normY, note)
}

func parseScreenAnnotationPayload(raw string) (float64, float64, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, "", false
	}
	coordsPart := raw
	note := ""
	if idx := strings.IndexByte(raw, ':'); idx >= 0 {
		coordsPart = raw[:idx]
		note = strings.TrimSpace(raw[idx+1:])
	}
	xy := strings.Split(coordsPart, ",")
	if len(xy) != 2 {
		return 0, 0, "", false
	}
	nx, errX := strconv.ParseFloat(strings.TrimSpace(xy[0]), 64)
	ny, errY := strconv.ParseFloat(strings.TrimSpace(xy[1]), 64)
	if errX != nil || errY != nil {
		return 0, 0, "", false
	}
	if nx < 0 {
		nx = 0
	} else if nx > 1 {
		nx = 1
	}
	if ny < 0 {
		ny = 0
	} else if ny > 1 {
		ny = 1
	}
	return nx, ny, note, true
}

func (c *Client) applyScreenAnnotation(peer, group string, normX, normY float64, note string) {
	nowMS := time.Now().UnixMilli()
	c.mu.Lock()
	if peer != "" && c.screenSharePeer == "" {
		c.screenSharePeer = peer
	}
	if group != "" && c.screenShareGroup == "" {
		c.screenShareGroup = group
	}
	c.screenAnnotX = normX
	c.screenAnnotY = normY
	c.screenAnnotNote = note
	c.screenAnnotTS = nowMS
	rawFrame := c.rawScreenFrame
	if rawFrame == "" {
		rawFrame = c.latestScreenFrame
		c.rawScreenFrame = rawFrame
	}
	fps := c.screenShareFPS
	targetPeer := c.screenSharePeer
	targetGrp := c.screenShareGroup
	composed := ""
	if rawFrame != "" {
		composed = overlayLaserAnnotationOnJPEG(rawFrame, normX, normY)
		c.latestScreenFrame = composed
	}
	c.mu.Unlock()

	if composed != "" {
		c.emitScreenShareFrame(targetPeer, targetGrp, fps, composed)
	}
}

// overlayLaserAnnotationOnJPEG 在 Base64 Data URI 图像帧的归一化坐标 (normX, normY) 处绘制醒目激光笔红点与光晕外环（阶段二 v6.2.0）。
func overlayLaserAnnotationOnJPEG(frameDataURI string, normX, normY float64) string {
	trimmed := strings.TrimSpace(frameDataURI)
	if !strings.HasPrefix(trimmed, "data:image/") {
		return frameDataURI
	}
	b64Idx := strings.Index(trimmed, ";base64,")
	if b64Idx < 0 {
		return frameDataURI
	}
	b64Raw := strings.TrimSpace(trimmed[b64Idx+len(";base64,"):])
	if b64Raw == "" {
		return frameDataURI
	}
	imgBytes, err := base64.StdEncoding.DecodeString(b64Raw)
	if err != nil || len(imgBytes) < 16 {
		return frameDataURI
	}
	srcImg, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return frameDataURI
	}
	bounds := srcImg.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return frameDataURI
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, y, srcImg.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	cx := int(normX * float64(w-1))
	cy := int(normY * float64(h-1))
	if cx < 0 {
		cx = 0
	} else if cx >= w {
		cx = w - 1
	}
	if cy < 0 {
		cy = 0
	} else if cy >= h {
		cy = h - 1
	}
	rOuter := w / 22
	if h/22 < rOuter {
		rOuter = h / 22
	}
	if rOuter < 7 {
		rOuter = 7
	}
	rInner := rOuter / 2
	if rInner < 3 {
		rInner = 3
	}
	rCore := rInner / 2
	if rCore < 1 {
		rCore = 1
	}
	rOuterSq := rOuter * rOuter
	rInnerSq := rInner * rInner
	rCoreSq := rCore * rCore
	for dy := -rOuter; dy <= rOuter; dy++ {
		py := cy + dy
		if py < 0 || py >= h {
			continue
		}
		for dx := -rOuter; dx <= rOuter; dx++ {
			px := cx + dx
			if px < 0 || px >= w {
				continue
			}
			distSq := dx*dx + dy*dy
			if distSq > rOuterSq {
				continue
			}
			switch {
			case distSq <= rCoreSq:
				dst.SetRGBA(px, py, color.RGBA{R: 255, G: 245, B: 245, A: 255})
			case distSq <= rInnerSq:
				dst.SetRGBA(px, py, color.RGBA{R: 255, G: 36, B: 36, A: 255})
			default:
				orig := dst.RGBAAt(px, py)
				dst.SetRGBA(px, py, color.RGBA{
					R: uint8((int(orig.R) + 255*2) / 3),
					G: uint8(int(orig.G) / 3),
					B: uint8(int(orig.B) / 3),
					A: 255,
				})
			}
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 70}); err != nil {
		return frameDataURI
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out.Bytes())
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
	if !c.screenSharing && c.latestScreenFrame == "" && c.screenAnnotTS == 0 {
		return nil
	}
	return &appapi.ScreenShareState{
		Active:      c.screenSharing,
		PeerID:      c.screenSharePeer,
		GroupID:     c.screenShareGroup,
		FPS:         c.screenShareFPS,
		LatestFrame: c.latestScreenFrame,
		AnnotX:      c.screenAnnotX,
		AnnotY:      c.screenAnnotY,
		AnnotNote:   c.screenAnnotNote,
		AnnotTS:     c.screenAnnotTS,
	}
}

func (c *Client) recordScreenFrame(peer, group string, fps int, frame string) string {
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
	c.rawScreenFrame = frame
	composed := frame
	if c.screenAnnotTS > 0 && time.Now().UnixMilli()-c.screenAnnotTS <= 5000 {
		composed = overlayLaserAnnotationOnJPEG(frame, c.screenAnnotX, c.screenAnnotY)
	}
	c.latestScreenFrame = composed
	c.mu.Unlock()
	return composed
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
		case strings.HasPrefix(arg, "region "):
			raw := strings.TrimSpace(strings.TrimPrefix(arg, "region "))
			parts := strings.Split(raw, ",")
			var rx, ry, rw, rh float64
			if len(parts) == 4 {
				rx, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				ry, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				rw, _ = strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
				rh, _ = strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			}
			c.SetScreenShareRegion("region", rx, ry, rw, rh)
			t, x, y, w, h := c.ScreenShareRegion()
			return fmt.Sprintf("screenshare:region:%s:%.2f,%.2f,%.2f,%.2f", t, x, y, w, h), true, nil
		case strings.HasPrefix(arg, "target "):
			tgt := strings.TrimSpace(strings.TrimPrefix(arg, "target "))
			if tgt == "" {
				tgt = "display:0"
			}
			_, x, y, w, h := c.ScreenShareRegion()
			c.SetScreenShareRegion(tgt, x, y, w, h)
			return "screenshare:target:" + tgt, true, nil
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

	case trimmed == "/folderwatch" || strings.HasPrefix(trimmed, "/folderwatch ") || strings.HasPrefix(trimmed, "/foldersync "):
		raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "/folderwatch"), "/foldersync"))
		switch {
		case raw == "stop" || raw == "off":
			c.StopFolderWatch()
			return "folderwatch:off", true, nil
		case raw == "status":
			active, dir, _, _, runs, files := c.FolderWatchStats()
			return fmt.Sprintf("folderwatch:active=%v dir=%s runs=%d files=%d", active, dir, runs, files), true, nil
		case strings.HasPrefix(raw, "watch ") || strings.HasPrefix(trimmed, "/folderwatch "):
			dirPath := strings.TrimSpace(strings.TrimPrefix(raw, "watch "))
			if dirPath == "" {
				return "", true, errEmptyPath
			}
			initial, err := c.StartFolderWatch(to, group, dirPath, 350*time.Millisecond)
			if err != nil {
				return "", true, err
			}
			return fmt.Sprintf("folderwatch:on:%d", initial), true, nil
		default:
			if raw == "" {
				return "", true, errEmptyPath
			}
			synced, err := c.SyncFolderWithPeer(to, group, raw)
			if err != nil {
				return "", true, err
			}
			return fmt.Sprintf("foldersync:queued:%d", synced), true, nil
		}

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

	case strings.HasPrefix(trimmed, "/chadmin ") || strings.HasPrefix(trimmed, "/chmute ") ||
		strings.HasPrefix(trimmed, "/chunmute ") || strings.HasPrefix(trimmed, "/chkick ") ||
		trimmed == "/chack" || strings.HasPrefix(trimmed, "/chack "):
		fields := strings.Fields(trimmed)
		cmd := fields[0]
		chID := group
		var memberID, action string
		switch cmd {
		case "/chadmin":
			action = "promote"
			if len(fields) >= 2 {
				memberID = fields[1]
			}
			if len(fields) >= 3 {
				if fields[2] == "demote" || fields[2] == "promote" {
					action = fields[2]
				} else {
					chID = fields[1]
					memberID = fields[2]
					if len(fields) >= 4 {
						action = fields[3]
					}
				}
			}
		case "/chmute":
			action = "mute"
			if len(fields) == 2 {
				memberID = fields[1]
			} else if len(fields) >= 3 {
				chID = fields[1]
				memberID = fields[2]
			}
		case "/chunmute":
			action = "unmute"
			if len(fields) == 2 {
				memberID = fields[1]
			} else if len(fields) >= 3 {
				chID = fields[1]
				memberID = fields[2]
			}
		case "/chkick":
			action = "kick"
			if len(fields) == 2 {
				memberID = fields[1]
			} else if len(fields) >= 3 {
				chID = fields[1]
				memberID = fields[2]
			}
		case "/chack":
			action = "ack_announcement"
			if len(fields) >= 2 && chID == "" {
				chID = fields[1]
			}
			memberID = c.selfIDValue()
		}
		if err := c.ChannelManageMember(chID, memberID, action); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("chgov:%s:%s:%s", chID, memberID, action), true, nil

	case trimmed == "/mailbox" || strings.HasPrefix(trimmed, "/mailbox "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/mailbox"))
		if strings.HasPrefix(arg, "send ") {
			body := strings.TrimSpace(strings.TrimPrefix(arg, "send "))
			id, err := c.EnqueueOfflineMailbox(to, body, "")
			if err != nil {
				return "", true, err
			}
			return "mailbox:queued:" + id, true, nil
		}
		return c.DrainOfflineMailbox(to), true, nil
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

// StartFolderWatch 启动本地文件夹双向实时监听同步（Watch Mode Folder Sync，v7.3.0）：
// 首次立即执行一次 CAS 增量扫描，随后按 interval 轮询监听目录内新增或修改文件并自动触发增量同步。
func (c *Client) StartFolderWatch(to, group, dirPath string, interval time.Duration) (int, error) {
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
	if interval < 40*time.Millisecond {
		interval = 350 * time.Millisecond
	}

	c.StopFolderWatch()
	initialSynced, err := c.SyncFolderWithPeer(to, group, dirPath)
	if err != nil {
		return 0, err
	}

	stopCh := make(chan struct{})
	c.mu.Lock()
	c.folderWatchActive = true
	c.folderWatchDir = dirPath
	c.folderWatchTo = to
	c.folderWatchGroup = group
	c.folderWatchRuns = 1
	c.folderWatchFiles = initialSynced
	c.folderWatchStop = stopCh
	c.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.runCtx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				synced, sErr := c.SyncFolderWithPeer(to, group, dirPath)
				if sErr != nil {
					continue
				}
				c.mu.Lock()
				c.folderWatchRuns++
				c.folderWatchFiles += synced
				c.mu.Unlock()
				if synced > 0 {
					c.listener().OnMessageReceived(
						to,
						group,
						"folder-watch",
						"FOLDER_WATCH_SYNC",
						fmt.Sprintf("📂 [实时目录监听已自动同步 %d 个变更文件]", synced),
					)
				}
			}
		}
	}()
	return initialSynced, nil
}

// StopFolderWatch 停止本地文件夹实时监听同步（v7.3.0）。
func (c *Client) StopFolderWatch() {
	c.mu.Lock()
	stopCh := c.folderWatchStop
	c.folderWatchStop = nil
	c.folderWatchActive = false
	c.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
}

// FolderWatchStats 返回当前本地文件夹实时监听同步的运行状态与累计统计（v7.3.0）。
func (c *Client) FolderWatchStats() (active bool, dirPath, to, group string, syncRuns, syncedFiles int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.folderWatchActive, c.folderWatchDir, c.folderWatchTo, c.folderWatchGroup, c.folderWatchRuns, c.folderWatchFiles
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

// channelGovState 维护 G2 自定义频道的精细化治理状态（管理员、禁言表、踢人记录与公告必读回执，v8.0.0 契约 v1.6）。
type channelGovState struct {
	admins          []string
	muted           []string
	announcementAck []string
	kicked          map[string]bool
}

// offlineMailboxItem 表示中心节点或本地暂存的一条 X25519 公钥密封离线信封（v8.0.0 契约 v1.6）。
type offlineMailboxItem struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Ciphertext string `json:"ciphertext"`
	SenderPub  string `json:"senderPub"`
	TS         int64  `json:"ts"`
}

func appendUniqueMember(slice []string, id string) []string {
	if id == "" || containsMember(slice, id) {
		return slice
	}
	return append(slice, id)
}

func removeMemberFromSlice(slice []string, id string) []string {
	if len(slice) == 0 || id == "" {
		return slice
	}
	out := make([]string, 0, len(slice))
	for _, m := range slice {
		if m != id {
			out = append(out, m)
		}
	}
	return out
}

func (c *Client) ensureChannelGovLocked(channelID string) *channelGovState {
	if c.channelGov == nil {
		c.channelGov = make(map[string]*channelGovState)
	}
	gov := c.channelGov[channelID]
	if gov == nil {
		gov = &channelGovState{
			kicked: make(map[string]bool),
		}
		c.channelGov[channelID] = gov
	} else if gov.kicked == nil {
		gov.kicked = make(map[string]bool)
	}
	return gov
}

func (c *Client) isChannelMemberMuted(channelID, memberID string) bool {
	if channelID == "" || memberID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.channels[channelID]; ok && containsMember(ch.Muted, memberID) {
		return true
	}
	if gov := c.channelGov[channelID]; gov != nil && containsMember(gov.muted, memberID) {
		return true
	}
	return false
}

func (c *Client) applyChannelGovernanceLocked(channelID, memberID, action string) []appapi.Channel {
	gov := c.ensureChannelGovLocked(channelID)
	switch action {
	case "promote":
		gov.admins = appendUniqueMember(gov.admins, memberID)
	case "demote":
		gov.admins = removeMemberFromSlice(gov.admins, memberID)
	case "mute":
		gov.muted = appendUniqueMember(gov.muted, memberID)
	case "unmute":
		gov.muted = removeMemberFromSlice(gov.muted, memberID)
	case "kick":
		gov.kicked[memberID] = true
		gov.admins = removeMemberFromSlice(gov.admins, memberID)
		gov.muted = removeMemberFromSlice(gov.muted, memberID)
	case "ack_announcement":
		gov.announcementAck = appendUniqueMember(gov.announcementAck, memberID)
	}

	if ch, ok := c.channels[channelID]; ok {
		if action == "kick" {
			ch.Members = removeMemberFromSlice(ch.Members, memberID)
		}
		ch.Admins = append([]string(nil), gov.admins...)
		ch.Muted = append([]string(nil), gov.muted...)
		ch.AnnouncementAck = append([]string(nil), gov.announcementAck...)
		c.channels[channelID] = ch
	}

	snapshot := make([]appapi.Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		snapshot = append(snapshot, ch)
	}
	return snapshot
}

// ChannelManageMember 执行频道精细化权限治理（promote/demote/mute/unmute/kick/ack_announcement，契约 v1.6）。
func (c *Client) ChannelManageMember(channelID, memberID, action string) error {
	channelID = strings.TrimSpace(channelID)
	memberID = strings.TrimSpace(memberID)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "ack" {
		action = "ack_announcement"
	}
	if channelID == "" || action == "" {
		return errInvalidState
	}
	switch action {
	case "promote", "demote", "mute", "unmute", "kick", "ack_announcement":
	default:
		return errInvalidState
	}

	selfID := c.selfIDValue()
	if action == "ack_announcement" && memberID == "" {
		memberID = selfID
	}
	if memberID == "" {
		return errPeerNotFound
	}

	c.mu.Lock()
	if ch, exists := c.channels[channelID]; exists {
		if action != "ack_announcement" {
			isOwner := ch.OwnerID != "" && ch.OwnerID == selfID
			isAdmin := containsMember(ch.Admins, selfID)
			if gov := c.channelGov[channelID]; gov != nil && containsMember(gov.admins, selfID) {
				isAdmin = true
			}
			if ch.OwnerID != "" && !isOwner && !isAdmin {
				c.mu.Unlock()
				return errChannelPermission
			}
			if (action == "mute" || action == "kick" || action == "demote") && ch.OwnerID != "" && memberID == ch.OwnerID {
				c.mu.Unlock()
				return errChannelPermission
			}
		}
	}
	snapshot := c.applyChannelGovernanceLocked(channelID, memberID, action)
	c.mu.Unlock()

	if _, err := c.connectedConn(); err == nil {
		ctrl := fmt.Sprintf("sys:chgov:%s:%s:%s", channelID, memberID, action)
		_ = c.sendControlText("", channelID, ctrl)
	}
	if len(snapshot) > 0 {
		c.listener().OnChannelUpdated(snapshot)
	}
	c.listener().OnMessageReceived(
		selfID,
		channelID,
		"chgov-"+channelID+"-"+action,
		"CHANNEL_GOVERNANCE",
		fmt.Sprintf("🛡️ [频道权限治理] %s → %s", action, memberID),
	)
	return nil
}

func (c *Client) onInboundChannelGovernance(actorID, channelID, memberID, action string) {
	channelID = strings.TrimSpace(channelID)
	memberID = strings.TrimSpace(memberID)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "ack" {
		action = "ack_announcement"
	}
	if channelID == "" || memberID == "" || action == "" {
		return
	}

	c.mu.Lock()
	selfID := c.selfID
	if selfID == "" {
		selfID = c.deviceID
	}
	if ch, exists := c.channels[channelID]; exists && action != "ack_announcement" {
		isOwner := ch.OwnerID == "" || ch.OwnerID == actorID
		isAdmin := containsMember(ch.Admins, actorID)
		if gov := c.channelGov[channelID]; gov != nil && containsMember(gov.admins, actorID) {
			isAdmin = true
		}
		if !isOwner && !isAdmin {
			c.mu.Unlock()
			return
		}
	}
	snapshot := c.applyChannelGovernanceLocked(channelID, memberID, action)
	kickedSelf := action == "kick" && memberID == selfID
	c.mu.Unlock()

	if len(snapshot) > 0 {
		c.listener().OnChannelUpdated(snapshot)
	}
	c.listener().OnMessageReceived(
		actorID,
		channelID,
		"chgov-"+channelID+"-"+action,
		"CHANNEL_GOVERNANCE",
		fmt.Sprintf("🛡️ [频道权限治理] %s → %s", action, memberID),
	)
	if kickedSelf {
		go func(id string) {
			_ = c.ChannelLeave(id)
		}(channelID)
	}
}

// EnqueueOfflineMailbox 向中心节点加密离线投递箱投递经接收方 X25519 公钥密封的离线信封（契约 v1.6）。
// 发送方发送完毕即可立即关机下线；中心节点仅暂存不可解密的 X25519 密文信封，接收方上线后自动或主动拉取并销毁。
func (c *Client) EnqueueOfflineMailbox(to, text, recipientPub string) (string, error) {
	to = strings.TrimSpace(to)
	text = strings.TrimSpace(text)
	recipientPub = strings.TrimSpace(recipientPub)
	if to == "" {
		return "", errPeerNotFound
	}
	if text == "" {
		return "", errEmptyText
	}

	c.mu.Lock()
	idPriv := c.identityPriv
	idPub := c.identityPub
	selfID := c.selfID
	if selfID == "" {
		selfID = c.deviceID
	}
	if recipientPub == "" {
		recipientPub = c.peerPubs[to]
	} else {
		c.peerPubs[to] = recipientPub
	}
	if recipientPub == "" {
		recipientPub = idPub
	}
	c.mu.Unlock()

	if idPriv == nil || recipientPub == "" {
		return "", errInvalidState
	}
	cipherText, err := transfer.SealECDHText(idPriv, recipientPub, text)
	if err != nil {
		return "", err
	}

	msgID := "mbox-" + uuid.NewString()
	nowTS := time.Now().UnixMilli()
	item := offlineMailboxItem{
		ID:         msgID,
		From:       selfID,
		To:         to,
		Ciphertext: cipherText,
		SenderPub:  idPub,
		TS:         nowTS,
	}

	c.history.append(appapi.ChatMessage{
		ID:   msgID,
		From: selfID,
		To:   to,
		Type: protocol.TextMsg,
		Text: text,
		TS:   nowTS,
	})

	rawItem, err := json.Marshal(item)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(rawItem)

	if conn, err := c.connectedConn(); err == nil {
		_, sendErr := c.sendTo(conn, protocol.TextMsg, to, protocol.TextPayload{
			Text:      "sys:mailbox:store:" + b64,
			SenderPub: idPub,
		})
		if sendErr != nil {
			c.mu.Lock()
			c.outboundMailbox = append(c.outboundMailbox, item)
			c.mu.Unlock()
		}
		if to == selfID {
			c.mu.Lock()
			c.inboundMailbox = append(c.inboundMailbox, item)
			c.mu.Unlock()
		}
	} else {
		c.mu.Lock()
		c.outboundMailbox = append(c.outboundMailbox, item)
		c.inboundMailbox = append(c.inboundMailbox, item)
		c.mu.Unlock()
	}
	return msgID, nil
}

func (c *Client) flushOutboundMailbox() {
	conn, err := c.connectedConn()
	if err != nil {
		return
	}
	c.mu.Lock()
	if len(c.outboundMailbox) == 0 {
		c.mu.Unlock()
		return
	}
	pending := c.outboundMailbox
	c.outboundMailbox = nil
	idPub := c.identityPub
	c.mu.Unlock()

	for _, item := range pending {
		rawItem, mErr := json.Marshal(item)
		if mErr != nil {
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(rawItem)
		_, _ = c.sendTo(conn, protocol.TextMsg, item.To, protocol.TextPayload{
			Text:      "sys:mailbox:store:" + b64,
			SenderPub: idPub,
		})
	}
}

func (c *Client) onInboundOfflineMailboxStore(fromPeer, b64 string) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) == 0 {
		return
	}
	var item offlineMailboxItem
	if json.Unmarshal(raw, &item) != nil || item.ID == "" || item.Ciphertext == "" {
		return
	}
	if item.From == "" {
		item.From = fromPeer
	}

	c.mu.Lock()
	idPriv := c.identityPriv
	if item.SenderPub != "" && item.From != "" {
		c.peerPubs[item.From] = item.SenderPub
	} else if item.SenderPub == "" && item.From != "" {
		item.SenderPub = c.peerPubs[item.From]
	}
	alreadyQueued := false
	for _, existing := range c.inboundMailbox {
		if existing.ID == item.ID {
			alreadyQueued = true
			break
		}
	}
	if !alreadyQueued {
		c.inboundMailbox = append(c.inboundMailbox, item)
	}
	c.mu.Unlock()

	if idPriv != nil && item.SenderPub != "" {
		if plain, decErr := transfer.OpenECDHText(idPriv, item.SenderPub, item.Ciphertext); decErr == nil && plain != "" {
			if c.history.append(appapi.ChatMessage{
				ID:   item.ID,
				From: item.From,
				To:   item.To,
				Type: protocol.TextMsg,
				Text: plain,
				TS:   item.TS,
			}) {
				c.recordInboundUnread(item.From, "", plain, nil)
				c.listener().OnMessageReceived(item.From, "", item.ID, protocol.TextMsg, plain)
			}
		}
	}
}

// DrainOfflineMailbox 从中心节点或本地待收投递箱拉取、解密并销毁离线加密信封（契约 v1.6）。
func (c *Client) DrainOfflineMailbox(fromPeer string) string {
	fromPeer = strings.TrimSpace(fromPeer)

	if _, err := c.connectedConn(); err == nil {
		for i := 0; i < 6; i++ {
			c.mu.Lock()
			n := len(c.inboundMailbox)
			c.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	c.mu.Lock()
	idPriv := c.identityPriv
	var toDrain []offlineMailboxItem
	var remaining []offlineMailboxItem
	for _, item := range c.inboundMailbox {
		if fromPeer == "" || item.From == fromPeer {
			toDrain = append(toDrain, item)
		} else {
			remaining = append(remaining, item)
		}
	}
	c.inboundMailbox = remaining
	c.mu.Unlock()

	drainedCount := 0
	for _, item := range toDrain {
		if idPriv != nil && item.SenderPub != "" {
			if plain, err := transfer.OpenECDHText(idPriv, item.SenderPub, item.Ciphertext); err == nil && plain != "" {
				drainedCount++
				if c.history.append(appapi.ChatMessage{
					ID:   item.ID,
					From: item.From,
					To:   item.To,
					Type: protocol.TextMsg,
					Text: plain,
					TS:   item.TS,
				}) {
					c.recordInboundUnread(item.From, "", plain, nil)
					c.listener().OnMessageReceived(item.From, "", item.ID, protocol.TextMsg, plain)
				}
				continue
			}
		}
		drainedCount++
	}
	return fmt.Sprintf("mailbox:drained:%d", drainedCount)
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
