//! GTK4 + libadwaita 主界面与事件处理（桌面版 QQ / NTQQ 布局与视觉风格）。
//!
//! 线程模型：GTK 主循环独占主线程并持有 AppState；tokio 后台线程
//! 经 `async_channel` 把 UI 事件 marshal 回主线程。

use crate::daemon::ensure_daemon;
use crate::ipc::{default_socket_path, IpcClient, Notification};
use crate::models::{
    format_matrix_summary, format_size, format_speed, Channel, ChatMessage, ConnState, ServerInfo,
    State, Transfer,
};
use crate::state::{AppState, ChatTarget, NavTab};
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
    /// 文本/表情发送回执：(会话键, group, 文本, 响应 msgID)
    TextSent(String, String, String, Result<String>),
    OfferSent(Result<String>),
    ChannelCreated(Result<String>),
    ChannelJoined(String, Result<()>),
    ChannelInvited(String, Result<()>),
    ChannelLeft(String, Result<()>),
    Responded(Result<()>),
    Paused(String, Result<()>),
    Resumed(String, Result<()>),
    Canceled(String, Result<()>),
    NickSet(Result<()>),
    DirSet(Result<()>),
    Notif(Notification),
}

struct Widgets {
    window: adw::ApplicationWindow,
    stack: gtk::Stack,
    toast: adw::ToastOverlay,

    // 登录页（QQ 登录窗口风格）
    server_list: gtk::ListBox,
    addr_row: adw::EntryRow,
    psk_row: adw::PasswordEntryRow,
    discover_btn: gtk::Button,
    connect_btn: gtk::Button,
    login_status: gtk::Label,
    login_spinner: gtk::Spinner,
    copy_status_btn: gtk::Button,

    // 左侧 QQ 窄导航栏（Column 0）
    self_avatar: adw::Avatar,
    self_profile_btn: gtk::Button,
    nav_sessions_btn: gtk::Button,
    nav_contacts_btn: gtk::Button,
    nav_channels_btn: gtk::Button,
    transfers_btn: gtk::Button,
    channels_btn: gtk::Button,
    update_btn: gtk::Button,
    nick_btn: gtk::Button,
    dirdir_btn: gtk::Button,

    // 中左会话/联系人/频道列表栏（Column 1）
    search_entry: gtk::SearchEntry,
    quick_new_channel_btn: gtk::Button,
    me_label: gtk::Label,
    conn_badge: gtk::Label,
    roster: gtk::ListBox,

    // 右侧主聊天面板（Column 2）
    chat_avatar: adw::Avatar,
    chat_header: gtk::Label,
    chat_subtitle: gtk::Label,
    header_invite_btn: gtk::Button,
    header_leave_btn: gtk::Button,
    header_drawer_btn: gtk::Button,
    msg_list: gtk::ListBox,
    msg_scroll: gtk::ScrolledWindow,
    input: gtk::TextView,
    send_btn: gtk::Button,
    sticker_btn: gtk::Button,
    sticker_menu_btn: gtk::MenuButton,
    sticker_popover: gtk::Popover,
    file_btn: gtk::Button,
    voice_btn: gtk::Button,
    e2ee_btn: gtk::Button,
    fingerprint_btn: gtk::Button,
    search_chat_btn: gtk::Button,

    // 右侧可折叠文件传输侧栏（Column 3）+ 独立窗口兼容
    right_drawer: gtk::Box,
    drawer_transfers_list: gtk::ListBox,
    transfers_win: gtk::Window,
    transfers_list: gtk::ListBox,

    // 频道管理窗口
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
    session_targets: Rc<RefCell<Vec<ChatTarget>>>,
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
        session_targets: Rc::new(RefCell::new(Vec::new())),
    });

    wire_actions(&ctx);
    wire_window_lifecycle(&ctx);

    let ctx_weak = Rc::downgrade(&ctx);
    MainContext::default().spawn_local(async move {
        while let Ok(event) = ui_rx.recv().await {
            if let Some(ctx) = ctx_weak.upgrade() {
                handle_event(&ctx, event);
            }
        }
    });

    ctx.widgets.window.present();

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

// ---------- UI 构建（Linux QQ / NTQQ 布局） ----------

