//! LANChat Linux 原生 UI 入口（GTK4 + libadwaita）。
//!
//! 主线程跑 GTK 主循环；另建单例 tokio 多线程 runtime 处理 Unix socket，
//! 后台事件经 async-channel marshal 回主线程（见 `app`）。

// DTO 字段刻意与 ipc.schema.json 全量对齐，即使本版本 UI 暂未读取也保留。
#![allow(dead_code)]

mod app;
mod daemon;
mod ipc;
mod models;
mod state;

use once_cell::sync::OnceCell;
use tokio::runtime::Runtime;

use adw::gtk::gio;
use adw::prelude::*;

static RUNTIME: OnceCell<Runtime> = OnceCell::new();

/// 在共享 tokio runtime 上执行后台任务（非阻塞）。
pub fn spawn_rt<F>(future: F)
where
    F: std::future::Future<Output = ()> + Send + 'static,
{
    runtime().spawn(future);
}

fn runtime() -> &'static Runtime {
    RUNTIME.get_or_init(|| Runtime::new().expect("无法创建 tokio runtime"))
}

fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "info,lanchat_ui=debug".into()),
        )
        .init();

    let application = adw::Application::new(
        Some("dev.lanchat.LANChat"),
        gio::ApplicationFlags::default(),
    );
    application.connect_activate(app::on_activate);
    application.run();
}
