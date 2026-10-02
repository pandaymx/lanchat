//! GTK4 + libadwaita 主界面与事件处理。
//!
//! 线程模型：GTK 主循环独占主线程并持有 AppState；tokio 后台线程
//! 经 `glib::MainContext::channel` 把 UI 事件 marshal 回主线程。

use crate::daemon::ensure_daemon;
use crate::ipc::{default_socket_path, IpcClient, Notification};
use crate::models::{
    format_size, format_speed, Channel, ChatMessage, ConnState, ServerInfo, State, Transfer,
};
use crate::state::{AppState, ChatTarget};
use adw::prelude::*;
use anyhow::Result;
use gdk::Display;
use glib::{clone, MainContext};
use gtk::gio;
use gtk::glib;
use gtk::{Align, Orientation};
use serde::Serialize;
use std::cell::RefCell;
use std::path::PathBuf;
use std::rc::Rc;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::sync::Notify;

#[derive(Serialize)]
struct ConnectParams {
    addr: String,
    psk: String,
}

#[derive(Serialize)]
struct SendTextParams {
    to: String,
    text: String,
    group: Option<String>,
}

#[derive(Serialize)]
struct OfferParams {
    to: String,
    path: String,
}

#[derive(Serialize)]
struct OfferGroupParams {
    group: String,
    path: String,
}

#[derive(Serialize)]
struct RespondParams {
    #[serde(rename = "transferID")]
    transfer_id: String,
    accept: bool,
    dest: String,
}

#[derive(Serialize)]
struct IdParams {
    #[serde(rename = "transferID")]
    transfer_id: String,
}

#[derive(Serialize)]
struct NickParams {
    name: String,
}

#[derive(Serialize)]
struct DirParams {
    path: String,
}

/// 后台 → 主线程事件。
#[allow(clippy::large_enum_variant)]
enum UiEvent {
    Ready(IpcClient),
    Reconnected,
    State(Result<State>),
    Discovered(Result<Vec<ServerInfo>>),
    Connected(Result<()>),
    /// 文本发送回执：(会话键, group, 文本, 响应 msgID)
    TextSent(String, String, String, Result<String>),
    OfferSent(Result<String>),
    ChannelCreated(Result<String>),
    ChannelJoined(String, Result<()>),
    ChannelInvited(String, Result<()>),
    ChannelLeft(String, Result<()>),
    Responded(Result<()>),
    Canceled(String, Result<()>),
    NickSet(Result<()>),
    DirSet(Result<()>),
    Notif(Notification),
}

struct Widgets {
    window: adw::ApplicationWindow,
    stack: gtk::Stack,
    toast: adw::ToastOverlay,

    // 登录页
    server_list: gtk::ListBox,
    addr_row: adw::EntryRow,
    psk_row: adw::PasswordEntryRow,
    discover_btn: gtk::Button,
    connect_btn: gtk::Button,
    login_status: gtk::Label,
    login_spinner: gtk::Spinner,

    // 主界面
    roster: gtk::ListBox,
    me_label: gtk::Label,
    conn_badge: gtk::Label,
    chat_header: gtk::Label,
    msg_list: gtk::ListBox,
    msg_scroll: gtk::ScrolledWindow,
    input: gtk::TextView,
    send_btn: gtk::Button,
    file_btn: gtk::Button,
    transfers_btn: gtk::Button,
    channels_btn: gtk::Button,
    nick_btn: gtk::Button,
    dirdir_btn: gtk::Button,

    // 传输窗口
    transfers_win: gtk::Window,
    transfers_list: gtk::ListBox,

    // 频道窗口
    channels_win: gtk::Window,
    channels_list: gtk::ListBox,
    new_channel_btn: gtk::Button,
}

pub struct AppCtx {
    widgets: Widgets,
    state: Rc<RefCell<AppState>>,
    ipc: Rc<RefCell<Option<IpcClient>>>,
    ui_tx: async_channel::Sender<UiEvent>,
    socket: PathBuf,
    stop: Arc<Notify>,
}

/// 应用启动入口（Application::activate）。
pub fn on_activate(app: &adw::Application) {
    load_css();

    let (ui_tx, ui_rx) = async_channel::unbounded::<UiEvent>();
    let widgets = build_ui(app);

    let ctx = Rc::new(AppCtx {
        widgets,
        state: Rc::new(RefCell::new(AppState::new())),
        ipc: Rc::new(RefCell::new(None)),
        ui_tx: ui_tx.clone(),
        socket: default_socket_path(),
        stop: Arc::new(Notify::new()),
    });

    wire_actions(&ctx);
    wire_window_lifecycle(&ctx);

    // 在 GTK 主上下文上消费后台事件（async_channel Receiver 是 Stream）。
    let ctx_weak = Rc::downgrade(&ctx);
    MainContext::default().spawn_local(async move {
        while let Ok(event) = ui_rx.recv().await {
            if let Some(ctx) = ctx_weak.upgrade() {
                handle_event(&ctx, event);
            }
        }
    });

    ctx.widgets.window.present();

    // 后台：确保 daemon → 建立 IPC → Ready。
    let socket = ctx.socket.clone();
    let stop = ctx.stop.clone();
    let ready_tx = ui_tx.clone();
    crate::spawn_rt(async move {
        ensure_daemon(&socket, None).await;
        let client = IpcClient::start(
            socket,
            {
                let tx = ready_tx.clone();
                move |n| {
                    let _ = tx.try_send(UiEvent::Notif(n));
                }
            },
            {
                let tx = ready_tx.clone();
                move || {
                    let _ = tx.try_send(UiEvent::Reconnected);
                }
            },
            stop,
        );
        let _ = ready_tx.try_send(UiEvent::Ready(client));
    });
}

// ---------- UI 构建 ----------

