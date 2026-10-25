// Package core 是 LANChat 唯一的生产客户端核心：
//   - 信令连接（WebSocket 拨号 / HELLO 握手 / PSK / 心跳 / 断线重连）；
//   - 在线表（USER_LIST 全量与 USER_JOIN / USER_LEAVE / PRESENCE_UPDATE 增量）；
//   - 文本与表情收发；
//   - 文件传输编排（P2P 直连 / 反向拨号 / AES-GCM 中继兜底）与事件分发。
//
// 设计原则：
//   - 控制面（WebSocket，≤1 MiB JSON）与数据面（独立 TCP 文件字节）严格分离；
//   - 直连优先，中继兜底，任务状态经 appapi.Listener 回调给 UI；
//   - 本包通过 appapi.API 对外暴露全部方法，方法集合对齐 api/ipc.schema.json。
package core

import (
	"context"
	"crypto/ecdh"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// Client 是一个 LANChat 客户端核心实例，实现 appapi.API。
//
// 并发模型：
//   - mu 保护全部快照态（连接态、自身信息、peers / transfers / channels）；
//   - writeMu 串行化 WebSocket 写入（coder/websocket 同一时刻仅允许一个写者）；
//   - 每个传输任务自带 ctx/cancel 与事件 channel，收泵据此路由信令。
type Client struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn

	connState appapi.ConnState
	addr      string
	psk       string
	selfID    string
	nickname  string
	server    string
	heartbeat int // 服务器下发的心跳间隔（秒）

	deviceID     string
	os           string
	identityPriv *ecdh.PrivateKey
	identityPub  string
	peerPubs     map[string]string
	autoMode     bool
	embeddedStop context.CancelFunc

	peers           map[string]appapi.Peer
	transfers       map[string]*transferTask
	groupTasks      map[string]*groupTask
	channels        map[string]appapi.Channel
	desiredChannels map[string]desiredChannel
	channelKeys     map[string]string
	channelKeySent  map[string]map[string]bool

	browser *discover.Browser

	downloadDir string
	history     *historyStore
	e2eeKey     string

	cb appapi.Listener

	// runCtx 在 Close 时取消，用于停止收泵 / 心跳等常驻 goroutine。
	runCtx    context.Context
	stopRun   context.CancelFunc
	closeOnce sync.Once

	// browserStarted 保证 mDNS 浏览 goroutine 只启动一次。
	browserStarted atomic.Bool

	// gen 标识当前会话，重连后递增；过期会话的 watcher 据此退出。
	gen uint64

	// forceRelay 为仅测试使用的钩子：禁止群文件数据面直连（外拨与入站均
	// 拒绝），确定性迫使传输回退中继。生产代码路径恒为 false。
	forceRelay bool
}

// Options 构造客户端时的参数。
type Options struct {
	// Nickname 初始昵称；为空时使用默认值。
	Nickname string
	// OS 上报给服务器与其他端的平台标识（如 windows/linux/macos/ios/android）。
	OS string
	// DownloadDir 默认下载目录；空表示不预设（RespondFile 时须显式给 dest）。
	DownloadDir string
	// Listener 事件回调；为空时事件被丢弃。
	Listener appapi.Listener
}

// New 创建一个未连接的客户端核心。
func New(opts Options) *Client {
	runCtx, cancel := context.WithCancel(context.Background())
	nick := opts.Nickname
	if nick == "" {
		nick = defaultNickname()
	}
	deviceID, idPriv, idPub := loadOrCreateIdentity(opts.DownloadDir)
	c := &Client{
		connState:       appapi.ConnDisconnected,
		nickname:        nick,
		os:              opts.OS,
		downloadDir:     opts.DownloadDir,
		history:         newHistoryStore(opts.DownloadDir),
		e2eeKey:         strings.TrimSpace(os.Getenv("LANCHAT_E2EE_KEY")),
		deviceID:        deviceID,
		identityPriv:    idPriv,
		identityPub:     idPub,
		peerPubs:        map[string]string{},
		heartbeat:       protocol.HeartbeatIntervalSec,
		peers:           map[string]appapi.Peer{},
		transfers:       map[string]*transferTask{},
		groupTasks:      map[string]*groupTask{},
		channels:        map[string]appapi.Channel{},
		desiredChannels: map[string]desiredChannel{},
		channelKeys:     map[string]string{},
		channelKeySent:  map[string]map[string]bool{},
		cb:              opts.Listener,
		runCtx:          runCtx,
		stopRun:         cancel,
	}
	return c
}