fn build_ui(app: &adw::Application) -> Widgets {
    // ═══ 1. QQ 风格登录页 ═══
    let login_avatar = adw::Avatar::new(76, Some("LANChat"), true);
    login_avatar.set_icon_name(Some("chat-message-new-symbolic"));
    login_avatar.add_css_class("qq-hero-avatar");

    let login_title = gtk::Label::new(Some("LANChat 局域网即时通信"));
    login_title.add_css_class("title-2");
    let login_subtitle = gtk::Label::new(Some("P2P 高速直连 · 端到端加密 · 零配置发现"));
    login_subtitle.add_css_class("dim-label");
    login_subtitle.add_css_class("caption");

    let discover_title = gtk::Label::new(Some("📡 局域网中心节点"));
    discover_title.set_xalign(0.0);
    discover_title.set_hexpand(true);
    discover_title.add_css_class("heading");

    let discover_btn = gtk::Button::with_label("🔄 自动发现");
    discover_btn.add_css_class("flat");
    discover_btn.add_css_class("qq-link-btn");

    let discover_header = gtk::Box::new(Orientation::Horizontal, 8);
    discover_header.append(&discover_title);
    discover_header.append(&discover_btn);

    let server_list = gtk::ListBox::new();
    server_list.set_selection_mode(gtk::SelectionMode::Single);
    server_list.add_css_class("boxed-list");
    let server_scroll = gtk::ScrolledWindow::new();
    server_scroll.set_min_content_height(110);
    server_scroll.set_max_content_height(150);
    server_scroll.set_child(Some(&server_list));

    let addr_row = adw::EntryRow::new();
    addr_row.set_title("服务器地址 (auto 或留空零配置自动选主)");
    addr_row.set_text("auto");

    let psk_row = adw::PasswordEntryRow::new();
    psk_row.set_title("共享口令 PSK（免密节点可留空）");

    let form_list = gtk::ListBox::new();
    form_list.set_selection_mode(gtk::SelectionMode::None);
    form_list.add_css_class("boxed-list");
    form_list.append(&addr_row);
    form_list.append(&psk_row);

    let login_status = gtk::Label::new(Some("正在启动本地守护服务…"));
    login_status.set_xalign(0.5);
    login_status.set_wrap(true);
    login_status.set_selectable(true);
    login_status.add_css_class("dim-label");
    login_status.add_css_class("caption");

    let copy_status_btn = gtk::Button::with_label("📋 复制");
    copy_status_btn.add_css_class("flat");
    copy_status_btn.add_css_class("caption");
    copy_status_btn.set_tooltip_text(Some("复制状态或错误信息到剪贴板"));
    copy_status_btn.set_visible(false);

    let login_spinner = gtk::Spinner::new();
    login_spinner.start();
    let status_row = gtk::Box::new(Orientation::Horizontal, 6);
    status_row.set_halign(Align::Center);
    status_row.append(&login_spinner);
    status_row.append(&login_status);
    status_row.append(&copy_status_btn);

    let connect_btn = gtk::Button::with_label("立即登录");
    connect_btn.add_css_class("pill");
    connect_btn.add_css_class("suggested-action");
    connect_btn.add_css_class("qq-login-btn");
    connect_btn.set_hexpand(true);

    let login_card = gtk::Box::new(Orientation::Vertical, 14);
    login_card.add_css_class("qq-login-card");
    login_card.set_margin_top(28);
    login_card.set_margin_bottom(28);
    login_card.set_margin_start(28);
    login_card.set_margin_end(28);
    login_card.append(&login_avatar);
    login_card.append(&login_title);
    login_card.append(&login_subtitle);
    login_card.append(&discover_header);
    login_card.append(&server_scroll);
    login_card.append(&form_list);
    login_card.append(&status_row);
    login_card.append(&connect_btn);

    let login_clamp = adw::Clamp::new();
    login_clamp.set_maximum_size(440);
    login_clamp.set_valign(Align::Center);
    login_clamp.set_child(Some(&login_card));

    let login_bg = gtk::Box::new(Orientation::Vertical, 0);
    login_bg.set_vexpand(true);
    login_bg.set_hexpand(true);
    login_bg.set_valign(Align::Center);
    login_bg.add_css_class("qq-login-bg");
    login_bg.append(&login_clamp);

    // ═══ 2. 主界面：NTQQ 四栏式布局 ═══
    // --- Column 0：最左侧深色导航轨（64px） ---
    let self_avatar = adw::Avatar::new(40, Some("我"), true);
    let self_profile_btn = gtk::Button::new();
    self_profile_btn.set_child(Some(&self_avatar));
    self_profile_btn.add_css_class("flat");
    self_profile_btn.add_css_class("qq-avatar-btn");
    self_profile_btn.set_tooltip_text(Some("点击修改个人昵称"));

    let nav_sessions_btn = gtk::Button::with_label("💬\n消息");
    nav_sessions_btn.add_css_class("flat");
    nav_sessions_btn.add_css_class("qq-rail-btn");
    nav_sessions_btn.add_css_class("qq-rail-active");
    nav_sessions_btn.set_tooltip_text(Some("消息会话列表"));

    let nav_contacts_btn = gtk::Button::with_label("👥\n好友");
    nav_contacts_btn.add_css_class("flat");
    nav_contacts_btn.add_css_class("qq-rail-btn");
    nav_contacts_btn.set_tooltip_text(Some("在线局域网好友"));

    let nav_channels_btn = gtk::Button::with_label("#️⃣\n频道");
    nav_channels_btn.add_css_class("flat");
    nav_channels_btn.add_css_class("qq-rail-btn");
    nav_channels_btn.set_tooltip_text(Some("群聊与自定义频道"));

    let transfers_btn = gtk::Button::with_label("⚡\n传输");
    transfers_btn.add_css_class("flat");
    transfers_btn.add_css_class("qq-rail-btn");
    transfers_btn.set_tooltip_text(Some("P2P 文件传输管理"));

    let rail_spacer = gtk::Box::new(Orientation::Vertical, 0);
    rail_spacer.set_vexpand(true);

    let channels_btn = gtk::Button::with_label("🌐");
    channels_btn.add_css_class("flat");
    channels_btn.add_css_class("qq-rail-sub-btn");
    channels_btn.set_tooltip_text(Some("打开频道广场窗口"));

    let dirdir_btn = gtk::Button::with_label("📂");
    dirdir_btn.add_css_class("flat");
    dirdir_btn.add_css_class("qq-rail-sub-btn");
    dirdir_btn.set_tooltip_text(Some("设置默认下载目录"));

    let update_btn = gtk::Button::with_label("🚀");
    update_btn.add_css_class("flat");
    update_btn.add_css_class("qq-rail-sub-btn");
    update_btn.set_tooltip_text(Some("版本更新（国内加速代理静默下载 · 免浏览器）"));

    let nick_btn = gtk::Button::with_label("✏️");
    nick_btn.add_css_class("flat");
    nick_btn.add_css_class("qq-rail-sub-btn");
    nick_btn.set_tooltip_text(Some("修改个人昵称"));

    let left_rail = gtk::Box::new(Orientation::Vertical, 10);
    left_rail.set_size_request(68, -1);
    left_rail.add_css_class("qq-left-rail");
    left_rail.append(&self_profile_btn);
    left_rail.append(&nav_sessions_btn);
    left_rail.append(&nav_contacts_btn);
    left_rail.append(&nav_channels_btn);
    left_rail.append(&transfers_btn);
    left_rail.append(&rail_spacer);
    left_rail.append(&channels_btn);
    left_rail.append(&update_btn);
    left_rail.append(&dirdir_btn);
    left_rail.append(&nick_btn);

    // --- Column 1：中左会话/联系人/频道列表栏（275px） ---
    let search_entry = gtk::SearchEntry::new();
    search_entry.set_placeholder_text(Some("搜索好友、频道或记录…"));
    search_entry.set_hexpand(true);
    search_entry.add_css_class("qq-search-entry");

    let quick_new_channel_btn = gtk::Button::with_label("＋");
    quick_new_channel_btn.add_css_class("flat");
    quick_new_channel_btn.add_css_class("qq-plus-btn");
    quick_new_channel_btn.set_tooltip_text(Some("新建频道 (G2)"));

    let search_bar_row = gtk::Box::new(Orientation::Horizontal, 6);
    search_bar_row.set_margin_top(12);
    search_bar_row.set_margin_bottom(6);
    search_bar_row.set_margin_start(12);
    search_bar_row.set_margin_end(12);
    search_bar_row.append(&search_entry);
    search_bar_row.append(&quick_new_channel_btn);

    let me_label = gtk::Label::new(Some("我"));
    me_label.set_xalign(0.0);
    me_label.set_hexpand(true);
    me_label.set_selectable(true);
    me_label.add_css_class("caption-heading");

    let conn_badge = gtk::Label::new(Some("● 在线"));
    conn_badge.set_xalign(1.0);
    conn_badge.add_css_class("qq-online-pill");

    let profile_strip = gtk::Box::new(Orientation::Horizontal, 6);
    profile_strip.set_margin_start(14);
    profile_strip.set_margin_end(14);
    profile_strip.set_margin_bottom(6);
    profile_strip.append(&me_label);
    profile_strip.append(&conn_badge);

    let roster = gtk::ListBox::new();
    roster.set_selection_mode(gtk::SelectionMode::Single);
    roster.add_css_class("navigation-sidebar");
    roster.add_css_class("qq-session-list");
    let roster_scroll = gtk::ScrolledWindow::new();
    roster_scroll.set_child(Some(&roster));
    roster_scroll.set_min_content_width(265);
    roster_scroll.set_vexpand(true);

    let sidebar = gtk::Box::new(Orientation::Vertical, 0);
    sidebar.set_size_request(275, -1);
    sidebar.add_css_class("qq-session-pane");
    sidebar.append(&search_bar_row);
    sidebar.append(&profile_strip);
    sidebar.append(&gtk::Separator::new(Orientation::Horizontal));
    sidebar.append(&roster_scroll);

    // --- Column 2：右侧主聊天面板（自适应 hexpand） ---
    let chat_avatar = adw::Avatar::new(38, Some("局域网全员广播群"), true);
    let chat_header = gtk::Label::new(Some("📢 局域网全员广播群"));
    chat_header.set_xalign(0.0);
    chat_header.set_selectable(true);
    chat_header.add_css_class("title-4");

    let chat_subtitle = gtk::Label::new(Some("● 全员在线广播 (G1 Swarm) · P2P 高速直连就绪"));
    chat_subtitle.set_xalign(0.0);
    chat_subtitle.set_selectable(true);
    chat_subtitle.add_css_class("caption");
    chat_subtitle.add_css_class("dim-label");

    let header_titles = gtk::Box::new(Orientation::Vertical, 2);
    header_titles.set_hexpand(true);
    header_titles.set_valign(Align::Center);
    header_titles.append(&chat_header);
    header_titles.append(&chat_subtitle);

    let header_invite_btn = gtk::Button::with_label("➕ 邀请");
    header_invite_btn.add_css_class("flat");
    header_invite_btn.set_visible(false);
    header_invite_btn.set_tooltip_text(Some("邀请在线好友加入本频道"));

    let header_leave_btn = gtk::Button::with_label("🚪 退出");
    header_leave_btn.add_css_class("flat");
    header_leave_btn.set_visible(false);
    header_leave_btn.set_tooltip_text(Some("退出当前自定义频道"));

    let header_drawer_btn = gtk::Button::with_label("📊 传输面板");
    header_drawer_btn.add_css_class("flat");
    header_drawer_btn.set_tooltip_text(Some("展开/收起右侧文件传输面板"));

    let header_bar = gtk::Box::new(Orientation::Horizontal, 10);
    header_bar.add_css_class("qq-chat-header");
    header_bar.set_margin_top(10);
    header_bar.set_margin_bottom(10);
    header_bar.set_margin_start(16);
    header_bar.set_margin_end(16);
    header_bar.append(&chat_avatar);
    header_bar.append(&header_titles);
    header_bar.append(&header_invite_btn);
    header_bar.append(&header_leave_btn);
    header_bar.append(&header_drawer_btn);

    let msg_list = gtk::ListBox::new();
    msg_list.set_selection_mode(gtk::SelectionMode::None);
    msg_list.add_css_class("messages");
    let msg_scroll = gtk::ScrolledWindow::new();
    msg_scroll.set_child(Some(&msg_list));
    msg_scroll.set_vexpand(true);
    msg_scroll.set_hexpand(true);
    msg_scroll.add_css_class("qq-msg-scroll");

    // 底部 QQ 风格工具栏 + 输入框
    let sticker_btn = gtk::Button::with_label("🖼️ 贴纸");
    sticker_btn.add_css_class("flat");
    sticker_btn.add_css_class("qq-tool-btn");
    sticker_btn.set_tooltip_text(Some("选择本地贴纸图片发送"));

    let sticker_popover = gtk::Popover::new();
    let sticker_menu_btn = gtk::MenuButton::builder()
        .label("😊 表情")
        .popover(&sticker_popover)
        .build();
    sticker_menu_btn.add_css_class("flat");
    sticker_menu_btn.add_css_class("qq-tool-btn");
    sticker_menu_btn.set_tooltip_text(Some("选择表情插入到输入框"));

    let file_btn = gtk::Button::with_label("📁 发送文件");
    file_btn.add_css_class("flat");
    file_btn.add_css_class("qq-tool-btn");
    file_btn.set_tooltip_text(Some("发起 P2P 直连 / 群发文件传输"));

    let voice_btn = gtk::Button::with_label("🎤 语音");
    voice_btn.add_css_class("flat");
    voice_btn.add_css_class("qq-tool-btn");
    voice_btn.set_tooltip_text(Some("发送语音消息 (/voice)"));

    let e2ee_btn = gtk::Button::with_label("🔐 加密会话");
    e2ee_btn.add_css_class("flat");
    e2ee_btn.add_css_class("qq-tool-btn");
    e2ee_btn.set_tooltip_text(Some("切换端到端加密 (/e2ee)"));

    let fingerprint_btn = gtk::Button::with_label("🛡️ 安全码");
    fingerprint_btn.add_css_class("flat");
    fingerprint_btn.add_css_class("qq-tool-btn");
    fingerprint_btn.set_tooltip_text(Some("核验 X25519 端到端安全码指纹 (/fingerprint)"));

    let search_chat_btn = gtk::Button::with_label("🔍 查找记录");
    search_chat_btn.add_css_class("flat");
    search_chat_btn.add_css_class("qq-tool-btn");
    search_chat_btn.set_tooltip_text(Some("检索当前会话消息记录"));

    let toolbar_spacer = gtk::Box::new(Orientation::Horizontal, 0);
    toolbar_spacer.set_hexpand(true);

    let shortcut_hint = gtk::Label::new(Some("Enter 发送 · Shift+Enter 换行"));
    shortcut_hint.add_css_class("caption");
    shortcut_hint.add_css_class("dim-label");

    let composer_toolbar = gtk::Box::new(Orientation::Horizontal, 4);
    composer_toolbar.append(&sticker_menu_btn);
    composer_toolbar.append(&sticker_btn);
    composer_toolbar.append(&file_btn);
    composer_toolbar.append(&voice_btn);
    composer_toolbar.append(&e2ee_btn);
    composer_toolbar.append(&fingerprint_btn);
    composer_toolbar.append(&search_chat_btn);
    composer_toolbar.append(&toolbar_spacer);
    composer_toolbar.append(&shortcut_hint);

    let input = gtk::TextView::new();
    input.set_wrap_mode(gtk::WrapMode::WordChar);
    input.set_size_request(-1, 78);
    input.set_top_margin(8);
    input.set_bottom_margin(8);
    input.set_left_margin(10);
    input.set_right_margin(10);
    input.add_css_class("qq-input-view");
    let input_scroll = gtk::ScrolledWindow::new();
    input_scroll.set_child(Some(&input));
    input_scroll.set_min_content_height(78);
    input_scroll.add_css_class("qq-input-card");

    let send_btn = gtk::Button::with_label("发送 (S)");
    send_btn.add_css_class("suggested-action");
    send_btn.add_css_class("qq-send-btn");
    let btn_row = gtk::Box::new(Orientation::Horizontal, 8);
    btn_row.set_halign(Align::End);
    btn_row.append(&send_btn);

    let composer = gtk::Box::new(Orientation::Vertical, 6);
    composer.add_css_class("qq-composer");
    composer.set_margin_top(6);
    composer.set_margin_bottom(10);
    composer.set_margin_start(14);
    composer.set_margin_end(14);
    composer.append(&composer_toolbar);
    composer.append(&input_scroll);
    composer.append(&btn_row);

    let chat_col = gtk::Box::new(Orientation::Vertical, 0);
    chat_col.set_hexpand(true);
    chat_col.set_vexpand(true);
    chat_col.add_css_class("qq-chat-col");
    chat_col.append(&header_bar);
    chat_col.append(&gtk::Separator::new(Orientation::Horizontal));
    chat_col.append(&msg_scroll);
    chat_col.append(&gtk::Separator::new(Orientation::Horizontal));
    chat_col.append(&composer);

    // --- Column 3：右侧可折叠文件传输抽屉（285px） ---
    let drawer_title = gtk::Label::new(Some("⚡ P2P 文件传输"));
    drawer_title.set_xalign(0.0);
    drawer_title.set_hexpand(true);
    drawer_title.add_css_class("heading");
    let drawer_header = gtk::Box::new(Orientation::Horizontal, 8);
    drawer_header.set_margin_top(12);
    drawer_header.set_margin_bottom(10);
    drawer_header.set_margin_start(12);
    drawer_header.set_margin_end(12);
    drawer_header.append(&drawer_title);

    let drawer_transfers_list = gtk::ListBox::new();
    drawer_transfers_list.set_selection_mode(gtk::SelectionMode::None);
    drawer_transfers_list.add_css_class("boxed-list");
    let drawer_scroll = gtk::ScrolledWindow::new();
    drawer_scroll.set_vexpand(true);
    drawer_scroll.set_margin_start(10);
    drawer_scroll.set_margin_end(10);
    drawer_scroll.set_margin_bottom(10);
    drawer_scroll.set_child(Some(&drawer_transfers_list));

    let right_drawer = gtk::Box::new(Orientation::Vertical, 0);
    right_drawer.set_size_request(285, -1);
    right_drawer.add_css_class("qq-right-drawer");
    right_drawer.append(&drawer_header);
    right_drawer.append(&gtk::Separator::new(Orientation::Horizontal));
    right_drawer.append(&drawer_scroll);

    let main_split = gtk::Box::new(Orientation::Horizontal, 0);
    main_split.set_hexpand(true);
    main_split.set_vexpand(true);
    main_split.append(&left_rail);
    main_split.append(&sidebar);
    main_split.append(&gtk::Separator::new(Orientation::Vertical));
    main_split.append(&chat_col);
    main_split.append(&gtk::Separator::new(Orientation::Vertical));
    main_split.append(&right_drawer);

    // --- 独立传输窗口与频道窗口 ---
    let transfers_list = gtk::ListBox::new();
    transfers_list.set_selection_mode(gtk::SelectionMode::None);
    transfers_list.add_css_class("boxed-list");
    let t_scroll = gtk::ScrolledWindow::new();
    t_scroll.set_min_content_width(480);
    t_scroll.set_min_content_height(360);
    t_scroll.set_margin_top(12);
    t_scroll.set_margin_bottom(12);
    t_scroll.set_margin_start(12);
    t_scroll.set_margin_end(12);
    t_scroll.set_child(Some(&transfers_list));
    let transfers_win = gtk::Window::builder()
        .title("P2P 文件传输管理")
        .default_width(520)
        .default_height(420)
        .child(&t_scroll)
        .build();
    transfers_win.set_hide_on_close(true);

    let new_channel_btn = gtk::Button::with_label("＋ 新建频道");
    new_channel_btn.add_css_class("pill");
    new_channel_btn.add_css_class("suggested-action");
    let btn_wrap = gtk::Box::new(Orientation::Horizontal, 0);
    btn_wrap.set_halign(Align::Center);
    btn_wrap.append(&new_channel_btn);

    let channels_list = gtk::ListBox::new();
    channels_list.set_selection_mode(gtk::SelectionMode::None);
    channels_list.add_css_class("boxed-list");
    let c_scroll = gtk::ScrolledWindow::new();
    c_scroll.set_min_content_width(480);
    c_scroll.set_min_content_height(320);
    c_scroll.set_child(Some(&channels_list));

    let channels_content = gtk::Box::new(Orientation::Vertical, 12);
    channels_content.set_margin_top(14);
    channels_content.set_margin_bottom(14);
    channels_content.set_margin_start(14);
    channels_content.set_margin_end(14);
    channels_content.append(&btn_wrap);
    channels_content.append(&c_scroll);

    let channels_win = gtk::Window::builder()
        .title("频道广场 (G2)")
        .default_width(520)
        .default_height(440)
        .child(&channels_content)
        .build();
    channels_win.set_hide_on_close(true);

    let stack = gtk::Stack::new();
    stack.set_transition_type(gtk::StackTransitionType::Crossfade);
    stack.add_named(&login_bg, Some("login"));
    stack.add_named(&main_split, Some("main"));

    let toast = adw::ToastOverlay::new();
    toast.set_child(Some(&stack));

    // 使用 ToolbarView + HeaderBar 提供完整系统窗口标题栏与最小化/最大化/关闭按钮
    let win_header = adw::HeaderBar::new();
    let win_title = adw::WindowTitle::new("LANChat", "局域网 P2P 即时通信");
    win_header.set_title_widget(Some(&win_title));

    let toolbar_view = adw::ToolbarView::new();
    toolbar_view.add_top_bar(&win_header);
    toolbar_view.set_content(Some(&toast));

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("LANChat")
        .default_width(1140)
        .default_height(720)
        .content(&toolbar_view)
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
        copy_status_btn,
        self_avatar,
        self_profile_btn,
        nav_sessions_btn,
        nav_contacts_btn,
        nav_channels_btn,
        transfers_btn,
        channels_btn,
        update_btn,
        nick_btn,
        dirdir_btn,
        search_entry,
        quick_new_channel_btn,
        me_label,
        conn_badge,
        roster,
        chat_avatar,
        chat_header,
        chat_subtitle,
        header_invite_btn,
        header_leave_btn,
        header_drawer_btn,
        msg_list,
        msg_scroll,
        input,
        send_btn,
        sticker_btn,
        sticker_menu_btn,
        sticker_popover,
        file_btn,
        voice_btn,
        e2ee_btn,
        fingerprint_btn,
        search_chat_btn,
        right_drawer,
        drawer_transfers_list,
        transfers_win,
        transfers_list,
        channels_win,
        channels_list,
        new_channel_btn,
    }
}