fn build_ui(app: &adw::Application) -> Widgets {
    // --- 登录页 ---
    let login_status = gtk::Label::new(Some("正在启动本地服务…"));
    login_status.set_xalign(0.0);
    login_status.add_css_class("dim-label");

    let login_spinner = gtk::Spinner::new();
    login_spinner.start();
    let status_row = gtk::Box::new(Orientation::Horizontal, 6);
    status_row.append(&login_spinner);
    status_row.append(&login_status);

    let server_list = gtk::ListBox::new();
    server_list.set_selection_mode(gtk::SelectionMode::Single);
    server_list.add_css_class("boxed-list");
    let server_scroll = gtk::ScrolledWindow::new();
    server_scroll.set_min_content_height(140);
    server_scroll.set_child(Some(&server_list));

    let discover_btn = gtk::Button::with_label("自动发现");
    discover_btn.add_css_class("pill");
    let discover_wrap = gtk::Box::new(Orientation::Horizontal, 0);
    discover_wrap.set_halign(Align::Center);
    discover_wrap.append(&discover_btn);

    let addr_row = adw::EntryRow::new();
    addr_row.set_title("服务器地址 host:port/path");

    let psk_row = adw::PasswordEntryRow::new();
    psk_row.set_title("口令 PSK");

    let connect_btn = gtk::Button::with_label("连接");
    connect_btn.add_css_class("pill");
    connect_btn.add_css_class("suggested-action");
    let connect_wrap = gtk::Box::new(Orientation::Horizontal, 0);
    connect_wrap.set_halign(Align::Center);
    connect_wrap.append(&connect_btn);

    let login_content = gtk::Box::new(Orientation::Vertical, 12);
    login_content.set_margin_top(24);
    login_content.set_margin_bottom(24);
    login_content.set_margin_start(36);
    login_content.set_margin_end(36);

    let login_title = gtk::Label::new(Some("连接 LANChat 中心节点"));
    login_title.add_css_class("title-3");
    login_content.append(&login_title);
    login_content.append(&discover_wrap);
    login_content.append(&server_scroll);
    login_content.append(&status_row);
    login_content.append(&addr_row);
    login_content.append(&psk_row);
    login_content.append(&connect_wrap);

    let login_clamp = adw::Clamp::new();
    login_clamp.set_maximum_size(520);
    login_clamp.set_child(Some(&login_content));

    // --- 主界面 ---
    let me_label = gtk::Label::new(Some("我"));
    me_label.set_xalign(0.0);
    let conn_badge = gtk::Label::new(Some("未连接"));
    conn_badge.set_xalign(0.0);
    conn_badge.add_css_class("dim-label");

    let roster = gtk::ListBox::new();
    roster.set_selection_mode(gtk::SelectionMode::Single);
    roster.add_css_class("navigation-sidebar");
    let roster_scroll = gtk::ScrolledWindow::new();
    roster_scroll.set_child(Some(&roster));
    roster_scroll.set_min_content_width(200);
    roster_scroll.set_vexpand(true);

    let transfers_btn = gtk::Button::with_label("传输列表");
    transfers_btn.set_halign(Align::Start);
    let channels_btn = gtk::Button::with_label("频道");
    channels_btn.set_halign(Align::Start);
    let nick_btn = gtk::Button::with_label("修改昵称");
    nick_btn.set_halign(Align::Start);
    let dirdir_btn = gtk::Button::with_label("默认下载目录");
    dirdir_btn.set_halign(Align::Start);

    let sidebar = gtk::Box::new(Orientation::Vertical, 8);
    sidebar.set_margin_top(10);
    sidebar.set_margin_bottom(10);
    sidebar.set_margin_start(10);
    sidebar.set_margin_end(6);
    sidebar.append(&me_label);
    sidebar.append(&conn_badge);
    sidebar.append(&transfers_btn);
    sidebar.append(&channels_btn);
    sidebar.append(&nick_btn);
    sidebar.append(&dirdir_btn);
    sidebar.append(&roster_scroll);

    let sep = gtk::Separator::new(Orientation::Vertical);

    // 聊天区
    let chat_header = gtk::Label::new(Some("选择一个联系人开始聊天"));
    chat_header.set_xalign(0.0);
    chat_header.add_css_class("title-4");
    let header_bar = gtk::Box::new(Orientation::Horizontal, 8);
    header_bar.set_margin_top(8);
    header_bar.set_margin_bottom(8);
    header_bar.set_margin_start(12);
    header_bar.set_margin_end(12);
    header_bar.append(&chat_header);

    let msg_list = gtk::ListBox::new();
    msg_list.set_selection_mode(gtk::SelectionMode::None);
    msg_list.add_css_class("messages");
    let msg_scroll = gtk::ScrolledWindow::new();
    msg_scroll.set_child(Some(&msg_list));
    msg_scroll.set_vexpand(true);

    let input = gtk::TextView::new();
    input.set_wrap_mode(gtk::WrapMode::WordChar);
    input.set_size_request(-1, 72);
    let input_scroll = gtk::ScrolledWindow::new();
    input_scroll.set_child(Some(&input));
    input_scroll.add_css_class("card");

    let send_btn = gtk::Button::with_label("发送");
    send_btn.add_css_class("suggested-action");
    let file_btn = gtk::Button::with_label("文件");
    let btn_row = gtk::Box::new(Orientation::Horizontal, 6);
    btn_row.set_halign(Align::End);
    btn_row.append(&file_btn);
    btn_row.append(&send_btn);

    let composer = gtk::Box::new(Orientation::Vertical, 6);
    composer.set_margin_top(6);
    composer.set_margin_bottom(10);
    composer.set_margin_start(12);
    composer.set_margin_end(12);
    composer.append(&input_scroll);
    composer.append(&btn_row);

    let chat_col = gtk::Box::new(Orientation::Vertical, 0);
    chat_col.append(&header_bar);
    chat_col.append(&msg_scroll);
    chat_col.append(&composer);

    let main_split = gtk::Box::new(Orientation::Horizontal, 0);
    main_split.append(&sidebar);
    main_split.append(&sep);
    main_split.append(&chat_col);

    // --- 传输窗口 ---
    let transfers_list = gtk::ListBox::new();
    transfers_list.set_selection_mode(gtk::SelectionMode::None);
    transfers_list.add_css_class("boxed-list");
    let t_scroll = gtk::ScrolledWindow::new();
    t_scroll.set_min_content_width(460);
    t_scroll.set_min_content_height(360);
    t_scroll.set_child(Some(&transfers_list));
    let transfers_win = gtk::Window::builder()
        .title("文件传输")
        .default_width(500)
        .default_height(420)
        .child(&t_scroll)
        .build();
    transfers_win.set_hide_on_close(true);

    // --- 频道窗口 ---
    let new_channel_btn = gtk::Button::with_label("新建频道");
    new_channel_btn.add_css_class("pill");
    new_channel_btn.add_css_class("suggested-action");
    let btn_wrap = gtk::Box::new(Orientation::Horizontal, 0);
    btn_wrap.set_halign(Align::Center);
    btn_wrap.append(&new_channel_btn);

    let channels_list = gtk::ListBox::new();
    channels_list.set_selection_mode(gtk::SelectionMode::None);
    channels_list.add_css_class("boxed-list");
    let c_scroll = gtk::ScrolledWindow::new();
    c_scroll.set_min_content_width(460);
    c_scroll.set_min_content_height(300);
    c_scroll.set_child(Some(&channels_list));

    let channels_content = gtk::Box::new(Orientation::Vertical, 12);
    channels_content.set_margin_top(12);
    channels_content.set_margin_bottom(12);
    channels_content.set_margin_start(12);
    channels_content.set_margin_end(12);
    channels_content.append(&btn_wrap);
    channels_content.append(&c_scroll);

    let channels_win = gtk::Window::builder()
        .title("频道")
        .default_width(500)
        .default_height(420)
        .child(&channels_content)
        .build();
    channels_win.set_hide_on_close(true);

    let stack = gtk::Stack::new();
    stack.add_named(&login_clamp, Some("login"));
    stack.add_named(&main_split, Some("main"));

    let toast = adw::ToastOverlay::new();
    toast.set_child(Some(&stack));

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .default_width(960)
        .default_height(640)
        .content(&toast)
        .build();

    Widgets {
        window,
        stack,
        toast,
        server_list,
        addr_row,
        psk_row,
        discover_btn,
        connect_btn,
        login_status,
        login_spinner,
        roster,
        me_label,
        conn_badge,
        chat_header,
        msg_list,
        msg_scroll,
        input,
        send_btn,
        file_btn,
        transfers_btn,
        channels_btn,
        nick_btn,
        dirdir_btn,
        transfers_win,
        transfers_list,
        channels_win,
        channels_list,
        new_channel_btn,
    }
}

