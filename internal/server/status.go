package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// StatusSnapshot 是中心信令节点的实时运行状态快照（GET /api/status）。
type StatusSnapshot struct {
	ProtocolVersion string             `json:"protocolVersion"`
	AuthMode        string             `json:"authMode"`
	RelayEnabled    bool               `json:"relayEnabled"`
	UptimeSec       int64              `json:"uptimeSec"`
	Revision        uint64             `json:"revision"`
	OnlineCount     int                `json:"onlineCount"`
	Users           []protocol.User    `json:"users"`
	ChannelCount    int                `json:"channelCount"`
	Channels        []protocol.Channel `json:"channels"`
	ActiveSwarms    int                `json:"activeSwarms"`
	MessagesRouted  uint64             `json:"messagesRouted"`
	OfflineQueued   uint64             `json:"offlineQueued"`
	RelaySessions   uint64             `json:"relaySessions"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

func (s *Server) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	snap := s.hub.statusSnapshot(ctx, s.startedAt)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}

func (s *Server) handleStatusPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(statusPageHTML))
}

// handleMetrics 输出标准 Prometheus 文本格式指标（GET /metrics，M25）。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	snap := s.hub.statusSnapshot(ctx, s.startedAt)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w,
		"# HELP lanchat_online_users Current number of connected signaling clients.\n"+
			"# TYPE lanchat_online_users gauge\n"+
			"lanchat_online_users %d\n"+
			"# HELP lanchat_active_channels Current number of custom G2 channels.\n"+
			"# TYPE lanchat_active_channels gauge\n"+
			"lanchat_active_channels %d\n"+
			"# HELP lanchat_active_swarms Current number of active group file swarm sessions.\n"+
			"# TYPE lanchat_active_swarms gauge\n"+
			"lanchat_active_swarms %d\n"+
			"# HELP lanchat_messages_routed_total Total number of chat messages routed by hub.\n"+
			"# TYPE lanchat_messages_routed_total counter\n"+
			"lanchat_messages_routed_total %d\n"+
			"# HELP lanchat_offline_queued_total Total number of messages queued in offline spool.\n"+
			"# TYPE lanchat_offline_queued_total counter\n"+
			"lanchat_offline_queued_total %d\n"+
			"# HELP lanchat_relay_sessions_total Total number of relay grants issued.\n"+
			"# TYPE lanchat_relay_sessions_total counter\n"+
			"lanchat_relay_sessions_total %d\n"+
			"# HELP lanchat_uptime_seconds Server uptime in seconds.\n"+
			"# TYPE lanchat_uptime_seconds gauge\n"+
			"lanchat_uptime_seconds %d\n",
		snap.OnlineCount,
		snap.ChannelCount,
		snap.ActiveSwarms,
		snap.MessagesRouted,
		snap.OfflineQueued,
		snap.RelaySessions,
		snap.UptimeSec,
	)
}

const statusPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LANChat 中心节点监控面板</title>
<style>
  :root {
    --bg: #0f172a; --card: #1e293b; --text: #f8fafc; --muted: #94a3b8;
    --accent: #38bdf8; --ok: #4ade80; --border: #334155;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 24px; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    background: var(--bg); color: var(--text);
  }
  .container { max-width: 1080px; margin: 0 auto; }
  header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
  h1 { font-size: 22px; margin: 0; }
  .badge { padding: 4px 10px; border-radius: 999px; font-size: 12px; background: rgba(74,222,128,0.15); color: var(--ok); }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin-bottom: 24px; }
  .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 16px; }
  .card .label { color: var(--muted); font-size: 13px; margin-bottom: 6px; }
  .card .value { font-size: 26px; font-weight: 600; color: var(--accent); }
  h2 { font-size: 16px; margin: 20px 0 10px; color: var(--muted); }
  table { width: 100%; border-collapse: collapse; background: var(--card); border-radius: 12px; overflow: hidden; border: 1px solid var(--border); }
  th, td { text-align: left; padding: 10px 14px; border-bottom: 1px solid var(--border); font-size: 14px; }
  th { color: var(--muted); font-weight: 500; }
</style>
</head>
<body>
<div class="container">
  <header>
    <h1>LANChat 中心信令节点监控</h1>
    <span class="badge" id="health">● 运行中</span>
  </header>
  <div class="grid">
    <div class="card"><div class="label">在线客户端</div><div class="value" id="online">-</div></div>
    <div class="card"><div class="label">活跃频道</div><div class="value" id="channels">-</div></div>
    <div class="card"><div class="label">群文件 Swarm 会话</div><div class="value" id="swarms">-</div></div>
    <div class="card"><div class="label">鉴权 / 中继</div><div class="value" id="mode">-</div></div>
    <div class="card"><div class="label">运行时长</div><div class="value" id="uptime">-</div></div>
  </div>
  <h2>在线成员</h2>
  <table>
    <thead><tr><th>节点 ID</th><th>昵称</th><th>平台 (OS)</th><th>状态</th></tr></thead>
    <tbody id="users-body"><tr><td colspan="4">加载中...</td></tr></tbody>
  </table>
  <h2>自定义频道 (G2)</h2>
  <table>
    <thead><tr><th>频道 ID</th><th>名称</th><th>主题</th><th>属性</th><th>成员数</th></tr></thead>
    <tbody id="channels-body"><tr><td colspan="5">加载中...</td></tr></tbody>
  </table>
</div>
<script>
function fmtUptime(s) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return h > 0 ? h + "h " + m + "m" : (m > 0 ? m + "m " + sec + "s" : sec + "s");
}
async function refresh() {
  try {
    const res = await fetch('/api/status');
    const d = await res.json();
    document.getElementById('online').textContent = d.onlineCount;
    document.getElementById('channels').textContent = d.channelCount;
    document.getElementById('swarms').textContent = d.activeSwarms;
    document.getElementById('mode').textContent = d.authMode.toUpperCase() + (d.relayEnabled ? ' + Relay' : '');
    document.getElementById('uptime').textContent = fmtUptime(d.uptimeSec);
    const ub = document.getElementById('users-body');
    ub.innerHTML = (d.users && d.users.length)
      ? d.users.map(u => '<tr><td><code>' + u.id + '</code></td><td>' + u.nickname + '</td><td>' + (u.os || '-') + '</td><td>' + (u.status || 'online') + '</td></tr>').join('')
      : '<tr><td colspan="4">暂无在线成员</td></tr>';
    const cb = document.getElementById('channels-body');
    cb.innerHTML = (d.channels && d.channels.length)
      ? d.channels.map(c => '<tr><td><code>' + c.id + '</code></td><td>' + c.name + '</td><td>' + (c.topic || '-') + '</td><td>' + (c.private ? '🔒 私有' : '公开') + '</td><td>' + (c.members ? c.members.length : 0) + '</td></tr>').join('')
      : '<tr><td colspan="5">暂无活跃频道</td></tr>';
  } catch (e) {
    document.getElementById('health').textContent = '● 连接断开';
  }
}
refresh();
setInterval(refresh, 3000);
</script>
</body>
</html>
`
