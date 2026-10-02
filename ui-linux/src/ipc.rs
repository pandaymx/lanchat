//! JSON-RPC 2.0 类型与 Unix socket IPC 客户端。
//!
//! 契约：daemon 在 Unix socket 上以「一行一帧 UTF-8」收发 JSON-RPC 2.0。
//! UI → daemon：全部为带 id 的 request；daemon → UI：可能是 response，
//! 也可能是无 id 的 notification。严格对齐 `api/ipc.schema.json`。

use anyhow::{anyhow, Context, Result};
use serde::Serialize;
use serde_json::Value;
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::sync::{mpsc, oneshot, Notify};
use tokio::time::{sleep, timeout, Duration};

/// daemon 默认 Unix socket 路径，与 `cmd/lanchat-daemon` 约定一致。
pub fn default_socket_path() -> PathBuf {
    if let Ok(dir) = std::env::var("XDG_RUNTIME_DIR") {
        if !dir.is_empty() {
            return PathBuf::from(dir).join("lanchat.sock");
        }
    }
    PathBuf::from("/tmp/lanchat.sock")
}

/// 下行通知（method + params）。IPC 层不解析业务字段，交由上层处理。
#[derive(Debug, Clone)]
pub struct Notification {
    pub method: String,
    pub params: Value,
}

#[derive(Serialize)]
struct RpcRequest<'a, P: Serialize> {
    jsonrpc: &'a str,
    id: i64,
    method: &'a str,
    params: Option<&'a P>,
}

/// 发给后台 actor 的命令。
enum Cmd {
    Invoke {
        line: String,
        waiter: oneshot::Sender<Result<Value>>,
    },
    Shutdown,
}

/// 读循环上报的消息。
enum Incoming {
    Frame(String),
    Closed,
}

/// IPC 客户端句柄（可 Clone）。
#[derive(Clone)]
pub struct IpcClient {
    next_id: Arc<AtomicI64>,
    tx: mpsc::UnboundedSender<Cmd>,
}

impl IpcClient {
    /// 启动后台连接循环。回调在读线程上下文执行，实现方需自行 marshal 回 GTK 主线程。
    pub fn start<N, R>(
        socket_path: PathBuf,
        on_notification: N,
        on_reconnect: R,
        stop: Arc<Notify>,
    ) -> Self
    where
        N: Fn(Notification) + Send + 'static,
        R: Fn() + Send + 'static,
    {
        let (tx, rx) = mpsc::unbounded_channel();
        let next_id = Arc::new(AtomicI64::new(0));
        let actor = Actor {
            socket_path,
            rx,
            pending: HashMap::new(),
            stop,
            on_notification,
            on_reconnect,
        };
        tokio::spawn(actor.run());
        Self { next_id, tx }
    }