// ---------- 动作绑定 ----------

fn wire_actions(ctx: &Rc<AppCtx>) {
    // 自动发现
    ctx.widgets.discover_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            if let Some(ipc) = cx.ipc.borrow().clone() {
                let tx = cx.ui_tx.clone();
                crate::spawn_rt(async move {
                    let res = ipc.invoke::<()>("BrowseServers", None).await.and_then(|v| {
                        serde_json::from_value::<Vec<ServerInfo>>(v).map_err(Into::into)
                    });
                    let _ = tx.try_send(UiEvent::Discovered(res));
                });
            }
        }
    ));

    // 选中候选服务器 → 填充地址
    ctx.widgets.server_list.connect_row_selected(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, row| {
            if let Some(row) = row {
                let idx = row.index();
                if idx >= 0 {
                    if let Some(srv) = cx.state.borrow().discovered.get(idx as usize) {
                        cx.widgets.addr_row.set_text(&server_addr_with_path(srv));
                    }
                }
            }
        }
    ));

    // 连接
    ctx.widgets.connect_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let addr = cx.widgets.addr_row.text().to_string();
            let psk = cx.widgets.psk_row.text().to_string();
            if addr.trim().is_empty() {
                cx.toast("请填写或选择服务器地址");
                return;
            }
            cx.set_login_busy(true, "正在连接…");
            if let Some(ipc) = cx.ipc.borrow().clone() {
                let params = ConnectParams { addr, psk };
                let tx = cx.ui_tx.clone();
                crate::spawn_rt(async move {
                    let res = ipc.invoke("Connect", Some(&params)).await.map(|_| ());
                    let _ = tx.try_send(UiEvent::Connected(res));
                });
            }
        }
    ));

    // 选择联系人 → 切换会话
    ctx.widgets.roster.connect_row_selected(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, row| {
            if let Some(row) = row {
                let idx = row.index();
                if idx >= 0 {
                    if let Some(peer) = cx.state.borrow().peers.get(idx as usize).cloned() {
                        cx.state.borrow_mut().current_chat = ChatTarget::Peer(peer.id.clone());
                        cx.widgets.chat_header.set_text(&peer.nickname);
                        cx.render_messages();
                    }
                }
            }
        }
    ));

    // 发送文本
    ctx.widgets.send_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            let Some(key) = target.key().map(|s| s.to_string()) else {
                cx.toast("请先选择联系人或频道");
                return;
            };
            let buffer = cx.widgets.input.buffer();
            let text = buffer
                .text(&buffer.start_iter(), &buffer.end_iter(), false)
                .to_string();
            if text.trim().is_empty() {
                return;
            }
            if let Some(ipc) = cx.ipc.borrow().clone() {
                let group = if target.is_channel() {
                    key.clone()
                } else {
                    String::new()
                };
                let params = SendTextParams {
                    to: key.clone(),
                    text: text.clone(),
                    group: Some(group.clone()),
                };
                let tx = cx.ui_tx.clone();
                buffer.set_text("");
                crate::spawn_rt(async move {
                    let res = ipc.invoke("SendText", Some(&params)).await.and_then(|v| {
                        v.get("msgID")
                            .and_then(|m| m.as_str())
                            .map(|s| s.to_string())
                            .ok_or_else(|| anyhow::anyhow!("响应缺少 msgID"))
                    });
                    let _ = tx.try_send(UiEvent::TextSent(key, group, text, res));
                });
            }
        }
    ));

    // 发送文件
    ctx.widgets.file_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            let Some(key) = target.key().map(|s| s.to_string()) else {
                cx.toast("请先选择联系人或频道");
                return;
            };
            let is_channel = target.is_channel();
            let dialog = gtk::FileDialog::new();
            dialog.open(
                Some(&cx.widgets.window),
                None::<&gio::Cancellable>,
                clone!(
                    #[strong]
                    cx,
                    move |res| {
                        if let Ok(file) = res {
                            if let Some(path) = file.path() {
                                let path = path.to_string_lossy().to_string();
                                if let Some(ipc) = cx.ipc.borrow().clone() {
                                    let tx = cx.ui_tx.clone();
                                    if is_channel {
                                        let params = OfferGroupParams {
                                            group: key.clone(),
                                            path,
                                        };
                                        crate::spawn_rt(async move {
                                            let r = ipc
                                                .invoke("OfferFileToGroup", Some(&params))
                                                .await
                                                .and_then(|v| {
                                                    v.get("transferID")
                                                        .and_then(|t| t.as_str())
                                                        .map(|s| s.to_string())
                                                        .ok_or_else(|| {
                                                            anyhow::anyhow!("响应缺少 transferID")
                                                        })
                                                });
                                            let _ = tx.try_send(UiEvent::OfferSent(r));
                                        });
                                    } else {
                                        let params = OfferParams { to: key, path };
                                        crate::spawn_rt(async move {
                                            let r = ipc
                                                .invoke("OfferFile", Some(&params))
                                                .await
                                                .and_then(|v| {
                                                    v.get("transferID")
                                                        .and_then(|t| t.as_str())
                                                        .map(|s| s.to_string())
                                                        .ok_or_else(|| {
                                                            anyhow::anyhow!("响应缺少 transferID")
                                                        })
                                                });
                                            let _ = tx.try_send(UiEvent::OfferSent(r));
                                        });
                                    }
                                }
                            }
                        }
                    }
                ),
            );
        }
    ));

    // 打开传输窗口
    ctx.widgets.transfers_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.render_transfers();
            cx.widgets.transfers_win.present();
        }
    ));

    // 打开频道窗口
    ctx.widgets.channels_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.render_channels();
            cx.widgets.channels_win.present();
        }
    ));

    // 新建频道：名称 + 主题 + 私有开关（对齐 ChannelCreate{name, topic?, private?}）。
    ctx.widgets.new_channel_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let name_row = adw::EntryRow::new();
            name_row.set_title("名称");

            let topic_row = adw::EntryRow::new();
            topic_row.set_title("主题（可选）");

            let private_row = adw::SwitchRow::new();
            private_row.set_title("私有频道");
            private_row.set_subtitle("仅受邀成员可加入");

            let extra = gtk::ListBox::new();
            extra.add_css_class("boxed-list");
            extra.set_selection_mode(gtk::SelectionMode::None);
            extra.append(&name_row);
            extra.append(&topic_row);
            extra.append(&private_row);

            let dialog = adw::AlertDialog::new(Some("新建频道"), None);
            dialog.set_extra_child(Some(&extra));
            dialog.add_responses(&[("cancel", "取消"), ("ok", "创建")]);
            dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
            dialog.set_close_response("cancel");
            dialog.choose(
                &cx.widgets.window,
                None::<&gio::Cancellable>,
                clone!(
                    #[strong]
                    cx,
                    move |response| {
                        if response == "ok" {
                            let name = name_row.text().to_string();
                            if name.trim().is_empty() {
                                cx.toast("请填写频道名称");
                                return;
                            }
                            let topic = topic_row.text().to_string();
                            let private = private_row.is_active();
                            if let Some(ipc) = cx.ipc.borrow().clone() {
                                let tx = cx.ui_tx.clone();
                                crate::spawn_rt(async move {
                                    let r = ipc
                                        .channel_create(name.trim(), topic.trim(), private)
                                        .await;
                                    let _ = tx.try_send(UiEvent::ChannelCreated(r));
                                });
                            }
                        }
                    }
                ),
            );
        }
    ));

    // 修改昵称
    ctx.widgets.nick_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let entry = gtk::Entry::new();
            entry.set_text(&cx.state.borrow().nickname);
            let dialog = adw::AlertDialog::new(Some("修改昵称"), None);
            dialog.set_extra_child(Some(&entry));
            dialog.add_responses(&[("cancel", "取消"), ("ok", "确定")]);
            dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
            dialog.set_close_response("cancel");
            dialog.choose(
                &cx.widgets.window,
                None::<&gio::Cancellable>,
                clone!(
                    #[strong]
                    cx,
                    #[strong]
                    entry,
                    move |response| {
                        if response == "ok" {
                            let name = entry.text().to_string();
                            if let Some(ipc) = cx.ipc.borrow().clone() {
                                let params = NickParams { name };
                                let tx = cx.ui_tx.clone();
                                crate::spawn_rt(async move {
                                    let r =
                                        ipc.invoke("SetNickname", Some(&params)).await.map(|_| ());
                                    let _ = tx.try_send(UiEvent::NickSet(r));
                                });
                            }
                        }
                    }
                ),
            );
        }
    ));

    // 默认下载目录
    ctx.widgets.dirdir_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let dialog = gtk::FileDialog::new();
            dialog.select_folder(
                Some(&cx.widgets.window),
                None::<&gio::Cancellable>,
                clone!(
                    #[strong]
                    cx,
                    move |res| {
                        if let Ok(file) = res {
                            if let Some(path) = file.path() {
                                let path = path.to_string_lossy().to_string();
                                if let Some(ipc) = cx.ipc.borrow().clone() {
                                    let params = DirParams { path };
                                    let tx = cx.ui_tx.clone();
                                    crate::spawn_rt(async move {
                                        let r = ipc
                                            .invoke("PickDownloadDir", Some(&params))
                                            .await
                                            .map(|_| ());
                                        let _ = tx.try_send(UiEvent::DirSet(r));
                                    });
                                }
                            }
                        }
                    }
                ),
            );
        }
    ));
}