// ---------- 动作绑定 ----------

fn wire_actions(ctx: &Rc<AppCtx>) {
    // 表情选择器网格（Popover）
    let grid = gtk::Grid::new();
    grid.set_row_spacing(6);
    grid.set_column_spacing(6);
    grid.set_margin_top(8);
    grid.set_margin_bottom(8);
    grid.set_margin_start(8);
    grid.set_margin_end(8);
    const EMOJIS: &[&str] = &[
        "🎉", "👍", "😂", "❤️", "🔥", "🚀", "👀", "🙏", "💯", "🥰", "🤔", "😭", "😎", "🤝", "✨",
        "🍀", "🎯", "⚡",
    ];
    for (idx, emoji) in EMOJIS.iter().enumerate() {
        let btn = gtk::Button::with_label(emoji);
        btn.add_css_class("flat");
        let e_str = (*emoji).to_string();
        btn.connect_clicked(clone!(
            #[strong(rename_to = cx)]
            ctx,
            move |_| {
                cx.widgets.sticker_popover.popdown();
                let buffer = cx.widgets.input.buffer();
                buffer.insert_at_cursor(&e_str);
                cx.widgets.input.grab_focus();
            }
        ));
        grid.attach(&btn, (idx % 6) as i32, (idx / 6) as i32, 1, 1);
    }
    ctx.widgets.sticker_popover.set_child(Some(&grid));

    // 自动发现
    ctx.widgets.discover_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            trigger_browse(&cx);
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

    // 复制登录状态/错误信息到剪贴板
    ctx.widgets.copy_status_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let text = cx.widgets.login_status.text();
            if !text.is_empty() {
                if let Some(display) = Display::default() {
                    display.clipboard().set_text(&text);
                    cx.toast("已复制错误信息到剪贴板");
                }
            }
        }
    ));

    // 连接（支持留空或 "auto" 零配置自动选主与自举）
    ctx.widgets.connect_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let raw_addr = cx.widgets.addr_row.text().to_string();
            let addr = if raw_addr.trim().is_empty() {
                "auto".to_string()
            } else {
                raw_addr
            };
            let psk = cx.widgets.psk_row.text().to_string();
            if cx.state.borrow().conn == ConnState::Connected
                && (addr.eq_ignore_ascii_case("auto") || addr == cx.state.borrow().server_addr)
            {
                cx.widgets.copy_status_btn.set_visible(false);
                cx.widgets.stack.set_visible_child_name("main");
                cx.sync_state();
                return;
            }
            cx.widgets.copy_status_btn.set_visible(false);
            cx.set_login_busy(true, "正在连接或自举局域网节点…");
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

    // 左侧导航栏切换：消息 / 好友 / 频道
    ctx.widgets.nav_sessions_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.switch_nav_tab(NavTab::Sessions);
        }
    ));
    ctx.widgets.nav_contacts_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.switch_nav_tab(NavTab::Contacts);
        }
    ));
    ctx.widgets.nav_channels_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.switch_nav_tab(NavTab::Channels);
        }
    ));

    // 搜索框实时过滤
    ctx.widgets.search_entry.connect_search_changed(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |entry| {
            cx.state.borrow_mut().search_filter = entry.text().to_string();
            cx.render_roster();
        }
    ));

    // 选择左侧列表项 → 切换当前会话
    ctx.widgets.roster.connect_row_selected(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, row| {
            if let Some(row) = row {
                let idx = row.index();
                if idx >= 0 {
                    let target_opt = cx.session_targets.borrow().get(idx as usize).cloned();
                    if let Some(target) = target_opt {
                        cx.select_chat_target(target);
                    }
                }
            }
        }
    ));

    // Enter 键发送消息（Shift+Enter 换行）
    let key_ctrl = gtk::EventControllerKey::new();
    key_ctrl.connect_key_pressed(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, key, _, state| {
            let is_enter = key == gdk::Key::Return || key == gdk::Key::KP_Enter;
            if is_enter && !state.contains(gdk::ModifierType::SHIFT_MASK) {
                cx.widgets.send_btn.emit_clicked();
                return glib::Propagation::Stop;
            }
            glib::Propagation::Proceed
        }
    ));
    ctx.widgets.input.add_controller(key_ctrl);

    // 发送文本
    ctx.widgets.send_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            let Some(key) = target.key().map(|s| s.to_string()) else {
                cx.toast("请先选择会话、联系人或频道");
                return;
            };
            let buffer = cx.widgets.input.buffer();
            let text = buffer
                .text(&buffer.start_iter(), &buffer.end_iter(), false)
                .to_string();
            let trimmed = text.trim();
            if trimmed.is_empty() {
                return;
            }
            if let Some(query) = trimmed.strip_prefix("/search ") {
                let hits = cx.state.borrow().search_messages(&key, query);
                buffer.set_text("");
                cx.toast(&format!(
                    "搜索“{}”：匹配 {} 条消息",
                    query.trim(),
                    hits.len()
                ));
                return;
            }
            send_text_to_target(&cx, &target, &text);
            buffer.set_text("");
        }
    ));

    // 选择本地图片贴纸发送（SendSticker）
    ctx.widgets.sticker_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
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
                                send_sticker_code(&cx, &path);
                            }
                        }
                    }
                ),
            );
        }
    ));

    // 语音消息快捷发送 (/voice)
    ctx.widgets.voice_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择聊天会话");
                return;
            }
            send_text_to_target(&cx, &target, "/voice 3");
        }
    ));

    // 切换端到端加密（/e2ee）
    ctx.widgets.e2ee_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择聊天会话");
                return;
            }
            let enabled = cx.state.borrow().e2ee_enabled;
            if enabled {
                send_text_to_target(&cx, &target, "/e2ee off");
            } else {
                send_text_to_target(&cx, &target, "/e2ee lanchat-qq-key");
            }
        }
    ));

    // 安全码指纹核验 (/fingerprint)
    ctx.widgets.fingerprint_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择聊天会话");
                return;
            }
            send_text_to_target(&cx, &target, "/fingerprint");
        }
    ));

    // 查找当前会话聊天记录
    ctx.widgets.search_chat_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_search_messages(&cx);
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

    // 右侧抽屉展开/收起 + 传输窗口
    ctx.widgets.header_drawer_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let vis = cx.widgets.right_drawer.is_visible();
            cx.widgets.right_drawer.set_visible(!vis);
        }
    ));
    ctx.widgets.transfers_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.render_transfers();
            let vis = cx.widgets.right_drawer.is_visible();
            cx.widgets.right_drawer.set_visible(!vis);
        }
    ));

    // 顶部频道邀请 & 退出快捷按钮
    ctx.widgets.header_invite_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            if let ChatTarget::Channel(cid) = cx.state.borrow().current_chat.clone() {
                if cid != "*" {
                    invite_member_dialog(&cx, &cid);
                }
            }
        }
    ));
    ctx.widgets.header_leave_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            if let ChatTarget::Channel(cid) = cx.state.borrow().current_chat.clone() {
                if cid != "*" {
                    if let Some(ipc) = cx.ipc.borrow().clone() {
                        let tx = cx.ui_tx.clone();
                        crate::spawn_rt(async move {
                            let r = ipc.channel_leave(&cid).await;
                            let _ = tx.try_send(UiEvent::ChannelLeft(cid, r));
                        });
                    }
                }
            }
        }
    ));

    // 打开频道广场窗口
    ctx.widgets.channels_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.render_channels();
            cx.widgets.channels_win.present();
        }
    ));

    // 新建频道（搜索栏旁 ＋ 按钮 & 频道窗口新建按钮共用）
    ctx.widgets.quick_new_channel_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_new_channel(&cx);
        }
    ));
    ctx.widgets.new_channel_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_new_channel(&cx);
        }
    ));

    // 修改昵称（头像点击 & 底部按钮共用）
    ctx.widgets.self_profile_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_set_nickname(&cx);
        }
    ));
    ctx.widgets.nick_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_set_nickname(&cx);
        }
    ));

    // 版本更新（内置国内加速代理静默下载 · 免浏览器）
    ctx.widgets.update_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            prompt_silent_update(&cx);
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