// SetListener 替换事件回调（通常在 UI 绑定阶段使用）。
func (c *Client) SetListener(l appapi.Listener) {
	c.mu.Lock()
	c.cb = l
	c.mu.Unlock()
}

// Close 停止全部常驻 goroutine 并关闭信令连接。
// 已在途的文件传输由各自任务的取消语义处理（详见 transfer 包）。
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.stopRun()
		c.mu.Lock()
		conn := c.conn
		c.conn = nil
		stopEmbedded := c.embeddedStop
		c.embeddedStop = nil
		tasks := make([]*transferTask, 0, len(c.transfers))
		for _, t := range c.transfers {
			tasks = append(tasks, t)
		}
		gtasks := make([]*groupTask, 0, len(c.groupTasks))
		for _, g := range c.groupTasks {
			gtasks = append(gtasks, g)
		}
		c.mu.Unlock()

		for _, t := range tasks {
			t.cancel()
		}
		for _, g := range gtasks {
			g.teardown()
		}
		if conn != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
		if stopEmbedded != nil {
			stopEmbedded()
		}
	})
}

// GetState 返回连接态 + 自身 + 在线表 + 传输 / 频道 / 历史消息快照。
func (c *Client) GetState() appapi.State {
	c.mu.Lock()
	defer c.mu.Unlock()

	peers := make([]appapi.Peer, 0, len(c.peers))
	for _, p := range c.peers {
		peers = append(peers, p)
	}
	transfers := make([]appapi.Transfer, 0, len(c.transfers)+len(c.groupTasks))
	for _, t := range c.transfers {
		transfers = append(transfers, t.snapshot())
	}
	for _, g := range c.groupTasks {
		transfers = append(transfers, g.currentSnapshot())
	}
	channels := make([]appapi.Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		channels = append(channels, ch)
	}

	return appapi.State{
		Conn:      c.connState,
		Server:    c.server,
		SelfID:    c.selfID,
		Nickname:  c.nickname,
		Peers:     peers,
		Transfers: transfers,
		Channels:  channels,
		Messages:  c.history.snapshot(),
	}
}

// PickDownloadDir 设置默认下载目录。
func (c *Client) PickDownloadDir(path string) error {
	if path == "" {
		return errEmptyPath
	}
	c.mu.Lock()
	c.downloadDir = path
	disconnected := c.connState == appapi.ConnDisconnected
	curID, curPriv, curPub := c.deviceID, c.identityPriv, c.identityPub
	c.mu.Unlock()

	if disconnected {
		id, priv, pub := loadOrCreateIdentity(path)
		c.mu.Lock()
		c.deviceID = id
		c.identityPriv = priv
		c.identityPub = pub
		c.mu.Unlock()
	} else if _, err := os.Stat(filepath.Join(path, identityFileName)); os.IsNotExist(err) {
		saveIdentity(path, curID, curPriv, curPub)
	}
	c.history.setDir(path)
	return nil
}

// SetNickname 修改本机昵称并同步给服务器（已连接时）。
func (c *Client) SetNickname(name string) error {
	if name == "" {
		return errEmptyNickname
	}
	c.mu.Lock()
	c.nickname = name
	conn := c.conn
	connected := c.connState == appapi.ConnConnected
	c.mu.Unlock()

	if connected {
		return c.sendPresenceUpdate(conn)
	}
	return nil
}