    /// 发起请求并等待结果（15s 超时）。
    pub async fn invoke<P: Serialize>(&self, method: &str, params: Option<&P>) -> Result<Value> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed) + 1;
        let line = serde_json::to_string(&RpcRequest {
            jsonrpc: "2.0",
            id,
            method,
            params,
        })?;
        let (waiter, rx) = oneshot::channel();
        self.tx
            .send(Cmd::Invoke { line, waiter })
            .map_err(|_| anyhow!("IPC 后台已停止"))?;

        match timeout(Duration::from_secs(15), rx).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) => Err(anyhow!("IPC 后台已停止")),
            Err(_) => Err(anyhow!("请求 {method} 超时")),
        }
    }

    /// 通知后台退出（关闭连接）。
    pub fn shutdown(&self) {
        let _ = self.tx.send(Cmd::Shutdown);
    }

    /// ChannelCreate 创建 G2 自定义频道，返回 channelID。
    /// `topic` 为频道主题（可空）；`private` 为真时仅可经 ChannelInvite 加入。
    pub async fn channel_create(&self, name: &str, topic: &str, private: bool) -> Result<String> {
        let params = serde_json::json!({
            "name": name,
            "topic": topic,
            "private": private,
        });
        let v = self.invoke("ChannelCreate", Some(&params)).await?;
        v.get("channelID")
            .and_then(|x| x.as_str())
            .map(|s| s.to_string())
            .ok_or_else(|| anyhow!("响应缺少 channelID"))
    }

    /// ChannelJoin 加入指定频道（private 频道会被后端拒绝）。
    pub async fn channel_join(&self, channel_id: &str) -> Result<()> {
        let params = serde_json::json!({ "channelID": channel_id });
        self.invoke("ChannelJoin", Some(&params)).await.map(|_| ())
    }

    /// ChannelInvite 邀请在线成员加入频道（仅 owner，后端校验）。
    pub async fn channel_invite(&self, channel_id: &str, member_id: &str) -> Result<()> {
        let params = serde_json::json!({
            "channelID": channel_id,
            "memberID": member_id,
        });
        self.invoke("ChannelInvite", Some(&params))
            .await
            .map(|_| ())
    }

    /// ChannelLeave 退出指定频道。
    pub async fn channel_leave(&self, channel_id: &str) -> Result<()> {
        let params = serde_json::json!({ "channelID": channel_id });
        self.invoke("ChannelLeave", Some(&params)).await.map(|_| ())
    }

    /// ChannelList 列出当前可见频道。
    pub async fn channel_list(&self) -> Result<Vec<crate::models::Channel>> {
        let v = self.invoke::<()>("ChannelList", None).await?;
        serde_json::from_value(v).map_err(Into::into)
    }

    /// OfferFileToGroup 向频道（group=channelID）发起文件发送，返回 transferID。
    pub async fn offer_file_to_group(&self, group: &str, path: &str) -> Result<String> {
        let params = serde_json::json!({ "group": group, "path": path });
        let v = self.invoke("OfferFileToGroup", Some(&params)).await?;
        v.get("transferID")
            .and_then(|x| x.as_str())
            .map(|s| s.to_string())
            .ok_or_else(|| anyhow!("响应缺少 transferID"))
    }
}

struct Actor<N, R> {
    socket_path: PathBuf,
    rx: mpsc::UnboundedReceiver<Cmd>,
    pending: HashMap<i64, oneshot::Sender<Result<Value>>>,
    stop: Arc<Notify>,
    on_notification: N,
    on_reconnect: R,
}

impl<N, R> Actor<N, R>
where
    N: Fn(Notification) + Send + 'static,
    R: Fn() + Send + 'static,
{
    async fn run(mut self) {
        let mut backoff_ms = 200u64;
        loop {
            tokio::select! {
                _ = self.stop.notified() => {
                    self.fail_all("正在退出");
                    return;
                }
                res = UnixStream::connect(&self.socket_path) => {
                    match res {
                        Ok(stream) => {
                            backoff_ms = 200;
                            let reconnect = self.serve_connection(stream).await;
                            // serve_connection 因读/写错误结束 → 失败化残留请求并重连。
                            self.fail_all("连接已断开");
                            if reconnect == Flow::Stop {
                                return;
                            }
                        }
                        Err(_) => {
                            sleep(Duration::from_millis(backoff_ms)).await;
                            backoff_ms = (backoff_ms * 2).min(5000);
                        }
                    }
                }
            }
        }
    }

    /// 在一条已建立的连接上服务，返回是否应停止整个 actor。
    async fn serve_connection(&mut self, stream: UnixStream) -> Flow {
        let (read_half, mut write_half) = stream.into_split();
        let (in_tx, in_rx) = mpsc::channel::<Incoming>(64);

        // 读循环任务：逐行读取并上报。
        tokio::spawn(async move {
            let mut reader = BufReader::new(read_half);
            let mut line = String::new();
            loop {
                line.clear();
                match reader.read_line(&mut line).await {
                    Ok(0) => {
                        let _ = in_tx.send(Incoming::Closed).await;
                        return;
                    }
                    Ok(_) => {
                        if in_tx.send(Incoming::Frame(line.clone())).await.is_err() {
                            return;
                        }
                    }
                    Err(_) => {
                        let _ = in_tx.send(Incoming::Closed).await;
                        return;
                    }
                }
            }
        });

        let mut in_rx = in_rx;
        let mut connected_once = false;

        loop {
            tokio::select! {
                _ = self.stop.notified() => {
                    self.fail_all("正在退出");
                    return Flow::Stop;
                }
                cmd = self.rx.recv() => {
                    match cmd {
                        Some(Cmd::Shutdown) | None => {
                            self.fail_all("正在退出");
                            return Flow::Stop;
                        }
                        Some(Cmd::Invoke { line, waiter }) => {
                            // 从请求行取回 id 用于登记。
                            let id = extract_id(&line);
                            if write_half.write_all(line.as_bytes()).await.is_err()
                                || write_half.write_u8(b'\n').await.is_err()
                            {
                                waiter.send(Err(anyhow!("写入失败"))).ok();
                                return Flow::Reconnect;
                            }
                            if let Some(id) = id {
                                self.pending.insert(id, waiter);
                            }
                        }
                    }
                }
                incoming = in_rx.recv() => {
                    match incoming {
                        Some(Incoming::Frame(line)) => {
                            self.handle_frame(line.trim());
                            if !connected_once {
                                connected_once = true;
                                (self.on_reconnect)();
                            }
                        }
                        Some(Incoming::Closed) | None => return Flow::Reconnect,
                    }
                }
            }
        }
    }

    fn handle_frame(&mut self, line: &str) {
        match parse_frame(line) {
            Ok(Frame::Response(v)) => {
                let Some(id) = v.get("id").and_then(|i| i.as_i64()) else {
                    return;
                };
                let Some(waiter) = self.pending.remove(&id) else {
                    return;
                };
                if let Some(err) = v.get("error") {
                    let msg = err
                        .get("message")
                        .and_then(|m| m.as_str())
                        .unwrap_or("daemon 返回错误");
                    waiter.send(Err(anyhow!("{msg}"))).ok();
                } else {
                    waiter
                        .send(Ok(v.get("result").cloned().unwrap_or(Value::Null)))
                        .ok();
                }
            }
            Ok(Frame::Notification(n)) => (self.on_notification)(n),
            Err(e) => tracing::warn!("丢弃坏帧：{e:#}"),
        }
    }

    fn fail_all(&mut self, msg: &str) {
        for (_, waiter) in self.pending.drain() {
            waiter.send(Err(anyhow!("{msg}"))).ok();
        }
    }
}