fn trigger_browse(ctx: &Rc<AppCtx>) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let res = ipc
                .invoke::<()>("BrowseServers", None)
                .await
                .and_then(|v| serde_json::from_value::<Vec<ServerInfo>>(v).map_err(Into::into));
            let _ = tx.try_send(UiEvent::Discovered(res));
        });
    }
}

fn send_text_to_target(ctx: &Rc<AppCtx>, target: &ChatTarget, text: &str) {
    let Some(key) = target.key().map(|s| s.to_string()) else {
        return;
    };
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let (to, group) = if target.is_channel() {
            (String::new(), key.clone())
        } else {
            (key.clone(), String::new())
        };
        let params = SendTextParams {
            to,
            text: text.to_string(),
            group: Some(group.clone()),
        };
        let tx = ctx.ui_tx.clone();
        let owned_text = text.to_string();
        crate::spawn_rt(async move {
            let res = ipc.invoke("SendText", Some(&params)).await.and_then(|v| {
                v.get("msgID")
                    .and_then(|m| m.as_str())
                    .map(|s| s.to_string())
                    .ok_or_else(|| anyhow::anyhow!("响应缺少 msgID"))
            });
            let _ = tx.try_send(UiEvent::TextSent(key, group, owned_text, res));
        });
    }
}

fn send_sticker_code(ctx: &Rc<AppCtx>, sticker: &str) {
    let target = ctx.state.borrow().current_chat.clone();
    let Some(key) = target.key().map(|s| s.to_string()) else {
        ctx.toast("请先选择联系人或频道");
        return;
    };
    let group = if target.is_channel() {
        key.clone()
    } else {
        String::new()
    };
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let sticker_owned = sticker.to_string();
        let params = OfferParams {
            to: key.clone(),
            path: sticker_owned.clone(),
        };
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let res = ipc
                .invoke("SendSticker", Some(&params))
                .await
                .and_then(|v| {
                    v.get("msgID")
                        .and_then(|m| m.as_str())
                        .map(|s| s.to_string())
                        .ok_or_else(|| anyhow::anyhow!("响应缺少 msgID"))
                });
            let _ = tx.try_send(UiEvent::TextSent(key, group, sticker_owned, res));
        });
    }
}

fn prompt_new_channel(ctx: &Rc<AppCtx>) {
    let name_row = adw::EntryRow::new();
    name_row.set_title("频道名称");

    let topic_row = adw::EntryRow::new();
    topic_row.set_title("频道公告 / 主题（可选）");

    let private_row = adw::SwitchRow::new();
    private_row.set_title("私有频道");
    private_row.set_subtitle("仅受邀成员可加入并查看消息");

    let extra = gtk::ListBox::new();
    extra.add_css_class("boxed-list");
    extra.set_selection_mode(gtk::SelectionMode::None);
    extra.append(&name_row);
    extra.append(&topic_row);
    extra.append(&private_row);

    let dialog = adw::AlertDialog::new(Some("新建频道 (G2)"), None);
    dialog.set_extra_child(Some(&extra));
    dialog.add_responses(&[("cancel", "取消"), ("ok", "立即创建")]);
    dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
    dialog.set_close_response("cancel");
    dialog.choose(
        &ctx.widgets.window,
        None::<&gio::Cancellable>,
        clone!(
            #[strong(rename_to = cx)]
            ctx,
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
                            let r = ipc.channel_create(name.trim(), topic.trim(), private).await;
                            let _ = tx.try_send(UiEvent::ChannelCreated(r));
                        });
                    }
                }
            }
        ),
    );
}

fn prompt_set_nickname(ctx: &Rc<AppCtx>) {
    let entry = gtk::Entry::new();
    entry.set_text(&ctx.state.borrow().nickname);
    entry.set_placeholder_text(Some("输入你的局域网展示昵称"));
    let dialog = adw::AlertDialog::new(Some("修改个人昵称"), None);
    dialog.set_extra_child(Some(&entry));
    dialog.add_responses(&[("cancel", "取消"), ("ok", "保存")]);
    dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
    dialog.set_close_response("cancel");
    dialog.choose(
        &ctx.widgets.window,
        None::<&gio::Cancellable>,
        clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[strong]
            entry,
            move |response| {
                if response == "ok" {
                    let name = entry.text().to_string();
                    if let Some(ipc) = cx.ipc.borrow().clone() {
                        let params = NickParams { name };
                        let tx = cx.ui_tx.clone();
                        crate::spawn_rt(async move {
                            let r = ipc.invoke("SetNickname", Some(&params)).await.map(|_| ());
                            let _ = tx.try_send(UiEvent::NickSet(r));
                        });
                    }
                }
            }
        ),
    );
}

fn prompt_silent_update(ctx: &Rc<AppCtx>) {
    let entry = gtk::Entry::new();
    entry.set_placeholder_text(Some(
        "可选自定义代理（留空自动轮询 ghfast.top / gh-proxy.com / ghproxy.net 等）",
    ));
    let dialog = adw::AlertDialog::new(
        Some("🚀 版本更新（国内代理静默下载 · 免浏览器）"),
        Some("自动按优先级轮询国内加速节点（ghfast.top → gh-proxy.com → ghproxy.net → mirror.ghproxy.com → ghps.cc → 直连），直接静默下载安装包并校验 SHA-256。"),
    );
    dialog.set_extra_child(Some(&entry));
    dialog.add_responses(&[
        ("cancel", "取消"),
        ("check", "🔍 仅检查版本"),
        ("download", "⬇️ 静默下载安装包"),
    ]);
    dialog.set_response_appearance("download", adw::ResponseAppearance::Suggested);
    dialog.set_close_response("cancel");
    dialog.choose(
        &ctx.widgets.window,
        None::<&gio::Cancellable>,
        clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[strong]
            entry,
            move |response| {
                if response == "check" || response == "download" {
                    let proxy = entry.text().trim().to_string();
                    let cmd = if response == "check" {
                        "/update check".to_string()
                    } else if proxy.is_empty() {
                        "/update".to_string()
                    } else {
                        format!("/update {proxy}")
                    };
                    cx.toast(if response == "check" {
                        "正在通过国内加速节点检查最新版本…"
                    } else {
                        "正在通过国内加速代理后台静默下载更新包…"
                    });
                    if let Some(ipc) = cx.ipc.borrow().clone() {
                        let params = SendTextParams {
                            to: String::new(),
                            text: cmd.clone(),
                            group: Some(String::new()),
                        };
                        let tx = cx.ui_tx.clone();
                        crate::spawn_rt(async move {
                            let res = ipc.invoke("SendText", Some(&params)).await.and_then(|v| {
                                v.get("msgID")
                                    .and_then(|m| m.as_str())
                                    .map(|s| s.to_string())
                                    .ok_or_else(|| anyhow::anyhow!("响应缺少 msgID"))
                            });
                            let _ = tx.try_send(UiEvent::TextSent(
                                "*".to_string(),
                                "*".to_string(),
                                cmd,
                                res,
                            ));
                        });
                    }
                }
            }
        ),
    );
}