fn wire_window_lifecycle(ctx: &Rc<AppCtx>) {
    ctx.widgets.window.connect_close_request(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.stop.notify_waiters();
            if let Some(ipc) = cx.ipc.borrow().clone() {
                ipc.shutdown();
            }
            glib::Propagation::Proceed
        }
    ));
}

// ---------- 事件处理 ----------

fn handle_event(ctx: &Rc<AppCtx>, event: UiEvent) {
    match event {
        UiEvent::Ready(ipc) => {
            *ctx.ipc.borrow_mut() = Some(ipc);
            ctx.set_login_busy(false, "本地服务已就绪，请发现或填写地址后连接");
            ctx.widgets.login_spinner.stop();
            ctx.sync_state();
        }
        UiEvent::Reconnected => ctx.sync_state(),
        UiEvent::State(res) => match res {
            Ok(state) => {
                let was_connected = ctx.state.borrow().conn == ConnState::Connected;
                ctx.state.borrow_mut().apply_full_state(state);
                render_all(ctx, was_connected);
            }
            Err(e) => tracing::warn!("GetState 失败：{e}"),
        },
        UiEvent::Discovered(res) => match res {
            Ok(servers) => {
                let n = servers.len();
                ctx.state.borrow_mut().discovered = servers;
                ctx.render_discovered();
                ctx.toast(&format!("发现 {n} 个服务器"));
            }
            Err(e) => ctx.toast(&format!("发现失败：{e}")),
        },
        UiEvent::Connected(res) => {
            ctx.set_login_busy(false, "");
            if let Err(e) = res {
                ctx.widgets
                    .login_status
                    .set_text(&format!("连接请求失败：{e}"));
            }
            // 最终是否成功以 conn.changed 为准
            ctx.sync_state();
        }
        UiEvent::TextSent(key, group, text, res) => match res {
            Ok(msg_id) => {
                let now = now_secs();
                ctx.state.borrow_mut().push_message(ChatMessage {
                    peer_id: key.clone(),
                    sender_id: String::new(),
                    group,
                    msg_id,
                    text,
                    inbound: false,
                    ts: now,
                });
                if ctx.state.borrow().current_chat.key() == Some(key.as_str()) {
                    ctx.render_messages();
                }
            }
            Err(e) => ctx.toast(&format!("发送失败：{e}")),
        },
        UiEvent::OfferSent(res) => {
            if let Err(e) = res {
                ctx.toast(&format!("文件发起失败：{e}"));
            }
        }
        UiEvent::ChannelCreated(res) => match res {
            Ok(id) => {
                ctx.toast("频道已创建");
                ctx.render_channels();
                open_channel(ctx, &id);
            }
            Err(e) => ctx.toast(&format!("创建频道失败：{e}")),
        },
        UiEvent::ChannelJoined(id, res) => match res {
            Ok(()) => {
                ctx.toast("已加入频道");
                ctx.render_channels();
                open_channel(ctx, &id);
            }
            Err(e) => ctx.toast(&format!("加入频道失败：{e}")),
        },
        UiEvent::ChannelInvited(_id, res) => match res {
            Ok(()) => ctx.toast("已邀请成员"),
            Err(e) => ctx.toast(&format!("邀请失败：{e}")),
        },
        UiEvent::ChannelLeft(id, res) => match res {
            Ok(()) => {
                ctx.toast("已退出频道");
                if ctx.state.borrow().current_chat.key() == Some(id.as_str()) {
                    ctx.state.borrow_mut().current_chat = ChatTarget::None;
                    ctx.widgets.chat_header.set_text("选择一个联系人开始聊天");
                    ctx.render_messages();
                }
                ctx.render_channels();
            }
            Err(e) => ctx.toast(&format!("退出频道失败：{e}")),
        },
        UiEvent::Responded(res) => {
            if let Err(e) = res {
                ctx.toast(&format!("应答失败：{e}"));
            }
        }
        UiEvent::Canceled(id, res) => match res {
            Ok(()) => {
                ctx.state.borrow_mut().remove_transfer(&id);
                ctx.render_transfers();
            }
            Err(e) => ctx.toast(&format!("取消失败：{e}")),
        },
        UiEvent::NickSet(res) => {
            if let Err(e) = res {
                ctx.toast(&format!("昵称设置失败：{e}"));
            } else {
                ctx.toast("昵称已更新");
                ctx.sync_state();
            }
        }
        UiEvent::DirSet(res) => {
            if let Err(e) = res {
                ctx.toast(&format!("目录设置失败：{e}"));
            } else {
                ctx.toast("默认下载目录已更新");
            }
        }
        UiEvent::Notif(n) => handle_notification(ctx, n),
    }
}