#[derive(PartialEq, Eq)]
enum Flow {
    Reconnect,
    Stop,
}

fn extract_id(line: &str) -> Option<i64> {
    serde_json::from_str::<Value>(line)
        .ok()?
        .get("id")?
        .as_i64()
}

/// 把一行 JSON 判定为 response 或 notification。
fn parse_frame(line: &str) -> Result<Frame> {
    let v: Value = serde_json::from_str(line).context("帧不是合法 JSON")?;
    let obj = v.as_object().ok_or_else(|| anyhow!("帧不是 JSON 对象"))?;
    if obj.contains_key("id") {
        Ok(Frame::Response(v))
    } else if let Some(method) = obj.get("method").and_then(|m| m.as_str()) {
        Ok(Frame::Notification(Notification {
            method: method.to_string(),
            params: obj.get("params").cloned().unwrap_or(Value::Null),
        }))
    } else {
        Err(anyhow!("无法识别的帧"))
    }
}

enum Frame {
    Response(Value),
    Notification(Notification),
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_response_frame() {
        let line = r#"{"jsonrpc":"2.0","id":1,"result":{"conn":"disconnected"}}"#;
        match parse_frame(line).unwrap() {
            Frame::Response(v) => {
                assert_eq!(v["id"], 1);
                assert_eq!(v["result"]["conn"], "disconnected");
            }
            _ => panic!("应为 response"),
        }
    }

    #[test]
    fn parse_notification_frame() {
        let line = r#"{"jsonrpc":"2.0","method":"peer.left","params":{"peerId":"p1"}}"#;
        match parse_frame(line).unwrap() {
            Frame::Notification(n) => {
                assert_eq!(n.method, "peer.left");
                assert_eq!(n.params["peerId"], "p1");
            }
            _ => panic!("应为 notification"),
        }
    }