fn prompt_search_messages(ctx: &Rc<AppCtx>) {
    let entry = gtk::Entry::new();
    entry.set_placeholder_text(Some("输入要检索的聊天关键词…"));
    let dialog = adw::AlertDialog::new(Some("查找聊天记录"), None);
    dialog.set_extra_child(Some(&entry));
    dialog.add_responses(&[("cancel", "关闭"), ("ok", "搜索")]);
    dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
    dialog.set_close_response("cancel");
    dialog.choose(
        &ctx.widgets.window,
        None::<&gio::Cancellable>,
        clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[strong]
            entry,
            move |response| {
                if response == "ok" {
                    let q = entry.text().to_string();
                    let conv_key = cx
                        .state
                        .borrow()
                        .current_chat
                        .key()
                        .unwrap_or("")
                        .to_string();
                    let hits = cx.state.borrow().search_messages(&conv_key, &q);
                    cx.toast(&format!(
                        "搜索“{}”：找到 {} 条匹配消息",
                        q.trim(),
                        hits.len()
                    ));
                }
            }
        ),
    );
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
            ctx.set_login_busy(false, "本地守护已就绪，已自动扫描局域网节点");
            ctx.widgets.login_spinner.stop();
            ctx.sync_state();
            trigger_browse(ctx);
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
                if let Some(first) = servers.first() {
                    if ctx.widgets.addr_row.text().trim().is_empty() {
                        ctx.widgets.addr_row.set_text(&server_addr_with_path(first));
                    }
                }
                ctx.state.borrow_mut().discovered = servers;
                ctx.render_discovered();
                if n > 0 {
                    ctx.toast(&format!("发现 {n} 个局域网中心节点"));
                }
            }
            Err(e) => {
                ctx.widgets.login_status.set_text(&format!("发现失败：{e}"));
                ctx.widgets.copy_status_btn.set_visible(true);
                ctx.toast(&format!("发现失败：{e}"));
            }
        },
        UiEvent::Connected(res) => {
            ctx.set_login_busy(false, "");
            match res {
                Ok(()) => {
                    ctx.widgets.copy_status_btn.set_visible(false);
                    ctx.widgets.stack.set_visible_child_name("main");
                    if ctx.state.borrow().current_chat == ChatTarget::None {
                        ctx.select_chat_target(ChatTarget::Channel("*".to_string()));
                    }
                }
                Err(e) => {
                    ctx.widgets
                        .login_status
                        .set_text(&format!("连接请求失败：{e}"));
                    ctx.widgets.copy_status_btn.set_visible(true);
                }
            }
            ctx.sync_state();
        }
        UiEvent::TextSent(key, group, text, res) => match res {
            Ok(msg_id) => {
                let trimmed = text.trim();
                if msg_id == "e2ee:on" {
                    ctx.state.borrow_mut().e2ee_enabled = true;
                    ctx.widgets.e2ee_btn.set_label("🔐 已加密");
                    ctx.toast("已启用端到端加密 (/e2ee)");
                    return;
                }
                if msg_id == "e2ee:off" {
                    ctx.state.borrow_mut().e2ee_enabled = false;
                    ctx.widgets.e2ee_btn.set_label("🔐 加密会话");
                    ctx.toast("已关闭端到端加密");
                    return;
                }
                if let Some(fp) = msg_id.strip_prefix("fp:") {
                    ctx.toast(&format!("🛡️ X25519 安全码：{fp}"));
                    ctx.sync_state();
                    return;
                }
                if let Some(bw) = msg_id.strip_prefix("bwlimit:") {
                    ctx.toast(&format!("⚡ 传输限速已设为：{bw}"));
                    return;
                }
                if let Some(st) = msg_id.strip_prefix("status:") {
                    ctx.toast(&format!("📊 诊断状态：{st}"));
                    ctx.sync_state();
                    return;
                }
                if let Some(up) = msg_id.strip_prefix("update:") {
                    ctx.toast(&format!("🚀 版本更新：{up}"));
                    ctx.sync_state();
                    return;
                }
                if trimmed.starts_with("/react ")
                    || trimmed.starts_with("/edit ")
                    || trimmed.starts_with("/pin ")
                {
                    ctx.sync_state();
                    return;
                }
                if trimmed.starts_with("/revoke ") {
                    ctx.state.borrow_mut().revoke_message(&msg_id);
                } else if trimmed.starts_with("/read ") {
                    let me = ctx.state.borrow().self_id.clone();
                    ctx.state.borrow_mut().mark_read(&msg_id, &me);
                } else {
                    let now = now_secs();
                    let display_text = if trimmed == "/voice" || trimmed.starts_with("/voice ") {
                        let sec = trimmed.trim_start_matches("/voice").trim();
                        let sec = if sec.is_empty() { "3" } else { sec };
                        format!("🎤 [语音消息 {sec}\"]")
                    } else {
                        ctx.state.borrow().format_reply_text(&text)
                    };
                    ctx.state.borrow_mut().push_message(ChatMessage {
                        peer_id: key.clone(),
                        sender_id: String::new(),
                        group,
                        msg_id,
                        text: display_text,
                        inbound: false,
                        revoked: false,
                        read_by: Vec::new(),
                        ts: now,
                    });
                }
                ctx.render_roster();
                if ctx.state.borrow().current_chat.key() == Some(key.as_str()) {
                    ctx.render_messages();
                }
            }
            Err(e) => ctx.toast(&format!("发送失败：{e}")),
        },
        UiEvent::OfferSent(res) => match res {
            Ok(_) => {
                ctx.widgets.right_drawer.set_visible(true);
                ctx.toast("文件传输已发起");
            }
            Err(e) => ctx.toast(&format!("文件发起失败：{e}")),
        },
        UiEvent::ChannelCreated(res) => match res {
            Ok(id) => {
                ctx.toast("频道已创建");
                ctx.render_channels();
                ctx.render_roster();
                open_channel(ctx, &id);
            }
            Err(e) => ctx.toast(&format!("创建频道失败：{e}")),
        },
        UiEvent::ChannelJoined(id, res) => match res {
            Ok(()) => {
                ctx.toast("已加入频道");
                ctx.render_channels();
                ctx.render_roster();
                open_channel(ctx, &id);
            }
            Err(e) => ctx.toast(&format!("加入频道失败：{e}")),
        },
        UiEvent::ChannelInvited(_id, res) => match res {
            Ok(()) => ctx.toast("已发送频道邀请"),
            Err(e) => ctx.toast(&format!("邀请失败：{e}")),
        },
        UiEvent::ChannelLeft(id, res) => match res {
            Ok(()) => {
                ctx.toast("已退出频道");
                if ctx.state.borrow().current_chat.key() == Some(id.as_str()) {
                    ctx.select_chat_target(ChatTarget::Channel("*".to_string()));
                }
                ctx.render_channels();
                ctx.render_roster();
            }
            Err(e) => ctx.toast(&format!("退出频道失败：{e}")),
        },
        UiEvent::Responded(res) => {
            if let Err(e) = res {
                ctx.toast(&format!("应答失败：{e}"));
            }
        }
        UiEvent::Paused(id, res) => match res {
            Ok(()) => {
                if let Some(slot) = ctx
                    .state
                    .borrow_mut()
                    .transfers
                    .iter_mut()
                    .find(|x| x.id == id)
                {
                    slot.state = crate::models::TransferState::Paused;
                    slot.speed_bps = 0;
                }
                ctx.render_transfers();
            }
            Err(e) => ctx.toast(&format!("暂停失败：{e}")),
        },
        UiEvent::Resumed(id, res) => match res {
            Ok(()) => {
                if let Some(slot) = ctx
                    .state
                    .borrow_mut()
                    .transfers
                    .iter_mut()
                    .find(|x| x.id == id)
                {
                    slot.state = crate::models::TransferState::Active;
                }
                ctx.render_transfers();
            }
            Err(e) => ctx.toast(&format!("恢复失败：{e}")),
        },
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
            ctx.widgets
                .conn_badge
                .set_text(&format!("● {}", state.label()));
            match state {
                ConnState::Connected => {
                    ctx.widgets.copy_status_btn.set_visible(false);
                    ctx.widgets.stack.set_visible_child_name("main");
                    if ctx.state.borrow().current_chat == ChatTarget::None {
                        ctx.select_chat_target(ChatTarget::Channel("*".to_string()));
                    }
                    ctx.toast("已登录 LANChat 中心节点");
                    ctx.sync_state();
                }
                ConnState::AuthFailed => {
                    ctx.widgets.login_status.set_text("口令错误，请重新输入");
                    ctx.widgets.copy_status_btn.set_visible(true);
                    ctx.widgets.stack.set_visible_child_name("login");
                    ctx.toast("口令错误");
                }
                ConnState::Disconnected => {
                    ctx.widgets
                        .login_status
                        .set_text(&format!("连接已断开：{reason}"));
                    ctx.widgets.copy_status_btn.set_visible(true);
                    ctx.widgets.stack.set_visible_child_name("login");
                }
                ConnState::Connecting => {
                    ctx.widgets.copy_status_btn.set_visible(false);
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
                    ctx.select_chat_target(ChatTarget::Channel("*".to_string()));
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
            let msg_type = p.get("type").and_then(|v| v.as_str()).unwrap_or("text");
            if msg_type == "MSG_REVOKE" {
                if ctx.state.borrow_mut().revoke_message(&msg_id) {
                    ctx.render_roster();
                    ctx.render_messages();
                }
                return;
            }
            if msg_type == "MSG_READ" {
                if ctx.state.borrow_mut().mark_read(&msg_id, &from) {
                    ctx.render_messages();
                }
                return;
            }
            if msg_type == "MSG_REACT" || msg_type == "MSG_EDIT" || msg_type == "MSG_PIN" {
                let updated_text = p.get("text").and_then(|v| v.as_str()).unwrap_or("");
                if ctx
                    .state
                    .borrow_mut()
                    .update_message_text(&msg_id, updated_text)
                {
                    ctx.render_roster();
                    ctx.render_messages();
                }
                return;
            }
            let text = p
                .get("text")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
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
                revoked: false,
                read_by: Vec::new(),
                ts: now_secs(),
            });
            let is_current = ctx.state.borrow().current_chat.key() == Some(conv_key.as_str());
            if is_current {
                ctx.state.borrow_mut().mark_conv_seen(&conv_key);
                ctx.render_messages();
            } else {
                ctx.toast("收到新消息");
            }
            ctx.render_roster();
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
        "group.matrix" => {
            if let (Some(id), Some(b64)) = (
                p.get("transferId").and_then(|v| v.as_str()),
                p.get("haveBitmap").and_then(|v| v.as_str()),
            ) {
                let size = ctx
                    .state
                    .borrow()
                    .transfers
                    .iter()
                    .find(|t| t.id == id)
                    .map(|t| t.size)
                    .unwrap_or(0);
                if let Some(summary) = format_matrix_summary(b64, size) {
                    ctx.state
                        .borrow_mut()
                        .matrix_summary
                        .insert(id.to_string(), summary);
                    ctx.render_transfers();
                }
            }
        }
        "channel.updated" => {
            if let Some(channels) = parse_channel_updated(p) {
                ctx.state.borrow_mut().channels = channels;
                ctx.render_channels();
                ctx.render_roster();
                ctx.refresh_chat_header();
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

/// 切换到频道会话：更新当前会话、刷新顶部标题栏与消息列表。
fn open_channel(ctx: &Rc<AppCtx>, channel_id: &str) {
    if ctx.state.borrow().channel(channel_id).is_none() && channel_id != "*" {
        return;
    }
    ctx.widgets.roster.unselect_all();
    ctx.select_chat_target(ChatTarget::Channel(channel_id.to_string()));
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
        let av = adw::Avatar::new(32, Some(&peer.nickname), true);
        row.add_prefix(&av);
        if already {
            row.set_sensitive(false);
        }
        list.append(&row);
    }

    let scroll = gtk::ScrolledWindow::new();
    scroll.set_min_content_height(260);
    scroll.set_child(Some(&list));

    let dialog = adw::AlertDialog::new(Some("邀请好友加入频道"), None);
    dialog.set_extra_child(Some(&scroll));
    dialog.add_responses(&[("close", "关闭")]);
    dialog.set_close_response("close");

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
        let dialog = adw::AlertDialog::new(
            Some("收到文件传输请求"),
            Some(&format!(
                "对方发来文件：{}（{}）",
                t.name,
                format_size(t.size)
            )),
        );
        dialog.add_responses(&[("reject", "拒绝"), ("accept", "另存为并接收")]);
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

// ---------- 渲染（Linux QQ 风格） ----------

fn render_all(ctx: &Rc<AppCtx>, was_connected: bool) {
    let nick = {
        let st = ctx.state.borrow();
        if st.nickname.trim().is_empty() {
            "我".to_string()
        } else {
            st.nickname.clone()
        }
    };
    ctx.widgets.self_avatar.set_text(Some(&nick));
    ctx.widgets
        .conn_badge
        .set_text(&format!("● {}", ctx.state.borrow().conn.label()));
    ctx.widgets.me_label.set_text(&format!("👤 {nick}"));

    if ctx.state.borrow().current_chat == ChatTarget::None {
        ctx.state.borrow_mut().current_chat = ChatTarget::Channel("*".to_string());
    }
    ctx.refresh_chat_header();
    ctx.render_roster();
    ctx.render_messages();
    ctx.render_transfers();

    let now_connected = ctx.state.borrow().conn == ConnState::Connected;
    if now_connected {
        if ctx.widgets.stack.visible_child_name().as_deref() != Some("main") {
            ctx.widgets.stack.set_visible_child_name("main");
        }
    } else if was_connected {
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
        if busy {
            self.widgets.login_spinner.start();
        } else {
            self.widgets.login_spinner.stop();
        }
        if !status.is_empty() {
            self.widgets.login_status.set_text(status);
        }
    }

    fn switch_nav_tab(self: &Rc<Self>, tab: NavTab) {
        self.state.borrow_mut().nav_tab = tab;
        self.widgets
            .nav_sessions_btn
            .remove_css_class("qq-rail-active");
        self.widgets
            .nav_contacts_btn
            .remove_css_class("qq-rail-active");
        self.widgets
            .nav_channels_btn
            .remove_css_class("qq-rail-active");
        match tab {
            NavTab::Sessions => self
                .widgets
                .nav_sessions_btn
                .add_css_class("qq-rail-active"),
            NavTab::Contacts => self
                .widgets
                .nav_contacts_btn
                .add_css_class("qq-rail-active"),
            NavTab::Channels => self
                .widgets
                .nav_channels_btn
                .add_css_class("qq-rail-active"),
        }
        self.render_roster();
    }

    fn select_chat_target(self: &Rc<Self>, target: ChatTarget) {
        if let Some(key) = target.key() {
            self.state.borrow_mut().mark_conv_seen(key);
        }
        self.state.borrow_mut().current_chat = target;
        self.refresh_chat_header();
        self.render_roster();
        self.render_messages();
    }

    fn refresh_chat_header(&self) {
        let st = self.state.borrow();
        match &st.current_chat {
            ChatTarget::None => {
                self.widgets.chat_avatar.set_text(Some("LANChat"));
                self.widgets.chat_header.set_text("选择一个会话开始聊天");
                self.widgets
                    .chat_subtitle
                    .set_text("P2P 直连加密传输 · 中心节点信令转发");
                self.widgets.header_invite_btn.set_visible(false);
                self.widgets.header_leave_btn.set_visible(false);
            }
            ChatTarget::Channel(cid) if cid == "*" => {
                self.widgets.chat_avatar.set_text(Some("全员群"));
                self.widgets
                    .chat_header
                    .set_text("📢 局域网全员广播群 (G1)");
                self.widgets.chat_subtitle.set_text(&format!(
                    "● 在线成员 {} 人 · 全员广播与 Swarm P2P 文件块分发",
                    st.peers.len() + 1
                ));
                self.widgets.header_invite_btn.set_visible(false);
                self.widgets.header_leave_btn.set_visible(false);
            }
            ChatTarget::Channel(cid) => {
                if let Some(ch) = st.channel(cid) {
                    self.widgets.chat_avatar.set_text(Some(&ch.name));
                    self.widgets.chat_header.set_text(&channel_header_text(ch));
                    let role = if st.is_channel_owner(cid) {
                        "我是群主"
                    } else {
                        "频道成员"
                    };
                    self.widgets
                        .chat_subtitle
                        .set_text(&format!("● {role} · 支持群内聊天与 Swarm 文件分发"));
                    self.widgets
                        .header_invite_btn
                        .set_visible(st.is_channel_owner(cid));
                    self.widgets
                        .header_leave_btn
                        .set_visible(st.is_channel_member(cid));
                }
            }
            ChatTarget::Peer(pid) => {
                if let Some(peer) = st.peer(pid) {
                    self.widgets.chat_avatar.set_text(Some(&peer.nickname));
                    self.widgets.chat_header.set_text(&peer.nickname);
                    self.widgets.chat_subtitle.set_text(&format!(
                        "● {} · {} · P2P TCP 直连就绪",
                        if peer.os.is_empty() { "LAN" } else { &peer.os },
                        if peer.status.is_empty() {
                            "在线"
                        } else {
                            &peer.status
                        }
                    ));
                } else {
                    self.widgets.chat_avatar.set_text(Some(pid));
                    self.widgets.chat_header.set_text(pid);
                    self.widgets.chat_subtitle.set_text("● 局域网对端节点");
                }
                self.widgets.header_invite_btn.set_visible(false);
                self.widgets.header_leave_btn.set_visible(false);
            }
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
        let st = self.state.borrow();
        if st.discovered.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂未扫到局域网节点，可点击上方「自动发现」或直接连接");
            row.set_sensitive(false);
            self.widgets.server_list.append(&row);
            return;
        }
        for srv in &st.discovered {
            let row = adw::ActionRow::new();
            row.set_title(if srv.name.is_empty() {
                &srv.addr
            } else {
                &srv.name
            });
            row.set_subtitle(&format!(
                "{} · 协议 {}",
                server_addr_with_path(srv),
                srv.version
            ));
            let av = adw::Avatar::new(34, Some(&srv.name), true);
            row.add_prefix(&av);
            let badge = gtk::Label::new(Some(if srv.auth_mode == "none" {
                "🔓 免密开放"
            } else {
                "🔒 PSK 鉴权"
            }));
            badge.add_css_class("caption-heading");
            badge.add_css_class("qq-tag-badge");
            row.add_suffix(&badge);
            self.widgets.server_list.append(&row);
        }
    }

    fn render_roster(&self) {
        clear_listbox(&self.widgets.roster);
        self.session_targets.borrow_mut().clear();

        let st = self.state.borrow();
        let q = st.search_filter.trim().to_lowercase();
        let tab = st.nav_tab;
        let current = st.current_chat.clone();

        // 1. 置顶：📢 局域网全员广播群（在「消息」与「频道」页签展示）
        if matches!(tab, NavTab::Sessions | NavTab::Channels)
            && (q.is_empty() || "局域网全员广播群".contains(&q))
        {
            let preview = st
                .last_message("*")
                .map(|m| m.text.clone())
                .unwrap_or_else(|| "点击向全员发送广播消息或群发文件".to_string());
            let time_str = st
                .last_message("*")
                .map(|m| format_hhmm(m.ts))
                .unwrap_or_else(|| "置顶".to_string());
            let unread = st.unseen_count("*");
            let active = current == ChatTarget::Channel("*".to_string());
            let row = build_qq_session_row(
                "全员",
                "📢 局域网全员广播群",
                &time_str,
                &preview,
                unread,
                active,
            );
            self.widgets.roster.append(&row);
            self.session_targets
                .borrow_mut()
                .push(ChatTarget::Channel("*".to_string()));
        }

        // 2. 自定义频道（在「消息」与「频道」页签展示）
        if matches!(tab, NavTab::Sessions | NavTab::Channels) {
            for ch in &st.channels {
                if !q.is_empty()
                    && !ch.name.to_lowercase().contains(&q)
                    && !ch.topic.to_lowercase().contains(&q)
                {
                    continue;
                }
                let lock = if ch.private { "🔒 " } else { "# " };
                let title = format!("{lock}{}", ch.name);
                let preview = st
                    .last_message(&ch.id)
                    .map(|m| m.text.clone())
                    .unwrap_or_else(|| {
                        if ch.topic.trim().is_empty() {
                            format!("{} 名成员", ch.members.len())
                        } else {
                            ch.topic.clone()
                        }
                    });
                let time_str = st
                    .last_message(&ch.id)
                    .map(|m| format_hhmm(m.ts))
                    .unwrap_or_else(|| format!("{}人", ch.members.len()));
                let unread = st.unseen_count(&ch.id);
                let active = current == ChatTarget::Channel(ch.id.clone());
                let row =
                    build_qq_session_row(&ch.name, &title, &time_str, &preview, unread, active);
                self.widgets.roster.append(&row);
                self.session_targets
                    .borrow_mut()
                    .push(ChatTarget::Channel(ch.id.clone()));
            }
        }

        // 3. 在线联系人（在「消息」与「好友」页签展示）
        if matches!(tab, NavTab::Sessions | NavTab::Contacts) {
            for peer in &st.peers {
                if !q.is_empty()
                    && !peer.nickname.to_lowercase().contains(&q)
                    && !peer.os.to_lowercase().contains(&q)
                {
                    continue;
                }
                let preview = st
                    .last_message(&peer.id)
                    .map(|m| m.text.clone())
                    .unwrap_or_else(|| {
                        format!(
                            "[在线] {} · {}",
                            if peer.os.is_empty() { "LAN" } else { &peer.os },
                            if peer.status.is_empty() {
                                "P2P 直连就绪"
                            } else {
                                &peer.status
                            }
                        )
                    });
                let time_str = st
                    .last_message(&peer.id)
                    .map(|m| format_hhmm(m.ts))
                    .unwrap_or_else(|| peer.os.clone());
                let unread = st.unseen_count(&peer.id);
                let active = current == ChatTarget::Peer(peer.id.clone());
                let row = build_qq_session_row(
                    &peer.nickname,
                    &peer.nickname,
                    &time_str,
                    &preview,
                    unread,
                    active,
                );
                self.widgets.roster.append(&row);
                self.session_targets
                    .borrow_mut()
                    .push(ChatTarget::Peer(peer.id.clone()));
            }
        }

        if self.session_targets.borrow().is_empty() {
            let empty_row = adw::ActionRow::new();
            empty_row.set_title("暂无匹配的联系人或频道");
            empty_row.set_subtitle("可切换上方页签或点击「＋」创建新频道");
            empty_row.set_sensitive(false);
            self.widgets.roster.append(&empty_row);
        }
    }

    fn render_channels(self: &Rc<Self>) {
        if let Some(ipc) = self.ipc.borrow().clone() {
            crate::spawn_rt(async move {
                let _ = ipc.invoke::<()>("ChannelList", None).await;
            });
        }
        clear_listbox(&self.widgets.channels_list);
        let st = self.state.borrow();
        let self_id = st.self_id.clone();
        if st.channels.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂无自定义频道，点击上方「＋ 新建频道」创建");
            row.set_sensitive(false);
            self.widgets.channels_list.append(&row);
            return;
        }
        for ch in &st.channels {
            let row = adw::ActionRow::new();
            row.set_title(&ch.name);
            let av = adw::Avatar::new(36, Some(&ch.name), true);
            row.add_prefix(&av);
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

    fn render_messages(self: &Rc<Self>) {
        clear_listbox(&self.widgets.msg_list);
        let st = self.state.borrow();
        let target = st.current_chat.clone();
        let Some(conv_key) = target.key().map(|s| s.to_string()) else {
            return;
        };
        let self_nick = if st.nickname.trim().is_empty() {
            "我".to_string()
        } else {
            st.nickname.clone()
        };
        let Some(msgs) = st.messages.get(&conv_key) else {
            // 空会话引导气泡
            let hint = gtk::Label::new(Some(
                "💬 暂无消息记录，在下方输入框发送第一条消息或表情贴纸吧！",
            ));
            hint.add_css_class("dim-label");
            hint.add_css_class("caption");
            hint.set_margin_top(28);
            hint.set_margin_bottom(12);
            self.widgets.msg_list.append(&hint);
            return;
        };

        for m in msgs {
            let sender_name = if m.inbound {
                if !m.sender_id.is_empty() {
                    st.peer(&m.sender_id)
                        .map(|p| p.nickname.clone())
                        .unwrap_or_else(|| {
                            m.sender_id
                                .get(0..6)
                                .map(|s| s.to_string())
                                .unwrap_or_else(|| "成员".to_string())
                        })
                } else {
                    st.peer(&m.peer_id)
                        .map(|p| p.nickname.clone())
                        .unwrap_or_else(|| "好友".to_string())
                }
            } else {
                self_nick.clone()
            };

            let read_suffix = if !m.inbound && !m.revoked && !m.read_by.is_empty() {
                if m.read_by.len() > 1 {
                    format!(" · ✓ 已读({})", m.read_by.len())
                } else {
                    " · ✓ 已读".to_string()
                }
            } else {
                String::new()
            };

            let meta_line = gtk::Label::new(Some(&format!(
                "{}  {}{}",
                sender_name,
                format_hhmmss(m.ts),
                read_suffix
            )));
            meta_line.add_css_class("caption");
            meta_line.add_css_class("dim-label");
            meta_line.set_xalign(if m.inbound { 0.0 } else { 1.0 });

            let bubble_card = gtk::Box::new(Orientation::Vertical, 6);
            bubble_card.add_css_class(if m.inbound { "bubble-in" } else { "bubble-out" });

            let (clean_body, reactions) = crate::state::split_reactions(&m.text);

            if !m.revoked {
                if let Some(b64) = extract_inline_image_b64(&clean_body) {
                    let bytes = glib::base64_decode(b64);
                    if !bytes.is_empty() {
                        let gbytes = glib::Bytes::from_owned(bytes);
                        if let Ok(texture) = gdk::Texture::from_bytes(&gbytes) {
                            let pic = gtk::Picture::for_paintable(&texture);
                            pic.set_can_shrink(true);
                            pic.set_content_fit(gtk::ContentFit::Contain);
                            pic.set_size_request(180, 130);
                            bubble_card.append(&pic);
                        }
                    }
                }
            }

            let display_body = if !m.revoked && extract_inline_image_b64(&clean_body).is_some() {
                "🖼️ [图片缩略图]".to_string()
            } else {
                clean_body
            };
            let escaped = glib::markup_escape_text(&display_body).to_string();
            let bubble = gtk::Label::new(None);
            bubble.set_use_markup(true);
            bubble.set_selectable(true);
            bubble.set_label(&escaped);
            bubble.set_wrap(true);
            bubble.set_max_width_chars(56);
            bubble.set_xalign(0.0);
            if m.revoked {
                bubble.add_css_class("dim-label");
            }
            bubble_card.append(&bubble);
            bubble_card.set_halign(if m.inbound { Align::Start } else { Align::End });

            // 右键点击气泡快捷触发引用回复
            if !m.revoked && !m.msg_id.is_empty() {
                let rclick = gtk::GestureClick::new();
                rclick.set_button(3);
                let mid_rc = m.msg_id.clone();
                rclick.connect_pressed(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_, _, _, _| {
                        cx.widgets
                            .input
                            .buffer()
                            .set_text(&format!("/reply {mid_rc} "));
                        cx.widgets.input.grab_focus();
                    }
                ));
                bubble_card.add_controller(rclick);
            }

            let col = gtk::Box::new(Orientation::Vertical, 4);
            col.append(&meta_line);
            col.append(&bubble_card);

            // 气泡下方：已触发的点赞/表情回应胶囊徽章行
            if !m.revoked && !reactions.is_empty() {
                let reactions_box = gtk::Box::new(Orientation::Horizontal, 4);
                reactions_box.set_halign(if m.inbound { Align::Start } else { Align::End });
                for emoji in &reactions {
                    let pill = gtk::Button::with_label(&format!("{emoji} 1"));
                    pill.add_css_class("flat");
                    pill.add_css_class("qq-reaction-pill");
                    pill.set_tooltip_text(Some("已获点赞/表情回应"));
                    if !m.msg_id.is_empty() {
                        let mid_pill = m.msg_id.clone();
                        let tgt_pill = target.clone();
                        let emoji_pill = emoji.clone();
                        pill.connect_clicked(clone!(
                            #[strong(rename_to = cx)]
                            self,
                            move |_| {
                                send_text_to_target(
                                    &cx,
                                    &tgt_pill,
                                    &format!("/react {mid_pill} {emoji_pill}"),
                                );
                            }
                        ));
                    }
                    reactions_box.append(&pill);
                }
                col.append(&reactions_box);
            }

            // 气泡下方快捷操作栏：👍 点赞 / ↩ 回复 / 📌 置顶 / 撤回
            if !m.revoked && !m.msg_id.is_empty() {
                let actions_box = gtk::Box::new(Orientation::Horizontal, 4);
                actions_box.set_halign(if m.inbound { Align::Start } else { Align::End });

                let react_btn = gtk::Button::with_label("👍 点赞");
                react_btn.add_css_class("flat");
                react_btn.add_css_class("qq-msg-action");
                react_btn.set_tooltip_text(Some("在气泡下方添加点赞回应 (/react)"));
                let mid_react = m.msg_id.clone();
                let tgt_react = target.clone();
                react_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        send_text_to_target(&cx, &tgt_react, &format!("/react {mid_react} 👍"));
                    }
                ));
                actions_box.append(&react_btn);

                let reply_btn = gtk::Button::with_label("↩ 回复");
                reply_btn.add_css_class("flat");
                reply_btn.add_css_class("qq-msg-action");
                reply_btn.set_tooltip_text(Some("引用回复此消息"));
                let mid = m.msg_id.clone();
                reply_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        cx.widgets
                            .input
                            .buffer()
                            .set_text(&format!("/reply {mid} "));
                        cx.widgets.input.grab_focus();
                    }
                ));
                actions_box.append(&reply_btn);

                let pin_btn = gtk::Button::with_label("📌 置顶");
                pin_btn.add_css_class("flat");
                pin_btn.add_css_class("qq-msg-action");
                pin_btn.set_tooltip_text(Some("置顶此消息 (/pin)"));
                let mid_pin = m.msg_id.clone();
                let tgt_pin = target.clone();
                pin_btn.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        send_text_to_target(&cx, &tgt_pin, &format!("/pin {mid_pin}"));
                    }
                ));
                actions_box.append(&pin_btn);

                if !m.inbound {
                    let revoke_btn = gtk::Button::with_label("撤回");
                    revoke_btn.add_css_class("flat");
                    revoke_btn.add_css_class("qq-msg-action");
                    revoke_btn.set_tooltip_text(Some("撤回这条消息"));
                    let mid2 = m.msg_id.clone();
                    let tgt = target.clone();
                    revoke_btn.connect_clicked(clone!(
                        #[strong(rename_to = cx)]
                        self,
                        move |_| {
                            send_text_to_target(&cx, &tgt, &format!("/revoke {mid2}"));
                        }
                    ));
                    actions_box.append(&revoke_btn);
                }

                col.append(&actions_box);
            }

            let avatar = adw::Avatar::new(36, Some(&sender_name), true);
            avatar.set_valign(Align::Start);

            let row = gtk::Box::new(Orientation::Horizontal, 10);
            row.set_margin_top(6);
            row.set_margin_bottom(6);
            row.set_margin_start(14);
            row.set_margin_end(14);
            if m.inbound {
                row.set_halign(Align::Start);
                row.append(&avatar);
                row.append(&col);
            } else {
                row.set_halign(Align::End);
                row.append(&col);
                row.append(&avatar);
            }
            self.widgets.msg_list.append(&row);
        }
        drop(st);
        let adj = self.widgets.msg_scroll.vadjustment();
        adj.set_value(adj.upper());
    }

    fn render_transfers(self: &Rc<Self>) {
        self.populate_transfer_listbox(&self.widgets.drawer_transfers_list);
        self.populate_transfer_listbox(&self.widgets.transfers_list);
    }

    fn populate_transfer_listbox(self: &Rc<Self>, target_list: &gtk::ListBox) {
        clear_listbox(target_list);
        let st = self.state.borrow();
        if st.transfers.is_empty() {
            let row = adw::ActionRow::new();
            row.set_title("暂无文件传输任务");
            row.set_subtitle("在聊天框点击「📁 发送文件」即可高速直连传输");
            row.set_sensitive(false);
            target_list.append(&row);
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
            } else {
                path_badge.add_css_class("qq-direct-badge");
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
            if let Some(matrix_text) = st.matrix_summary.get(&t.id) {
                let matrix_badge = gtk::Label::new(Some(matrix_text));
                matrix_badge.add_css_class("caption");
                matrix_badge.add_css_class("dim-label");
                meta.append(&matrix_badge);
            }

            let name_label = gtk::Label::builder()
                .label(if t.kind == crate::models::PathKind::Channel {
                    format!("📄 {} · #{} · {}", t.direction.label(), t.group_id, t.name)
                } else {
                    format!("📄 {} · {}", t.direction.label(), t.name)
                })
                .halign(Align::Start)
                .ellipsize(gtk::pango::EllipsizeMode::Middle)
                .build();

            let body = gtk::Box::new(Orientation::Vertical, 4);
            body.set_margin_top(6);
            body.set_margin_bottom(6);
            body.set_margin_start(8);
            body.set_margin_end(8);
            body.append(&name_label);
            body.append(&bar);
            body.append(&meta);

            let row = adw::ActionRow::new();
            row.set_child(Some(&body));

            if t.state == crate::models::TransferState::Active {
                let pause = gtk::Button::with_label("暂停");
                let id = t.id.clone();
                pause.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        pause_transfer(&cx, &id);
                    }
                ));
                row.add_suffix(&pause);
            } else if t.state == crate::models::TransferState::Paused {
                let resume = gtk::Button::with_label("继续");
                let id = t.id.clone();
                resume.connect_clicked(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_| {
                        resume_transfer(&cx, &id);
                    }
                ));
                row.add_suffix(&resume);
            }

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
                err.set_selectable(true);
                err.add_css_class("error");
                err.add_css_class("caption");
                row.add_suffix(&err);
            }
            target_list.append(&row);
        }
    }
}