fn handle_notification(ctx: &Rc<AppCtx>, n: Notification) {
    let p = &n.params;
    match n.method.as_str() {
        "conn.changed" => {
            let state: ConnState = p
                .get("state")
                .and_then(|v| serde_json::from_value(v.clone()).ok())
                .unwrap_or_default();
            let reason = p
                .get("reason")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            ctx.state.borrow_mut().conn = state.clone();
            ctx.state.borrow_mut().conn_reason = reason.clone();
            ctx.widgets.conn_badge.set_text(state.label());
            match state {
                ConnState::Connected => {
                    ctx.widgets.stack.set_visible_child_name("main");
                    ctx.toast("已连接");
                    ctx.sync_state();
                }
                ConnState::AuthFailed => {
                    ctx.widgets.login_status.set_text("口令错误，请重新输入");
                    ctx.widgets.stack.set_visible_child_name("login");
                    ctx.toast("口令错误");
                }
                ConnState::Disconnected => {
                    ctx.widgets
                        .login_status
                        .set_text(&format!("连接已断开：{reason}"));
                    ctx.widgets.stack.set_visible_child_name("login");
                }
                ConnState::Connecting => {
                    ctx.widgets.login_status.set_text("正在连接…");
                }
            }
        }
        "peer.joined" => {
            if let Some(peer) = p
                .get("peer")
                .and_then(|v| serde_json::from_value(v.clone()).ok())
            {
                let mut st = ctx.state.borrow_mut();
                let peer: crate::models::Peer = peer;
                if !st.peers.iter().any(|x| x.id == peer.id) {
                    st.peers.push(peer);
                }
                drop(st);
                ctx.render_roster();
            }
        }
        "peer.left" => {
            if let Some(id) = p.get("peerId").and_then(|v| v.as_str()) {
                let id = id.to_string();
                ctx.state.borrow_mut().peers.retain(|x| x.id != id);
                if ctx.state.borrow().current_chat.key() == Some(id.as_str()) {
                    ctx.state.borrow_mut().current_chat = ChatTarget::None;
                    ctx.widgets.chat_header.set_text("选择一个联系人开始聊天");
                    ctx.render_messages();
                }
                ctx.render_roster();
            }
        }
        "msg.received" => {
            let from = p
                .get("from")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let group = p
                .get("group")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let msg_id = p
                .get("msgId")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let text = p
                .get("text")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            // 频道消息按 group（channelID）归类，sender 记录在 sender_id；
            // 单播按 from 归类。
            let (conv_key, sender_id) = if group.is_empty() {
                (from.clone(), String::new())
            } else {
                (group.clone(), from.clone())
            };
            ctx.state.borrow_mut().push_message(ChatMessage {
                peer_id: conv_key.clone(),
                sender_id,
                group,
                msg_id,
                text,
                inbound: true,
                ts: now_secs(),
            });
            if ctx.state.borrow().current_chat.key() == Some(conv_key.as_str()) {
                ctx.render_messages();
            } else {
                ctx.toast("收到新消息");
            }
        }
        "transfer.progress" => {
            if let Some(t) = p
                .get("transfer")
                .and_then(|v| serde_json::from_value::<Transfer>(v.clone()).ok())
            {
                ctx.state.borrow_mut().upsert_transfer(t);
                ctx.render_transfers();
                maybe_prompt_inbound(ctx);
            }
        }
        "transfer.done" => {
            if let Some(id) = p.get("transferId").and_then(|v| v.as_str()) {
                if let Some(slot) = ctx
                    .state
                    .borrow_mut()
                    .transfers
                    .iter_mut()
                    .find(|x| x.id == id)
                {
                    slot.state = crate::models::TransferState::Done;
                    slot.speed_bps = 0;
                }
                ctx.render_transfers();
                ctx.toast("文件传输完成（SHA-256 校验通过）");
            }
        }
        "transfer.failed" => {
            if let Some(id) = p.get("transferId").and_then(|v| v.as_str()) {
                let reason = p
                    .get("reason")
                    .and_then(|v| v.as_str())
                    .unwrap_or("未知原因")
                    .to_string();
                if let Some(slot) = ctx
                    .state
                    .borrow_mut()
                    .transfers
                    .iter_mut()
                    .find(|x| x.id == id)
                {
                    slot.state = crate::models::TransferState::Failed;
                    slot.error_reason = Some(reason.clone());
                }
                ctx.render_transfers();
                ctx.toast(&format!("传输失败：{reason}"));
            }
        }
        // M9 G1 矩阵事件：当前 UI 不展示位图进度，安全忽略。
        "group.matrix" => {}
        "channel.updated" => {
            if let Some(channels) = parse_channel_updated(p) {
                ctx.state.borrow_mut().channels = channels;
                ctx.render_channels();
                // 若正在查看频道，刷新标题中的成员信息。
                if let ChatTarget::Channel(id) = &ctx.state.borrow().current_chat {
                    if let Some(ch) = ctx.state.borrow().channel(id).cloned() {
                        ctx.widgets.chat_header.set_text(&channel_header_text(&ch));
                    }
                }
            }
        }
        other => tracing::debug!("未处理通知：{other}"),
    }
}