    #[test]
    fn reject_garbage() {
        assert!(parse_frame("not json").is_err());
        assert!(parse_frame(r#"{"jsonrpc":"2.0"}"#).is_err());
    }

    /// 端到端：用真实 Unix socket pair 验证请求/响应与通知。
    #[tokio::test]
    async fn request_and_notification_roundtrip() {
        let dir = std::env::temp_dir().join(format!("lanchat-test-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let sock = dir.join("ipc.sock");
        let listener = tokio::net::UnixListener::bind(&sock).unwrap();

        // 假 daemon：回显 GetState 结果，并推送一条通知。
        tokio::spawn(async move {
            use tokio::io::AsyncWriteExt;
            let (mut conn, _) = listener.accept().await.unwrap();
            let mut reader = BufReader::new(&mut conn);
            let mut line = String::new();
            reader.read_line(&mut line).await.unwrap();
            conn.write_all(
                br#"{"jsonrpc":"2.0","id":1,"result":{"conn":"disconnected","nickname":"t","peers":[],"transfers":[],"channels":[]}}
{"jsonrpc":"2.0","method":"peer.joined","params":{}}
"#,
            )
            .await
            .unwrap();
            sleep(Duration::from_secs(1)).await;
        });

        let (notify_tx, mut notify_rx) = mpsc::channel::<Notification>(4);
        let stop = Arc::new(Notify::new());
        let client = IpcClient::start(
            sock.clone(),
            move |n| {
                let _ = notify_tx.try_send(n);
            },
            || {},
            stop.clone(),
        );

        let result = client.invoke::<()>("GetState", None).await.unwrap();
        assert_eq!(result["conn"], "disconnected");

        let n = timeout(Duration::from_secs(2), notify_rx.recv())
            .await
            .unwrap()
            .unwrap();
        assert_eq!(n.method, "peer.joined");

        stop.notify_waiters();
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// 频道相关方法的请求/响应往返。
    #[tokio::test]
    async fn channel_methods_roundtrip() {
        let dir = std::env::temp_dir().join(format!("lanchat-test-chan-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let sock = dir.join("chan.sock");
        let listener = tokio::net::UnixListener::bind(&sock).unwrap();

        // 假 daemon：按 method 回契约规定的 result；同时记录创建参数。
        let captured = Arc::new(std::sync::Mutex::new(None::<Value>));
        let cap2 = captured.clone();
        tokio::spawn(async move {
            use tokio::io::AsyncWriteExt;
            let (conn, _) = listener.accept().await.unwrap();
            let (read_half, mut write_half) = conn.into_split();
            let mut reader = BufReader::new(read_half);
            let mut line = String::new();
            loop {
                line.clear();
                if reader.read_line(&mut line).await.unwrap() == 0 {
                    return;
                }
                let frame: Value = serde_json::from_str(line.trim()).unwrap();
                let id = frame["id"].clone();
                let method = frame["method"].as_str().unwrap();
                if method == "ChannelCreate" {
                    *cap2.lock().unwrap() = frame.get("params").cloned();
                }
                let result = match method {
                    "ChannelCreate" => serde_json::json!({"channelID": "c-9"}),
                    "ChannelJoin" => serde_json::json!({}),
                    "ChannelInvite" => serde_json::json!({}),
                    "ChannelLeave" => serde_json::json!({}),
                    "ChannelList" => serde_json::json!([
                        {"id": "c-9", "name": "运维", "ownerId": "p1", "members": ["p1"]}
                    ]),
                    "OfferFileToGroup" => serde_json::json!({"transferID": "t-9"}),
                    m => panic!("意外方法 {m}"),
                };
                let resp = serde_json::json!({"jsonrpc":"2.0","id":id,"result":result});
                write_half
                    .write_all(resp.to_string().as_bytes())
                    .await
                    .unwrap();
                write_half.write_u8(b'\n').await.unwrap();
            }
        });

        let stop = Arc::new(Notify::new());
        let client = IpcClient::start(sock.clone(), |_| {}, || {}, stop.clone());

        assert_eq!(
            client
                .channel_create("运维", "值班频道", true)
                .await
                .unwrap(),
            "c-9"
        );
        // 创建参数序列化对齐 schema：name / topic / private。
        let params = captured.lock().unwrap().clone().unwrap();
        assert_eq!(params["name"], "运维");
        assert_eq!(params["topic"], "值班频道");
        assert_eq!(params["private"], true);

        client.channel_join("c-9").await.unwrap();
        client.channel_invite("c-9", "p7").await.unwrap();
        client.channel_leave("c-9").await.unwrap();
        let list = client.channel_list().await.unwrap();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].name, "运维");
        assert_eq!(
            client.offer_file_to_group("c-9", "/tmp/a").await.unwrap(),
            "t-9"
        );

        stop.notify_waiters();
        let _ = std::fs::remove_dir_all(&dir);
    }
}