/// 构建一行桌面 QQ 风格的会话卡片（左圆形头像 + 上行名称/时间 + 下行消息摘要/红点角标）。
fn build_qq_session_row(
    avatar_seed: &str,
    title: &str,
    time_tag: &str,
    subtitle: &str,
    unread: usize,
    active: bool,
) -> gtk::ListBoxRow {
    let avatar = adw::Avatar::new(42, Some(avatar_seed), true);
    avatar.set_valign(Align::Center);

    let title_lbl = gtk::Label::new(Some(title));
    title_lbl.set_xalign(0.0);
    title_lbl.set_hexpand(true);
    title_lbl.set_ellipsize(gtk::pango::EllipsizeMode::End);
    title_lbl.add_css_class("heading");

    let time_lbl = gtk::Label::new(Some(time_tag));
    time_lbl.set_xalign(1.0);
    time_lbl.add_css_class("caption");
    time_lbl.add_css_class("dim-label");

    let top_row = gtk::Box::new(Orientation::Horizontal, 6);
    top_row.append(&title_lbl);
    top_row.append(&time_lbl);

    let sub_lbl = gtk::Label::new(Some(subtitle));
    sub_lbl.set_xalign(0.0);
    sub_lbl.set_hexpand(true);
    sub_lbl.set_ellipsize(gtk::pango::EllipsizeMode::End);
    sub_lbl.add_css_class("caption");
    sub_lbl.add_css_class("dim-label");

    let bottom_row = gtk::Box::new(Orientation::Horizontal, 6);
    bottom_row.append(&sub_lbl);
    if unread > 0 {
        let badge = gtk::Label::new(Some(&if unread > 99 {
            "99+".to_string()
        } else {
            unread.to_string()
        }));
        badge.add_css_class("qq-unread-badge");
        bottom_row.append(&badge);
    }

    let text_col = gtk::Box::new(Orientation::Vertical, 4);
    text_col.set_hexpand(true);
    text_col.set_valign(Align::Center);
    text_col.append(&top_row);
    text_col.append(&bottom_row);

    let hbox = gtk::Box::new(Orientation::Horizontal, 10);
    hbox.set_margin_top(8);
    hbox.set_margin_bottom(8);
    hbox.set_margin_start(10);
    hbox.set_margin_end(10);
    hbox.append(&avatar);
    hbox.append(&text_col);

    let row = gtk::ListBoxRow::new();
    row.set_child(Some(&hbox));
    row.add_css_class("qq-session-row");
    if active {
        row.add_css_class("qq-session-active");
    }
    row
}

