//! daemon 进程管理：UI 启动时若 socket 不可连或为升级后残留的已删除旧进程，则拉起新版 `lanchat-daemon`。

use std::path::{Path, PathBuf};
use tokio::net::UnixStream;
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

/// 清理升级安装包后仍驻留在内存中的旧版 `lanchat-daemon (deleted)` 进程。
fn reap_deleted_daemons() -> bool {
    let Ok(entries) = std::fs::read_dir("/proc") else {
        return false;
    };
    let mut reaped = false;
    for entry in entries.flatten() {
        let name = entry.file_name();
        let Some(pid_str) = name.to_str() else {
            continue;
        };
        if !pid_str.chars().all(|c| c.is_ascii_digit()) {
            continue;
        }
        let comm_path = entry.path().join("comm");
        let Ok(comm) = std::fs::read_to_string(&comm_path) else {
            continue;
        };
        if comm.trim() != "lanchat-daemon" {
            continue;
        }
        let exe_path = entry.path().join("exe");
        if let Ok(target) = std::fs::read_link(&exe_path) {
            if target.to_string_lossy().ends_with(" (deleted)") {
                info!("检测到升级前残留的旧版 lanchat-daemon (pid {pid_str})，正在终止…");
                let _ = std::process::Command::new("kill")
                    .args(["-9", pid_str])
                    .status();
                reaped = true;
            }
        }
    }
    reaped
}

/// 若 socket 尚不可连或旧版 daemon 已清理，则拉起新版 daemon；返回是否执行了拉起。
pub async fn ensure_daemon(socket: &Path, download_dir: Option<&Path>) -> bool {
    let reaped = reap_deleted_daemons();
    if !reaped && UnixStream::connect(socket).await.is_ok() {
        return false;
    }
    if socket.exists() {
        let _ = std::fs::remove_file(socket);
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
            // 等待 socket 可连接（最多约 3s）。
            for _ in 0..30 {
                tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                if UnixStream::connect(socket).await.is_ok() {
                    return true;
                }
            }
            warn!("daemon 已启动但 socket 尚未就绪");
            true
        }
        Err(e) => {
            warn!("无法拉起 daemon {bin:?}: {e}");
            false
        }
    }
}
