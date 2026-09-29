//! daemon 进程管理：UI 启动时若 socket 不存在则拉起 `lanchat-daemon`。

use std::path::{Path, PathBuf};
use tokio::process::Command;
use tracing::{info, warn};

/// 查找 daemon 可执行文件：优先 PATH，其次 UI 同目录（打包安装布局）。
fn find_daemon() -> PathBuf {
    if which("lanchat-daemon").is_some() {
        return PathBuf::from("lanchat-daemon");
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let sibling = dir.join("lanchat-daemon");
            if sibling.exists() {
                return sibling;
            }
        }
    }
    PathBuf::from("lanchat-daemon")
}

fn which(name: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&path) {
        let candidate = dir.join(name);
        if candidate.is_file() {
            return Some(candidate);
        }
    }
    None
}

/// 若 socket 尚不存在则拉起 daemon；返回是否执行了拉起。
pub async fn ensure_daemon(socket: &Path, download_dir: Option<&Path>) -> bool {
    if socket.exists() {
        return false;
    }
    let bin = find_daemon();
    let mut cmd = Command::new(&bin);
    cmd.arg("--socket").arg(socket);
    if let Some(dir) = download_dir {
        cmd.arg("--download-dir").arg(dir);
    }
    match cmd.spawn() {
        Ok(child) => {
            info!("已拉起 daemon (pid {})", child.id().unwrap_or(0));
            // 等待 socket 出现（最多约 3s）。
            for _ in 0..30 {
                tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                if socket.exists() {
                    return true;
                }
            }
            warn!("daemon 已启动但 socket 未出现");
            true
        }
        Err(e) => {
            warn!("无法拉起 daemon {bin:?}: {e}");
            false
        }
    }
}