fn pause_transfer(ctx: &Rc<AppCtx>, id: &str) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let transfer_id = id.to_string();
        let params = IdParams {
            transfer_id: transfer_id.clone(),
        };
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let r = ipc.invoke("PauseFile", Some(&params)).await.map(|_| ());
            let _ = tx.try_send(UiEvent::Paused(transfer_id, r));
        });
    }
}

fn resume_transfer(ctx: &Rc<AppCtx>, id: &str) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let transfer_id = id.to_string();
        let params = IdParams {
            transfer_id: transfer_id.clone(),
        };
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let r = ipc.invoke("ResumeFile", Some(&params)).await.map(|_| ());
            let _ = tx.try_send(UiEvent::Resumed(transfer_id, r));
        });
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

/// 从 `data:image/<mime>;base64,<b64>`（或带 `[表情] ` 前缀的文本）中提取内联图片 Base64 数据（M17）。
fn extract_inline_image_b64(text: &str) -> Option<&str> {
    let trimmed = text.trim().strip_prefix("[表情] ").unwrap_or(text.trim());
    if !trimmed.starts_with("data:image/") {
        return None;
    }
    let idx = trimmed.find(";base64,")?;
    let b64 = trimmed[idx + ";base64,".len()..].trim();
    if b64.is_empty() {
        None
    } else {
        Some(b64)
    }
}