/// 解析 channel.updated payload：{channels: Channel[]}。
fn parse_channel_updated(p: &serde_json::Value) -> Option<Vec<Channel>> {
    p.get("channels")
        .cloned()
        .and_then(|v| serde_json::from_value(v).ok())
}

/// 频道会话标题：名称 + 成员数（私有频道加锁；有主题则追加）。
fn channel_header_text(ch: &Channel) -> String {
    let lock = if ch.private { "🔒 " } else { "" };
    let topic = if ch.topic.trim().is_empty() {
        String::new()
    } else {
        format!(" · {}", ch.topic)
    };
    format!("{lock}# {}（{} 名成员）{topic}", ch.name, ch.members.len())
}

/// 切换到频道会话：取消 roster 选中、设置会话键与标题、渲染消息。
fn open_channel(ctx: &Rc<AppCtx>, channel_id: &str) {
    let Some(ch) = ctx.state.borrow().channel(channel_id).cloned() else {
        return;
    };
    ctx.widgets.roster.unselect_all();
    ctx.state.borrow_mut().current_chat = ChatTarget::Channel(ch.id.clone());
    ctx.widgets.chat_header.set_text(&channel_header_text(&ch));
    ctx.render_messages();
}

/// 邀请成员对话框：列出在线 peers，已是成员者禁用并标注；点击即邀请。
fn invite_member_dialog(ctx: &Rc<AppCtx>, channel_id: &str) {
    let list = gtk::ListBox::new();
    list.add_css_class("boxed-list");
    list.set_selection_mode(gtk::SelectionMode::Single);

    let st = ctx.state.borrow();
    let self_id = st.self_id.clone();
    let members: Vec<String> = st
        .channel(channel_id)
        .map(|c| c.members.clone())
        .unwrap_or_default();

    let candidates = st
        .peers
        .iter()
        .filter(|p| p.id != self_id)
        .cloned()
        .collect::<Vec<_>>();
    drop(st);

    if candidates.is_empty() {
        let row = adw::ActionRow::new();
        row.set_title("暂无可邀请的在线成员");
        row.set_sensitive(false);
        list.append(&row);
    }
    for peer in &candidates {
        let already = members.iter().any(|m| m == &peer.id);
        let row = adw::ActionRow::new();
        row.set_title(&peer.nickname);
        row.set_subtitle(if already { "已是成员" } else { &peer.os });
        if already {
            row.set_sensitive(false);
        }
        list.append(&row);
    }

    let scroll = gtk::ScrolledWindow::new();
    scroll.set_min_content_height(280);
    scroll.set_child(Some(&list));

    let dialog = adw::AlertDialog::new(Some("邀请成员加入频道"), None);
    dialog.set_extra_child(Some(&scroll));
    dialog.add_responses(&[("close", "关闭")]);
    dialog.set_close_response("close");

    // 行选中（仅非成员行可点）→ 邀请后关闭。
    list.connect_row_activated(clone!(
        #[strong(rename_to = cx)]
        ctx,
        #[strong]
        dialog,
        #[strong]
        candidates,
        #[strong]
        members,
        #[strong(rename_to = cid)]
        channel_id.to_string(),
        move |_, row| {
            let idx = row.index();
            if idx < 0 || idx as usize >= candidates.len() {
                return;
            }
            let member_id = candidates[idx as usize].id.clone();
            if members.iter().any(|m| m == &member_id) {
                return;
            }
            if let Some(ipc) = cx.ipc.borrow().clone() {
                let cid2 = cid.clone();
                let mid = member_id.clone();
                let tx = cx.ui_tx.clone();
                crate::spawn_rt(async move {
                    let r = ipc.channel_invite(&cid2, &mid).await;
                    let _ = tx.try_send(UiEvent::ChannelInvited(cid2, r));
                });
            }
            dialog.close();
        }
    ));

    dialog.present(Some(&ctx.widgets.window));
}

/// 对所有 pending 状态的 inbound 传输弹一次接收确认。
fn maybe_prompt_inbound(ctx: &Rc<AppCtx>) {
    let pending: Vec<Transfer> = ctx
        .state
        .borrow()
        .transfers
        .iter()
        .filter(|t| {
            t.state == crate::models::TransferState::Pending
                && t.direction == crate::models::TransferDirection::Inbound
        })
        .cloned()
        .collect();

    for t in pending {
        let dialog =
            adw::AlertDialog::new(Some("接收文件"), Some(&format!("对方发送：{}", t.name)));
        dialog.add_responses(&[("reject", "拒绝"), ("accept", "接收")]);
        dialog.set_response_appearance("accept", adw::ResponseAppearance::Suggested);
        dialog.set_close_response("reject");
        dialog.choose(
            &ctx.widgets.window,
            None::<&gio::Cancellable>,
            clone!(
                #[strong]
                ctx,
                #[strong]
                t,
                move |response| {
                    if response != "accept" {
                        respond_transfer(&ctx, &t.id, false, String::new());
                    } else {
                        // 选保存目录（文件名为对端提供的 name）。
                        let fdialog = gtk::FileDialog::new();
                        fdialog.set_initial_name(Some(&t.name));
                        fdialog.select_folder(
                            Some(&ctx.widgets.window),
                            None::<&gio::Cancellable>,
                            clone!(
                                #[strong]
                                ctx,
                                #[strong]
                                t,
                                move |res| {
                                    if let Ok(dir) = res {
                                        if let Some(base) = dir.path() {
                                            let dest =
                                                base.join(&t.name).to_string_lossy().to_string();
                                            respond_transfer(&ctx, &t.id, true, dest);
                                        }
                                    } else {
                                        respond_transfer(&ctx, &t.id, false, String::new());
                                    }
                                }
                            ),
                        );
                    }
                }
            ),
        );
    }
}

fn respond_transfer(ctx: &Rc<AppCtx>, id: &str, accept: bool, dest: String) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let params = RespondParams {
            transfer_id: id.to_string(),
            accept,
            dest,
        };
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let r = ipc.invoke("RespondFile", Some(&params)).await.map(|_| ());
            let _ = tx.try_send(UiEvent::Responded(r));
        });
    }
}

// ---------- 渲染 ----------

