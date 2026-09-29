package core

import (
	"context"
	"crypto/ecdh"
	"os"
	"sync"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// taskSignal 是收泵路由给某个传输任务的一条信令。
type taskSignal struct {
	typ string
	env *protocol.Envelope
}

// transferTask 是一个文件传输任务的全部运行时状态。
//
// 快照字段（对齐 appapi.Transfer）由 t.mu 保护；任务 goroutine 串行推进
// 直连 / 反向 / 中继各路径，路径结果经 dataCh 回送，切换路径时用 mode 丢弃过期结果。
type transferTask struct {
	cli *Client

	mu   sync.Mutex
	snap appapi.Transfer
	mode int

	ctx    context.Context
	cancel context.CancelFunc

	ch chan taskSignal

	// dataCh 承载当前数据路径（Serve / DialSend / Relay*）的结束结果。
	dataCh chan dataResult

	// sha256 全文件校验和（hex）。
	sha256 string

	// 出站任务资源。
	file       *os.File
	senderPriv *ecdh.PrivateKey

	// 入站任务从 FILE_OFFER 学到的参数。
	token        string
	candidates   []string
	senderECDH   string
	destPath     string // 用户接受时指定的落盘全路径
	relayRequest string // RELAY_REQUEST 信封 ID（收 ERROR / ACK 时据此匹配）
}

type dataResult struct {
	mode int
	err  error
}

func newTransferTask(cli *Client, id string, dir appapi.TransferDirection, peerID, name string, size int64) (*transferTask, error) {
	ctx, cancel := context.WithCancel(cli.runCtx)
	t := &transferTask{
		cli: cli,
		snap: appapi.Transfer{
			ID:        id,
			Direction: dir,
			State:     appapi.TransferPending,
			Kind:      appapi.PathUnicast,
			PeerID:    peerID,
			Name:      name,
			Size:      size,
		},
		ctx:    ctx,
		cancel: cancel,
		ch:     make(chan taskSignal, 16),
		dataCh: make(chan dataResult, 1),
	}
	return t, nil
}

// snapshot 返回任务快照（c.mu 之外使用，自身加锁）。
func (t *transferTask) snapshot() appapi.Transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snap
}

func (t *transferTask) setState(s appapi.TransferState) {
	t.mu.Lock()
	t.snap.State = s
	t.mu.Unlock()
}

func (t *transferTask) setProgress(bytesDone, speedBps int64) appapi.Transfer {
	t.mu.Lock()
	t.snap.BytesDone = bytesDone
	t.snap.SpeedBps = speedBps
	t.snap.State = appapi.TransferActive
	cp := t.snap
	t.mu.Unlock()
	return cp
}

func (t *transferTask) fail(reason string) {
	t.mu.Lock()
	t.snap.State = appapi.TransferFailed
	t.snap.SpeedBps = 0
	t.snap.ErrorReason = reason
	cp := t.snap
	t.mu.Unlock()
	t.cli.listener().OnTransferFailed(t.snap.ID, reason)
	t.cli.removeTransfer(t.snap.ID)
	_ = cp
}

// done 标记完成：状态机置 done，发最终进度与完成事件；任务保留在快照中直到 Close / Cancel。
func (t *transferTask) done() {
	t.mu.Lock()
	t.snap.State = appapi.TransferDone
	t.snap.BytesDone = t.snap.Size
	t.snap.SpeedBps = 0
	cp := t.snap
	t.mu.Unlock()
	t.cli.listener().OnTransferProgress(cp)
	t.cli.listener().OnTransferDone(t.snap.ID)
}

func (t *transferTask) canceled(reason string) {
	t.mu.Lock()
	already := t.snap.State == appapi.TransferCanceled || t.snap.State == appapi.TransferDone
	t.snap.State = appapi.TransferCanceled
	t.snap.SpeedBps = 0
	id := t.snap.ID
	t.mu.Unlock()
	t.cli.removeTransfer(id)
	if !already {
		t.cli.listener().OnTransferFailed(id, reason)
	}
}

// deliver 把一条信令投递到任务；任务已结束则丢弃。
func (t *transferTask) deliver(s taskSignal) {
	select {
	case t.ch <- s:
	default:
		// 缓冲满说明任务 goroutine 已失活；丢弃避免收泵阻塞。
	}
}
