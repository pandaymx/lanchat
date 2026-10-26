package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/core"
)

type cliOptions struct {
	addr        string
	psk         string
	nick        string
	downloadDir string
}

type cliListener struct {
	mu  sync.Mutex
	out io.Writer
}

func (l *cliListener) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.out, format+"\n", args...)
}

func (l *cliListener) OnConnChanged(state appapi.ConnState, reason string) {
	if reason != "" {
		l.printf("[连接状态] %s (%s)", state, reason)
		return
	}
	l.printf("[连接状态] %s", state)
}

func (l *cliListener) OnPeerJoined(peer appapi.Peer) {
	l.printf("[上线] %s (%s, %s)", peer.Nickname, peer.ID, peer.OS)
}

func (l *cliListener) OnPeerLeft(peerID string) {
	l.printf("[离线] %s", peerID)
}

func (l *cliListener) OnMessageReceived(from, group, msgID, msgType, text string) {
	scope := "1:1"
	if group == "*" {
		scope = "全体"
	} else if group != "" {
		scope = "频道:" + group
	}
	l.printf("[%s] %s (%s): %s [id=%s]", scope, from, msgType, text, msgID)
}

func (l *cliListener) OnTransferProgress(tr appapi.Transfer) {
	if tr.Direction == appapi.TransferInbound && tr.State == appapi.TransferPending {
		l.printf("[文件邀请] 来自 %s: %s (%d B) -> 输入 `/accept %s` 接收", tr.PeerID, tr.Name, tr.Size, tr.ID)
	}
}

func (l *cliListener) OnTransferDone(transferID string) {
	l.printf("[传输完成] %s", transferID)
}

func (l *cliListener) OnTransferFailed(transferID, reason string) {
	l.printf("[传输失败] %s: %s", transferID, reason)
}

func (l *cliListener) OnGroupMatrix(channelID, transferID string, _ []byte) {
	l.printf("[群块矩阵更新] channel=%s transfer=%s", channelID, transferID)
}

func (l *cliListener) OnChannelUpdated(channels []appapi.Channel) {
	l.printf("[频道列表更新] 共 %d 个频道", len(channels))
}

func runCLI(args []string) error {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	var opts cliOptions
	fs.StringVar(&opts.addr, "addr", "auto", "信令服务器地址（auto/local 或 ws://host:port/lctp）")
	fs.StringVar(&opts.psk, "psk", "", "预共享口令 PSK")
	fs.StringVar(&opts.nick, "nick", "", "本机昵称")
	fs.StringVar(&opts.downloadDir, "dir", ".", "默认下载与身份存储目录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runCLISession(ctx, os.Stdin, os.Stdout, opts)
}

func runCLISession(ctx context.Context, in io.Reader, out io.Writer, opts cliOptions) error {
	listener := &cliListener{out: out}
	client := core.New(core.Options{
		Nickname:    opts.nick,
		OS:          runtime.GOOS,
		DownloadDir: opts.downloadDir,
		Listener:    listener,
	})
	defer client.Close()

	if err := client.Connect(opts.addr, opts.psk); err != nil {
		return err
	}

	target := "*"
	listener.printf("已连接（SelfID=%s, 指纹=%s）。默认发送目标: %s（输入 /help 查看命令）",
		client.GetState().SelfID, client.Fingerprint(), target)

	lineCh := make(chan string)
	go func() {
		defer close(lineCh)
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			select {
			case lineCh <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case raw, ok := <-lineCh:
			if !ok {
				return nil
			}
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			if line == "/quit" || line == "/exit" {
				return nil
			}
			if line == "/help" {
				listener.printf("可用命令: /peers | /channels | /to <peerID|*|chID> | /create <name> | /join <chID> | /send <path> | /accept <transferID> | /status | /fingerprint | /quit")
				continue
			}
			if line == "/peers" {
				st := client.GetState()
				for _, p := range st.Peers {
					listener.printf("  peer: %s (%s, %s)", p.ID, p.Nickname, p.OS)
				}
				continue
			}
			if line == "/channels" {
				st := client.GetState()
				for _, ch := range st.Channels {
					listener.printf("  channel: %s name=%s members=%d", ch.ID, ch.Name, len(ch.Members))
				}
				continue
			}
			if strings.HasPrefix(line, "/to ") {
				next := strings.TrimSpace(strings.TrimPrefix(line, "/to "))
				if next != "" {
					target = next
					listener.printf("[目标切换] 当前会话目标 -> %s", target)
				}
				continue
			}
			if strings.HasPrefix(line, "/create ") {
				name := strings.TrimSpace(strings.TrimPrefix(line, "/create "))
				if _, err := client.ChannelCreate(name, "", false); err != nil {
					listener.printf("[错误] 创建频道失败: %v", err)
				}
				continue
			}
			if strings.HasPrefix(line, "/join ") {
				chID := strings.TrimSpace(strings.TrimPrefix(line, "/join "))
				if err := client.ChannelJoin(chID); err != nil {
					listener.printf("[错误] 加入频道失败: %v", err)
				} else {
					target = chID
				}
				continue
			}
			if strings.HasPrefix(line, "/send ") {
				path := strings.TrimSpace(strings.TrimPrefix(line, "/send "))
				var err error
				if target == "*" || isChannelTarget(client, target) {
					_, err = client.OfferFileToGroup(target, path)
				} else {
					_, err = client.OfferFile(target, path)
				}
				if err != nil {
					listener.printf("[错误] 发送文件失败: %v", err)
				}
				continue
			}
			if strings.HasPrefix(line, "/accept ") {
				tid := strings.TrimSpace(strings.TrimPrefix(line, "/accept "))
				dest := filepath.Join(opts.downloadDir, "received-"+tid)
				for _, tr := range client.GetState().Transfers {
					if tr.ID == tid && tr.Name != "" {
						dest = filepath.Join(opts.downloadDir, filepath.Base(tr.Name))
						break
					}
				}
				if err := client.RespondFile(tid, true, dest); err != nil {
					listener.printf("[错误] 接收文件失败: %v", err)
				}
				continue
			}

			var (
				res string
				err error
			)
			if target == "*" || isChannelTarget(client, target) {
				res, err = client.SendText("", line, target)
			} else {
				res, err = client.SendText(target, line, "")
			}
			if err != nil {
				listener.printf("[错误] 发送失败: %v", err)
			} else if strings.HasPrefix(res, "status:") || strings.HasPrefix(res, "fp:") || strings.HasPrefix(res, "bwlimit:") {
				listener.printf("[结果] %s", res)
			}
		}
	}
}

func isChannelTarget(c *core.Client, target string) bool {
	for _, ch := range c.GetState().Channels {
		if ch.ID == target {
			return true
		}
	}
	return false
}