fn render_all(ctx: &Rc<AppCtx>, was_connected: bool) {
    ctx.widgets
        .conn_badge
        .set_text(ctx.state.borrow().conn.label());
    ctx.widgets
        .me_label
        .set_text(&format!("我：{}", ctx.state.borrow().nickname));
    ctx.render_roster();
    ctx.render_messages();
    ctx.render_transfers();

    let now_connected = ctx.state.borrow().conn == ConnState::Connected;
    if now_connected && !was_connected {
        ctx.widgets.stack.set_visible_child_name("main");
    } else if !now_connected && was_connected {
        ctx.widgets.stack.set_visible_child_name("login");
    }
}

impl AppCtx {
    fn toast(&self, msg: &str) {
        self.widgets.toast.add_toast(adw::Toast::new(msg));
    }

    fn set_login_busy(&self, busy: bool, status: &str) {
        self.widgets.connect_btn.set_sensitive(!busy);
        self.widgets.discover_btn.set_sensitive(!busy);
        if !status.is_empty() {
            self.widgets.login_status.set_text(status);
        }
    }

    fn sync_state(&self) {
        if let Some(ipc) = self.ipc.borrow().clone() {
            let tx = self.ui_tx.clone();
            crate::spawn_rt(async move {
                let res = ipc
                    .invoke::<()>("GetState", None)
                    .await
                    .and_then(|v| serde_json::from_value::<State>(v).map_err(Into::into));
                let _ = tx.try_send(UiEvent::State(res));
            });
        }
    }

    fn render_discovered(&self) {
        clear_listbox(&self.widgets.server_list);
        for srv in &self.state.borrow().discovered {
            let row = adw::ActionRow::new();
            row.set_title(if srv.name.is_empty() {
                &srv.addr
            } else {
                &srv.name
            });
            row.set_subtitle(&format!("{} · {}", srv.addr, srv.version));
            let badge = gtk::Label::new(Some(&srv.auth_mode));
            badge.add_css_class("caption-heading");
            badge.add_css_class("dim-label");
            row.add_suffix(&badge);
            self.widgets.server_list.append(&row);
        }
    }

    fn render_roster(&self) {
        clear_listbox(&self.widgets.roster);
        let st = self.state.borrow();
        if st.peers.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂无在线联系人");
            row.set_sensitive(false);
            self.widgets.roster.append(&row);
            return;
        }
        for peer in &st.peers {
            let row = adw::ActionRow::new();
            row.set_title(&peer.nickname);
            row.set_subtitle(&format!("{} · {}", peer.os, peer.status));
            self.widgets.roster.append(&row);
        }
    }

    fn render_channels(self: &Rc<Self>) {
        clear_listbox(&self.widgets.channels_list);
        let st = self.state.borrow();
        let self_id = st.self_id.clone();
        if st.channels.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂无频道，点击上方「新建频道」");
            row.set_sensitive(false);
            self.widgets.channels_list.append(&row);
            return;
        }
        for ch in &st.channels {
            let row = adw::ActionRow::new();
            row.set_title(&ch.name);
            let members_text = ch
                .members
                .iter()
                .map(|id| {
                    if id == &self_id {
                        "我".to_string()
                    } else {
                        st.peer(id).map(|p| p.nickname.clone()).unwrap_or_else(|| {
                            id.get(0..6).map(|s| s.to_string()).unwrap_or_default()
                        })
                    }
                })
                .collect::<Vec<_>>()
                .join("、");
            let is_owner = ch.owner_id == self_id;
            let prefix = if ch.private { "🔒 私有 · " } else { "" };
            let topic_text = if ch.topic.trim().is_empty() {
                String::new()
            } else {
                format!("{} · ", ch.topic)
            };
            row.set_subtitle(&format!(
                "{prefix}{topic_text}{}{}名成员：{members_text}",
                if is_owner { "我是频道主 · " } else { "" },
                ch.members.len()
            ));

            let member = ch.members.iter().any(|m| m == &self_id);
            let channel_id = ch.id.clone();
            if member {
                // 仅 owner 显示「邀请」入口。
                if is_owner {
                    let invite_btn = gtk::Button::with_label("邀请");
                    invite_btn.connect_clicked(clone!(
                        #[strong(rename_to = cx)]
                        self,
                        #[strong]
                        channel_id,
                        move |_| {
                            invite_member_dialog(&cx, &channel_id);
                        }
                    ));
                    row.add_suffix(&invite_btn);
                }

                // 已加入频道显示「退出」按钮。
                let leave_btn = gtk::Button::with_label("退出");
                leave_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    #[strong]
                    channel_id,
                    move |_| {
                        if let Some(ipc) = cx.ipc.borrow().clone() {
                            let id = channel_id.clone();
                            let tx = cx.ui_tx.clone();
                            crate::spawn_rt(async move {
                                let r = ipc.channel_leave(&id).await;
                                let _ = tx.try_send(UiEvent::ChannelLeft(id, r));
                            });
                        }
                    }
                ));
                row.add_suffix(&leave_btn);

                let open_btn = gtk::Button::with_label("进入");
                open_btn.add_css_class("suggested-action");
                open_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    #[strong]
                    channel_id,
                    move |_| {
                        open_channel(&cx, &channel_id);
                        cx.widgets.channels_win.close();
                    }
                ));
                row.add_suffix(&open_btn);
            } else {
                let join_btn = gtk::Button::with_label("加入");
                join_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    #[strong]
                    channel_id,
                    move |_| {
                        if let Some(ipc) = cx.ipc.borrow().clone() {
                            let id = channel_id.clone();
                            let tx = cx.ui_tx.clone();
                            crate::spawn_rt(async move {
                                let r = ipc.channel_join(&id).await;
                                let _ = tx.try_send(UiEvent::ChannelJoined(id, r));
                            });
                        }
                    }
                ));
                row.add_suffix(&join_btn);
            }
            self.widgets.channels_list.append(&row);
        }
    }

    fn render_messages(&self) {
        clear_listbox(&self.widgets.msg_list);
        let st = self.state.borrow();
        let target = st.current_chat.clone();
        let Some(conv_key) = target.key().map(|s| s.to_string()) else {
            return;
        };
        let Some(msgs) = st.messages.get(&conv_key) else {
            return;
        };
        for m in msgs {
            let label_text = if target.is_channel() && m.inbound {
                let who = st
                    .peer(&m.sender_id)
                    .map(|p| p.nickname.clone())
                    .unwrap_or_else(|| {
                        m.sender_id
                            .get(0..6)
                            .map(|s| s.to_string())
                            .unwrap_or_default()
                    });
                glib::markup_escape_text(&format!("{who}：{}", m.text)).to_string()
            } else {
                glib::markup_escape_text(&m.text).to_string()
            };
            let bubble = gtk::Label::new(None);
            bubble.set_use_markup(true);
            bubble.set_label(&label_text);
            bubble.set_wrap(true);
            bubble.set_max_width_chars(60);
            bubble.set_xalign(if m.inbound { 0.0 } else { 1.0 });
            bubble.add_css_class(if m.inbound { "bubble-in" } else { "bubble-out" });

            let row = gtk::Box::new(Orientation::Horizontal, 0);
            if !m.inbound {
                row.set_halign(Align::End);
            }
            row.append(&bubble);
            row.set_margin_top(3);
            row.set_margin_bottom(3);
            self.widgets.msg_list.append(&row);
        }
        drop(st);
        // 滚动到底部。
        let adj = self.widgets.msg_scroll.vadjustment();
        adj.set_value(adj.upper());
    }

    fn render_transfers(self: &Rc<Self>) {
        clear_listbox(&self.widgets.transfers_list);
        let st = self.state.borrow();
        if st.transfers.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂无传输任务");
            self.widgets.transfers_list.append(&row);
            return;
        }
        for t in &st.transfers {
            let bar = gtk::ProgressBar::new();
            bar.set_fraction(t.fraction());
            bar.set_text(Some(&format!(
                "{} / {}",
                format_size(t.bytes_done),
                format_size(t.size)
            )));
            bar.set_show_text(true);

            let path_badge =
                gtk::Label::new(Some(if t.via_relay { "⚠ 中继" } else { "直连" }));
            path_badge.add_css_class("caption-heading");
            if t.via_relay {
                path_badge.add_css_class("warning");
            }

            let kind_badge = gtk::Label::new(Some(match t.kind {
                crate::models::PathKind::Unicast => "单播",
                crate::models::PathKind::Swarm => "群播",
                crate::models::PathKind::Channel => "频道",
            }));
            kind_badge.add_css_class("caption-heading");
            kind_badge.add_css_class("dim-label");

            let state_label = gtk::Label::new(Some(t.state.label()));
            state_label.add_css_class("dim-label");
            state_label.add_css_class("caption");

            let speed = gtk::Label::new(Some(&format_speed(t.speed_bps)));
            speed.add_css_class("dim-label");
            speed.add_css_class("caption");

            let meta = gtk::Box::new(Orientation::Horizontal, 8);
            meta.append(&state_label);
            meta.append(&speed);
            meta.append(&kind_badge);
            meta.append(&path_badge);

            let name_label = gtk::Label::builder()
                .label(if t.kind == crate::models::PathKind::Channel {
                    format!("{} · #{} · {}", t.direction.label(), t.group_id, t.name)
                } else {
                    format!("{} · {}", t.direction.label(), t.name)
                })
                .halign(Align::Start)
                .build();

            let body = gtk::Box::new(Orientation::Vertical, 4);
            body.append(&name_label);
            body.append(&bar);
            body.append(&meta);

            let row = adw::ActionRow::new();
            row.set_child(Some(&body));

            let active = matches!(
                t.state,
                crate::models::TransferState::Pending
                    | crate::models::TransferState::Active
                    | crate::models::TransferState::Paused
            );
            if active {
                let cancel = gtk::Button::from_icon_name("process-stop-symbolic");
                cancel.set_tooltip_text(Some("取消"));
                let id = t.id.clone();
                cancel.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        cancel_transfer(&cx, &id);
                    }
                ));
                row.add_suffix(&cancel);
            }
            if let Some(reason) = &t.error_reason {
                let err = gtk::Label::new(Some(reason));
                err.add_css_class("error");
                err.add_css_class("caption");
                row.add_suffix(&err);
            }
            self.widgets.transfers_list.append(&row);
        }
    }
}