fn server_addr_with_path(srv: &ServerInfo) -> String {
    let addr = &srv.addr;
    if addr.contains('/') {
        addr.clone()
    } else {
        format!("{addr}/lctp")
    }
}

fn now_secs() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

fn format_hhmm(ts: i64) -> String {
    if ts <= 0 {
        return String::new();
    }
    let secs = ts.rem_euclid(86400);
    let h = (secs / 3600 + 8) % 24;
    let m = (secs % 3600) / 60;
    format!("{h:02}:{m:02}")
}

fn format_hhmmss(ts: i64) -> String {
    if ts <= 0 {
        return String::new();
    }
    let secs = ts.rem_euclid(86400);
    let h = (secs / 3600 + 8) % 24;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    format!("{h:02}:{m:02}:{s:02}")
}

fn load_css() {
    const CSS: &str = r#"
    .qq-login-bg {
        background: linear-gradient(145deg, alpha(#0099ff, 0.14), alpha(#0066cc, 0.05));
    }
    .qq-login-card {
        background: @card_bg_color;
        border-radius: 18px;
        padding: 24px 28px;
        box-shadow: 0 10px 32px alpha(black, 0.18);
        border: 1px solid alpha(#0099ff, 0.22);
    }
    .qq-login-btn {
        background: linear-gradient(90deg, #0099ff, #0077ee);
        color: white;
        font-weight: 700;
        min-height: 42px;
    }
    .qq-left-rail {
        background: #1b1f2a;
        color: #d8dee9;
        padding-top: 14px;
        padding-bottom: 12px;
        padding-left: 6px;
        padding-right: 6px;
    }
    .qq-rail-btn {
        color: alpha(white, 0.72);
        border-radius: 12px;
        padding: 8px 4px;
        font-size: 0.82em;
    }
    .qq-rail-btn:hover {
        background: alpha(white, 0.10);
        color: white;
    }
    .qq-rail-active {
        background: #0099ff;
        color: white;
        font-weight: 700;
    }
    .qq-rail-sub-btn {
        color: alpha(white, 0.68);
        border-radius: 10px;
        padding: 6px;
    }
    .qq-session-pane {
        background: alpha(@window_bg_color, 0.96);
    }
    .qq-session-row {
        border-radius: 10px;
        margin: 2px 6px;
    }
    .qq-session-active {
        background: alpha(#0099ff, 0.16);
    }
    .qq-unread-badge {
        background: #ff3b30;
        color: white;
        border-radius: 999px;
        padding: 1px 7px;
        font-size: 0.75em;
        font-weight: 700;
    }
    .qq-online-pill {
        color: #22c55e;
        font-size: 0.82em;
        font-weight: 700;
    }
    .qq-plus-btn {
        background: alpha(#0099ff, 0.14);
        color: #0099ff;
        border-radius: 8px;
        font-weight: 700;
    }
    .qq-chat-header {
        background: @headerbar_bg_color;
    }
    .qq-msg-scroll, .messages {
        background: alpha(#8a94a6, 0.07);
    }
    .bubble-in, .bubble-out {
        padding: 10px 14px;
        border-radius: 14px;
    }
    .bubble-in {
        background: @card_bg_color;
        border: 1px solid alpha(#888, 0.16);
    }
    .bubble-out {
        background: linear-gradient(135deg, #0099ff, #0077ee);
        color: white;
    }
    .qq-msg-action {
        font-size: 0.75em;
        opacity: 0.65;
        padding: 2px 6px;
    }
    .qq-msg-action:hover {
        opacity: 1.0;
    }
    .qq-reaction-pill {
        font-size: 0.78em;
        font-weight: 600;
        padding: 1px 8px;
        border-radius: 999px;
        background: alpha(#0099ff, 0.14);
        border: 1px solid alpha(#0099ff, 0.28);
        color: #0099ff;
    }
    .qq-reaction-pill:hover {
        background: alpha(#0099ff, 0.24);
    }
    .qq-input-card {
        background: @card_bg_color;
        border-radius: 10px;
        border: 1px solid alpha(#0099ff, 0.20);
    }
    .qq-input-view {
        background: transparent;
    }
    .qq-send-btn {
        background: #0099ff;
        color: white;
        border-radius: 8px;
        padding: 6px 20px;
        font-weight: 700;
    }
    .qq-direct-badge {
        color: #22c55e;
    }
    .warning { color: #e5a50a; }
    .error { color: #e01b24; }
    .caption { font-size: 0.82em; }
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

    #[test]
    fn default_server_path_is_lctp() {
        let s = ServerInfo {
            addr: "192.168.1.10:19090".into(),
            ..Default::default()
        };
        assert_eq!(server_addr_with_path(&s), "192.168.1.10:19090/lctp");
    }

    #[test]
    fn inline_image_b64_extracted() {
        assert_eq!(
            extract_inline_image_b64("data:image/png;base64,iVBORw=="),
            Some("iVBORw==")
        );
        assert_eq!(
            extract_inline_image_b64("[表情] data:image/jpeg;base64,/9j/4AA="),
            Some("/9j/4AA=")
        );
        assert_eq!(extract_inline_image_b64("普通文本"), None);
    }
}