fn cancel_transfer(ctx: &Rc<AppCtx>, id: &str) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let transfer_id = id.to_string();
        let params = IdParams {
            transfer_id: transfer_id.clone(),
        };
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let r = ipc.invoke("CancelFile", Some(&params)).await.map(|_| ());
            let _ = tx.try_send(UiEvent::Canceled(transfer_id, r));
        });
    }
}

fn clear_listbox(lb: &gtk::ListBox) {
    while let Some(child) = lb.first_child() {
        lb.remove(&child);
    }
}

fn server_addr_with_path(srv: &ServerInfo) -> String {
    let addr = &srv.addr;
    if addr.contains('/') {
        addr.clone()
    } else {
        format!("{addr}/lanchat")
    }
}

fn now_secs() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

fn load_css() {
    const CSS: &str = r#"
    .messages { background: transparent; }
    .bubble-in, .bubble-out {
        padding: 8px 12px;
        border-radius: 10px;
    }
    .bubble-in { background: alpha(#888, .18); }
    .bubble-out { background: #3584e4; color: white; }
    .warning { color: #e5a50a; }
    .error { color: #e01b24; }
    .caption { font-size: 0.8em; }
    "#;
    let provider = gtk::CssProvider::new();
    provider.load_from_string(CSS);
    gtk::style_context_add_provider_for_display(
        &Display::default().expect("无显示设备"),
        &provider,
        gtk::STYLE_PROVIDER_PRIORITY_APPLICATION,
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn channel_updated_parsed() {
        let p = serde_json::json!({
            "channels": [
                {"id": "c1", "name": "运维", "ownerId": "p1", "members": ["p1", "p2"]}
            ]
        });
        let channels = parse_channel_updated(&p).expect("应解析成功");
        assert_eq!(channels.len(), 1);
        assert_eq!(channels[0].id, "c1");
        assert_eq!(channel_header_text(&channels[0]), "# 运维（2 名成员）");
    }

    #[test]
    fn channel_header_shows_private_and_topic() {
        let ch = Channel {
            id: "c1".into(),
            name: "运维".into(),
            owner_id: "p1".into(),
            private: true,
            topic: "值班".into(),
            members: vec!["p1".into()],
        };
        assert_eq!(channel_header_text(&ch), "🔒 # 运维（1 名成员） · 值班");
    }

    #[test]
    fn channel_updated_missing_channels() {
        let p = serde_json::json!({});
        assert!(parse_channel_updated(&p).is_none());
        let p = serde_json::json!({"channels": "not-an-array"});
        assert!(parse_channel_updated(&p).is_none());
    }

    #[test]
    fn chat_target_keys() {
        assert_eq!(ChatTarget::None.key(), None);
        assert_eq!(ChatTarget::Peer("p1".into()).key(), Some("p1"));
        assert!(ChatTarget::Channel("c1".into()).is_channel());
        assert!(!ChatTarget::Peer("p1".into()).is_channel());
    }
}
