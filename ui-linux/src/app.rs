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
use std::cell::{Cell, RefCell};
use std::collections::HashMap;
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
    header_call_btn: gtk::Button,
    header_screen_btn: gtk::Button,
    header_sync_btn: gtk::Button,
    header_invite_btn: gtk::Button,
    header_leave_btn: gtk::Button,
    header_drawer_btn: gtk::Button,
    pinned_bar: gtk::Box,
    pinned_label: gtk::Label,
    call_bar: gtk::Box,
    call_avatar: adw::Avatar,
    call_label: gtk::Label,
    call_mute_btn: gtk::Button,
    call_accept_btn: gtk::Button,
    call_reject_btn: gtk::Button,
    screen_bar: gtk::Box,
    screen_pic: gtk::Picture,
    screen_region_btn: gtk::Button,
    screen_annotate_btn: gtk::Button,
    msg_list: gtk::ListBox,
    msg_scroll: gtk::ScrolledWindow,
    reply_bar: gtk::Box,
    reply_label: gtk::Label,
    reply_cancel_btn: gtk::Button,
    input: gtk::TextView,
    send_btn: gtk::Button,
    sticker_btn: gtk::Button,
    sticker_menu_btn: gtk::MenuButton,
    sticker_popover: gtk::Popover,
    file_btn: gtk::Button,
    folder_btn: gtk::Button,
    clip_btn: gtk::Button,
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
    voice_recorder: Rc<RefCell<Option<(std::process::Child, PathBuf, std::time::Instant)>>>,
    texture_cache: Rc<RefCell<HashMap<String, gdk::Texture>>>,
    updating_roster: Cell<bool>,
    sync_in_flight: Cell<bool>,
    sync_pending: Cell<bool>,
    last_transfer_render_ms: Cell<u128>,
    render_limit: Cell<usize>,
    preserve_scroll_from_bottom: Cell<Option<f64>>,
}

/// 应用启动入口（Application::activate）。
pub fn on_activate(app: &adw::Application) {
    if let Some(existing_win) = app.windows().first() {
        existing_win.set_visible(true);
        existing_win.present();
        return;
    }
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
        voice_recorder: Rc::new(RefCell::new(None)),
        texture_cache: Rc::new(RefCell::new(HashMap::new())),
        updating_roster: Cell::new(false),
        sync_in_flight: Cell::new(false),
        sync_pending: Cell::new(false),
        last_transfer_render_ms: Cell::new(0),
        render_limit: Cell::new(60),
        preserve_scroll_from_bottom: Cell::new(None),
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
        let default_dl = crate::daemon::default_download_dir();
        ensure_daemon(&socket, Some(&default_dl)).await;
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

    let header_call_btn = gtk::Button::with_label("📞 语音通话");
    header_call_btn.add_css_class("flat");
    header_call_btn.set_tooltip_text(Some(
        "发起/挂断局域网实时 UDP 语音通话 (/call / /voiceroom)",
    ));

    let header_screen_btn = gtk::Button::with_label("🖥️ 屏幕投屏");
    header_screen_btn.add_css_class("flat");
    header_screen_btn.set_tooltip_text(Some("开启/停止局域网低延迟 P2P 屏幕投屏 (/screenshare)"));

    let header_sync_btn = gtk::Button::with_label("🔄 漫游同步");
    header_sync_btn.add_css_class("flat");
    header_sync_btn.set_tooltip_text(Some("向对端或频道触发个人多端加密历史漫游增量同步 (/sync)"));

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
    header_bar.append(&header_call_btn);
    header_bar.append(&header_screen_btn);
    header_bar.append(&header_sync_btn);
    header_bar.append(&header_invite_btn);
    header_bar.append(&header_leave_btn);
    header_bar.append(&header_drawer_btn);

    // NTQQ 次顶栏：置顶消息横幅 + 实时语音通话栏 + 实时屏幕共享画布舞台
    let pinned_label = gtk::Label::new(None);
    pinned_label.set_xalign(0.0);
    pinned_label.set_hexpand(true);
    pinned_label.set_ellipsize(gtk::pango::EllipsizeMode::End);
    pinned_label.add_css_class("caption-heading");
    let pinned_bar = gtk::Box::new(Orientation::Horizontal, 8);
    pinned_bar.add_css_class("qq-pinned-banner");
    pinned_bar.set_visible(false);
    pinned_bar.append(&pinned_label);

    let call_avatar = adw::Avatar::new(26, Some("语音来电"), true);
    let call_label = gtk::Label::new(Some("📞 实时低延迟 P2P 语音通话中 (Mesh 混音 · 音量检测)"));
    call_label.set_xalign(0.0);
    call_label.set_hexpand(true);
    call_label.add_css_class("caption-heading");
    let call_mute_btn = gtk::Button::with_label("🎙️ 静音");
    call_mute_btn.add_css_class("flat");
    call_mute_btn.add_css_class("caption");
    call_mute_btn.set_tooltip_text(Some(
        "切换麦克风静音并实时广播至频道成员 (/call toggle-mute)",
    ));
    let call_accept_btn = gtk::Button::with_label("🟢 接听");
    call_accept_btn.add_css_class("suggested-action");
    call_accept_btn.add_css_class("caption");
    call_accept_btn.set_visible(false);
    let call_reject_btn = gtk::Button::with_label("🔴 挂断");
    call_reject_btn.add_css_class("destructive-action");
    call_reject_btn.add_css_class("caption");
    let call_bar = gtk::Box::new(Orientation::Horizontal, 8);
    call_bar.add_css_class("qq-call-banner");
    call_bar.set_visible(false);
    call_bar.append(&call_avatar);
    call_bar.append(&call_label);
    call_bar.append(&call_mute_btn);
    call_bar.append(&call_accept_btn);
    call_bar.append(&call_reject_btn);

    let screen_title = gtk::Label::new(Some(
        "🖥️ 实时屏幕共享舞台 · 8×8 脏矩形自适应帧率 + 激光笔批注",
    ));
    screen_title.set_xalign(0.0);
    screen_title.set_hexpand(true);
    screen_title.add_css_class("caption-heading");
    let screen_region_btn = gtk::Button::with_label("🗔 窗口/裁剪");
    screen_region_btn.add_css_class("flat");
    screen_region_btn.add_css_class("caption");
    screen_region_btn.set_tooltip_text(Some(
        "切换全屏显示器 / 活动窗口区域裁剪 (/screenshare region)",
    ));
    let screen_annotate_btn = gtk::Button::with_label("📍 激光批注");
    screen_annotate_btn.add_css_class("flat");
    screen_annotate_btn.add_css_class("caption");
    let screen_head = gtk::Box::new(Orientation::Horizontal, 8);
    screen_head.append(&screen_title);
    screen_head.append(&screen_region_btn);
    screen_head.append(&screen_annotate_btn);

    let screen_pic = gtk::Picture::new();
    screen_pic.set_can_shrink(true);
    screen_pic.set_content_fit(gtk::ContentFit::Contain);
    screen_pic.set_size_request(-1, 180);

    let screen_bar = gtk::Box::new(Orientation::Vertical, 6);
    screen_bar.add_css_class("qq-screen-stage");
    screen_bar.set_visible(false);
    screen_bar.append(&screen_head);
    screen_bar.append(&screen_pic);

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

    let folder_btn = gtk::Button::with_label("📂 文件夹");
    folder_btn.add_css_class("flat");
    folder_btn.add_css_class("qq-tool-btn");
    folder_btn.set_tooltip_text(Some("选择整个文件夹流式传输并在对端自动还原目录树"));

    let clip_btn = gtk::Button::with_label("📋 剪贴板");
    clip_btn.add_css_class("flat");
    clip_btn.add_css_class("qq-tool-btn");
    clip_btn.set_tooltip_text(Some(
        "一键同步系统剪贴板文本或直发剪贴板图片路径 (/clip / /paste)",
    ));

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

    // QQ 引用回复预览条（仅点击「引用回复」时在输入框上方展示）
    let reply_accent = gtk::Box::new(Orientation::Vertical, 0);
    reply_accent.set_size_request(3, 18);
    reply_accent.add_css_class("qq-reply-accent");

    let reply_label = gtk::Label::new(None);
    reply_label.set_xalign(0.0);
    reply_label.set_hexpand(true);
    reply_label.set_ellipsize(gtk::pango::EllipsizeMode::End);
    reply_label.add_css_class("caption");
    reply_label.add_css_class("qq-reply-text");

    let reply_cancel_btn = gtk::Button::with_label("✕");
    reply_cancel_btn.add_css_class("flat");
    reply_cancel_btn.add_css_class("caption");
    reply_cancel_btn.set_tooltip_text(Some("取消引用回复"));

    let reply_bar = gtk::Box::new(Orientation::Horizontal, 8);
    reply_bar.add_css_class("qq-reply-banner");
    reply_bar.set_visible(false);
    reply_bar.append(&reply_accent);
    reply_bar.append(&reply_label);
    reply_bar.append(&reply_cancel_btn);

    let toolbar_spacer = gtk::Box::new(Orientation::Horizontal, 0);
    toolbar_spacer.set_hexpand(true);

    let shortcut_hint = gtk::Label::new(Some("右键气泡可点赞 / 回复 / 撤回 · Enter 发送"));
    shortcut_hint.add_css_class("caption");
    shortcut_hint.add_css_class("dim-label");

    let composer_toolbar = gtk::Box::new(Orientation::Horizontal, 4);
    composer_toolbar.append(&sticker_menu_btn);
    composer_toolbar.append(&sticker_btn);
    composer_toolbar.append(&file_btn);
    composer_toolbar.append(&folder_btn);
    composer_toolbar.append(&clip_btn);
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

    let send_btn = gtk::Button::with_label("发送");
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
    composer.append(&reply_bar);
    composer.append(&composer_toolbar);
    composer.append(&input_scroll);
    composer.append(&btn_row);

    let chat_col = gtk::Box::new(Orientation::Vertical, 0);
    chat_col.set_hexpand(true);
    chat_col.set_vexpand(true);
    chat_col.add_css_class("qq-chat-col");
    chat_col.append(&header_bar);
    chat_col.append(&gtk::Separator::new(Orientation::Horizontal));
    chat_col.append(&pinned_bar);
    chat_col.append(&call_bar);
    chat_col.append(&screen_bar);
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
        header_call_btn,
        header_screen_btn,
        header_sync_btn,
        header_invite_btn,
        header_leave_btn,
        header_drawer_btn,
        pinned_bar,
        pinned_label,
        call_bar,
        call_avatar,
        call_label,
        call_mute_btn,
        call_accept_btn,
        call_reject_btn,
        screen_bar,
        screen_pic,
        screen_region_btn,
        screen_annotate_btn,
        msg_list,
        msg_scroll,
        reply_bar,
        reply_label,
        reply_cancel_btn,
        input,
        send_btn,
        sticker_btn,
        sticker_menu_btn,
        sticker_popover,
        file_btn,
        folder_btn,
        clip_btn,
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
            cx.widgets
                .login_status
                .set_text("正在扫描局域网与 Tailscale 节点…");
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

    // 搜索框实时过滤（120ms 防抖避免输入卡顿）
    ctx.widgets.search_entry.set_search_delay(120);
    ctx.widgets.search_entry.connect_search_changed(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |entry| {
            let next = entry.text().to_string();
            if cx.state.borrow().search_filter != next {
                cx.state.borrow_mut().search_filter = next;
                cx.render_roster();
            }
        }
    ));

    // 选择左侧列表项 → 切换当前会话（忽略由 render_roster 触发的程序化信号）
    ctx.widgets.roster.connect_row_selected(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, row| {
            if cx.updating_roster.get() {
                return;
            }
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

    // Enter 键发送消息（Shift+Enter 换行）+ Ctrl+V 直接粘贴系统剪贴板截图发送（v7.1.0）
    let key_ctrl = gtk::EventControllerKey::new();
    key_ctrl.connect_key_pressed(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, key, _, state| {
            let is_ctrl_v = state.contains(gdk::ModifierType::CONTROL_MASK)
                && (key == gdk::Key::v || key == gdk::Key::V);
            if is_ctrl_v {
                let target = cx.state.borrow().current_chat.clone();
                if target.key().is_some() {
                    let clipboard = cx.widgets.window.clipboard();
                    glib::spawn_future_local(clone!(
                        #[strong]
                        cx,
                        #[strong]
                        target,
                        async move {
                            if let Ok(Some(texture)) = clipboard.read_texture_future().await {
                                let png_bytes = texture.save_to_png_bytes();
                                let b64 = glib::base64_encode(&png_bytes);
                                send_text_to_target(
                                    &cx,
                                    &target,
                                    &format!("/paste data:image/png;base64,{b64}"),
                                );
                                cx.toast("🖼️ 已从剪贴板粘贴截图并发送");
                            }
                        }
                    ));
                }
            }
            let is_enter = key == gdk::Key::Return || key == gdk::Key::KP_Enter;
            if is_enter && !state.contains(gdk::ModifierType::SHIFT_MASK) {
                cx.widgets.send_btn.emit_clicked();
                return glib::Propagation::Stop;
            }
            glib::Propagation::Proceed
        }
    ));
    ctx.widgets.input.add_controller(key_ctrl);

    // 向上滚动触顶无缝分页加载更早历史消息并保持滚动锚点（v7.1.0）
    ctx.widgets.msg_scroll.connect_edge_reached(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |scroll, pos| {
            if pos != gtk::PositionType::Top {
                return;
            }
            let (conv_opt, before_ts, total_len) = {
                let st = cx.state.borrow();
                let Some(k) = st.current_chat.key().map(|s| s.to_string()) else {
                    return;
                };
                let msgs = st.messages.get(&k);
                let total = msgs.map(|v| v.len()).unwrap_or(0);
                let cur_limit = cx.render_limit.get().max(60);
                let start_idx = total.saturating_sub(cur_limit);
                let earliest_ts = msgs
                    .and_then(|v| v.get(start_idx).or_else(|| v.first()))
                    .map(|m| m.ts)
                    .unwrap_or(0);
                (Some(k), earliest_ts, total)
            };
            let Some(conv_key) = conv_opt else {
                return;
            };
            if total_len == 0 {
                return;
            }
            let adj = scroll.vadjustment();
            let dist_from_bottom = (adj.upper() - adj.value()).max(0.0);
            cx.preserve_scroll_from_bottom.set(Some(dist_from_bottom));
            cx.render_limit
                .set(cx.render_limit.get().max(60).saturating_add(30));
            invoke_get_history_page(&cx, &conv_key, before_ts, 30);
        }
    ));

    // 取消引用回复预览条
    ctx.widgets.reply_cancel_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            cx.state.borrow_mut().reply_target = None;
            cx.widgets.reply_bar.set_visible(false);
            cx.widgets.input.grab_focus();
        }
    ));

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
            let reply_opt = cx.state.borrow_mut().reply_target.take();
            cx.widgets.reply_bar.set_visible(false);
            let payload = match reply_opt {
                Some(rep) if !trimmed.starts_with('/') && !rep.msg_id.is_empty() => {
                    format!("/reply {} {}", rep.msg_id, trimmed)
                }
                _ => text,
            };
            send_text_to_target(&cx, &target, &payload);
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

    // 语音消息录制与发送 (/voice)：后台线程收尾录音，避免阻塞 GTK 主线程
    ctx.widgets.voice_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |btn| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择聊天会话");
                return;
            }
            if let Some((mut child, wav_path, started_at)) = cx.voice_recorder.borrow_mut().take() {
                btn.set_label("🎤 语音");
                let secs = started_at.elapsed().as_secs().clamp(1, 60) as u32;
                cx.toast(&format!("🎤 正在封装并发送 {secs}\" 语音消息…"));
                let ipc_opt = cx.ipc.borrow().clone();
                let tx = cx.ui_tx.clone();
                let key = target.key().unwrap_or("").to_string();
                let is_channel = target.is_channel();
                crate::spawn_rt(async move {
                    let wav_for_stop = wav_path.clone();
                    let _ = tokio::task::spawn_blocking(move || {
                        let _ = std::process::Command::new("kill")
                            .args(["-s", "INT", &child.id().to_string()])
                            .status();
                        std::thread::sleep(std::time::Duration::from_millis(80));
                        let _ = child.kill();
                        let _ = child.wait();
                        let valid_wav = std::fs::metadata(&wav_for_stop)
                            .map(|m| m.len() > 100)
                            .unwrap_or(false);
                        if !valid_wav {
                            let _ = write_fallback_voice_wav(&wav_for_stop, secs.clamp(1, 5));
                        }
                    })
                    .await;
                    if let Some(ipc) = ipc_opt {
                        let text = format!("/voice {}", wav_path.display());
                        let (to, group) = if is_channel {
                            (String::new(), Some(key.clone()))
                        } else {
                            (key.clone(), None)
                        };
                        let params = SendTextParams {
                            to,
                            text: text.clone(),
                            group: group.clone(),
                        };
                        let r = ipc.invoke("SendText", Some(&params)).await.and_then(|v| {
                            v.get("msgID")
                                .and_then(|m| m.as_str())
                                .map(|s| s.to_string())
                                .ok_or_else(|| anyhow::anyhow!("响应缺少 msgID"))
                        });
                        let _ =
                            tx.try_send(UiEvent::TextSent(key, group.unwrap_or_default(), text, r));
                    }
                });
                return;
            }

            let millis = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_millis())
                .unwrap_or(0);
            let wav_path = std::env::temp_dir().join(format!("lanchat-voice-{millis}.wav"));
            match spawn_voice_recorder(&wav_path) {
                Some(child) => {
                    *cx.voice_recorder.borrow_mut() =
                        Some((child, wav_path, std::time::Instant::now()));
                    btn.set_label("🔴 发送语音");
                    cx.toast("🎙️ 正在录音… 再次点击「🔴 发送语音」立即发送");
                }
                None => {
                    let _ = write_fallback_voice_wav(&wav_path, 3);
                    send_text_to_target(&cx, &target, &format!("/voice {}", wav_path.display()));
                    cx.toast("🎤 已发送 3\" 语音消息");
                }
            }
        }
    ));

    // 切换端到端加密（契约 v1.4 显式 SetSessionE2EE RPC）
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
                invoke_set_session_e2ee(&cx, "");
                cx.state.borrow_mut().e2ee_enabled = false;
                cx.widgets.e2ee_btn.set_label("🔐 加密会话");
                cx.toast("已关闭端到端加密");
            } else {
                invoke_set_session_e2ee(&cx, "lanchat-qq-key");
                cx.state.borrow_mut().e2ee_enabled = true;
                cx.widgets.e2ee_btn.set_label("🔐 已加密");
                cx.toast("已启用端到端加密");
            }
        }
    ));

    // 安全码指纹核验 (VerifyPeerFingerprint RPC)
    ctx.widgets.fingerprint_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择聊天会话");
                return;
            }
            invoke_verify_peer_fingerprint(&cx, &target);
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

    // 局域网剪贴板同步与粘贴直发（M29）
    ctx.widgets.clip_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择联系人或频道");
                return;
            }
            let clipboard = cx.widgets.window.clipboard();
            glib::spawn_future_local(clone!(
                #[strong]
                cx,
                #[strong]
                target,
                async move {
                    if let Ok(Some(texture)) = clipboard.read_texture_future().await {
                        let png_bytes = texture.save_to_png_bytes();
                        let b64 = glib::base64_encode(&png_bytes);
                        send_text_to_target(
                            &cx,
                            &target,
                            &format!("/paste data:image/png;base64,{b64}"),
                        );
                        cx.toast("🖼️ 已从剪贴板粘贴截图并发送");
                        return;
                    }
                    if let Ok(Some(text)) = clipboard.read_text_future().await {
                        let trimmed = text.trim();
                        if !trimmed.is_empty() {
                            if trimmed.starts_with("data:image/") {
                                send_text_to_target(&cx, &target, &format!("/paste {}", trimmed));
                                cx.toast("已从剪贴板粘贴并直发图片");
                            } else {
                                send_text_to_target(&cx, &target, &format!("/clip {}", trimmed));
                                cx.toast("已将剪贴板内容加密同步给对端");
                            }
                            return;
                        }
                    }
                    cx.toast("系统剪贴板当前为空");
                }
            ));
        }
    ));

    // 拖拽文件或文件夹到主窗口一键直传（v5.2.0）
    let drop_target = gtk::DropTarget::new(gio::File::static_type(), gtk::gdk::DragAction::COPY);
    drop_target.connect_drop(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, value, _, _| {
            if let Ok(file) = value.get::<gio::File>() {
                if let Some(path) = file.path() {
                    let target = cx.state.borrow().current_chat.clone();
                    if target.key().is_none() {
                        cx.toast("请先选择联系人或频道后再拖入文件/文件夹");
                        return false;
                    }
                    let path_str = path.to_string_lossy().to_string();
                    offer_path_to_target(&cx, &target, path_str.clone());
                    cx.toast(&format!("📁 已发起拖拽直传：{}", path_str));
                    return true;
                }
            }
            false
        }
    ));
    ctx.widgets.window.add_controller(drop_target);

    // 发送整个文件夹（M28 / v5.2.0 流式 .lanchat-dir.tar.gz 直传）
    ctx.widgets.folder_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择联系人或频道");
                return;
            }
            let dialog = gtk::FileDialog::new();
            dialog.select_folder(
                Some(&cx.widgets.window),
                None::<&gio::Cancellable>,
                clone!(
                    #[strong]
                    cx,
                    #[strong]
                    target,
                    move |res| {
                        if let Ok(folder) = res {
                            if let Some(path) = folder.path() {
                                let path_str = path.to_string_lossy().to_string();
                                offer_path_to_target(&cx, &target, path_str.clone());
                                cx.toast(&format!("📁 已发起文件夹流式直传：{}", path_str));
                            }
                        }
                    }
                ),
            );
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

    // 顶部实时语音通话快捷按钮 + 来电响铃横幅「🟢 接听」「🔴 挂断」按钮（v4.9.0）
    ctx.widgets.call_accept_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let (peer_id, group_id) = {
                let st = cx.state.borrow();
                (
                    st.voice_call.peer_id.clone(),
                    st.voice_call.group_id.clone(),
                )
            };
            let target = if !group_id.is_empty() {
                ChatTarget::Channel(group_id)
            } else if !peer_id.is_empty() {
                ChatTarget::Peer(peer_id)
            } else {
                cx.state.borrow().current_chat.clone()
            };
            if target.key().is_some() {
                cx.select_chat_target(target.clone());
                invoke_accept_voice_call(&cx, &target);
                cx.toast("🟢 已接听实时语音通话，建立双向 UDP 音频流");
            }
        }
    ));
    ctx.widgets.call_reject_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let (peer_id, group_id) = {
                let st = cx.state.borrow();
                (
                    st.voice_call.peer_id.clone(),
                    st.voice_call.group_id.clone(),
                )
            };
            let target = if !group_id.is_empty() {
                ChatTarget::Channel(group_id)
            } else if !peer_id.is_empty() {
                ChatTarget::Peer(peer_id)
            } else {
                cx.state.borrow().current_chat.clone()
            };
            if target.key().is_some() {
                invoke_stop_voice_call(&cx, &target);
            }
            cx.widgets.call_bar.set_visible(false);
            cx.widgets.header_call_btn.set_label("📞 语音通话");
            cx.toast("🔴 已挂断实时语音通话");
        }
    ));
    ctx.widgets.header_call_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |btn| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择联系人或频道");
                return;
            }
            let cur_label = btn.label().map(|s| s.to_string()).unwrap_or_default();
            if cur_label.contains("挂断") {
                btn.set_label("📞 语音通话");
                invoke_stop_voice_call(&cx, &target);
            } else {
                btn.set_label("📵 挂断语音");
                invoke_start_voice_call(&cx, &target);
            }
        }
    ));

    // 顶部局域网屏幕投屏与多端漫游同步快捷按钮 (v3.2.0 / 契约 v1.3)
    ctx.widgets.header_screen_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |btn| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择联系人或频道");
                return;
            }
            let cur_label = btn.label().map(|s| s.to_string()).unwrap_or_default();
            if cur_label.contains("停止") {
                btn.set_label("🖥️ 屏幕投屏");
                invoke_stop_screen_share(&cx, &target);
                cx.toast("已停止局域网屏幕投屏");
            } else {
                btn.set_label("⏹ 停止投屏");
                invoke_start_screen_share(&cx, &target, 24);
                cx.toast("🖥️ 已开启 24fps 脏矩形自适应投屏（静止零带宽）");
            }
        }
    ));
    ctx.widgets.call_mute_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |btn| {
            let target = cx.state.borrow().current_chat.clone();
            let cur = btn.label().map(|s| s.to_string()).unwrap_or_default();
            if cur.contains("已静音") {
                btn.set_label("🎙️ 静音");
                send_control_command(&cx, &target, "/call unmute");
                cx.toast("🔊 已取消麦克风静音并通知语音频道");
            } else {
                btn.set_label("🔇 已静音");
                send_control_command(&cx, &target, "/call mute");
                cx.toast("🔇 已开启麦克风静音并实时广播");
            }
        }
    ));
    ctx.widgets.screen_region_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |btn| {
            let target = cx.state.borrow().current_chat.clone();
            let cur = btn.label().map(|s| s.to_string()).unwrap_or_default();
            if cur.contains("全屏") {
                btn.set_label("🗔 窗口/裁剪");
                send_control_command(&cx, &target, "/screenshare region 0,0,0,0");
                cx.toast("🖥️ 已切换为全屏显示器投屏");
            } else {
                btn.set_label("🖥️ 恢复全屏");
                send_control_command(&cx, &target, "/screenshare region 0.05,0.05,0.90,0.90");
                cx.toast("🗔 已启用活动窗口区域裁剪（8×8 脏矩形自适应帧率）");
            }
        }
    ));
    ctx.widgets.header_sync_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                cx.toast("请先选择联系人或频道");
                return;
            }
            invoke_sync_history_with_peer(&cx, &target, 0);
            cx.toast("🔄 已触发个人多端加密历史漫游增量同步");
        }
    ));
    ctx.widgets.screen_annotate_btn.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                return;
            }
            invoke_send_screen_annotation(&cx, &target, 0.5, 0.5, "重点关注此处");
            cx.toast("📍 已发送屏幕共享激光笔坐标批注");
        }
    ));

    // 点击实时投屏画面直接发送归一化坐标激光笔红点批注（v4.8.0 / 契约 v1.3）
    let screen_click = gtk::GestureClick::new();
    screen_click.connect_released(clone!(
        #[strong(rename_to = cx)]
        ctx,
        move |_, _, x, y| {
            let target = cx.state.borrow().current_chat.clone();
            if target.key().is_none() {
                return;
            }
            let w = cx.widgets.screen_pic.width().max(1) as f64;
            let h = cx.widgets.screen_pic.height().max(1) as f64;
            let norm_x = (x / w).clamp(0.0, 1.0);
            let norm_y = (y / h).clamp(0.0, 1.0);
            invoke_send_screen_annotation(&cx, &target, norm_x, norm_y, "🔴 激光笔点击批注");
            cx.toast(&format!("📍 激光笔红点批注 ({norm_x:.2}, {norm_y:.2})"));
        }
    ));
    ctx.widgets.screen_pic.add_controller(screen_click);

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

fn offer_path_to_target(ctx: &Rc<AppCtx>, target: &ChatTarget, path: String) {
    let Some(key) = target.key().map(|s| s.to_string()) else {
        return;
    };
    let is_channel = target.is_channel();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        if is_channel {
            let params = OfferGroupParams { group: key, path };
            crate::spawn_rt(async move {
                let r = ipc
                    .invoke("OfferFileToGroup", Some(&params))
                    .await
                    .and_then(|v| {
                        v.get("transferID")
                            .and_then(|t| t.as_str())
                            .map(|s| s.to_string())
                            .ok_or_else(|| anyhow::anyhow!("响应缺少 transferID"))
                    });
                let _ = tx.try_send(UiEvent::OfferSent(r));
            });
        } else {
            let params = OfferParams { to: key, path };
            crate::spawn_rt(async move {
                let r = ipc.invoke("OfferFile", Some(&params)).await.and_then(|v| {
                    v.get("transferID")
                        .and_then(|t| t.as_str())
                        .map(|s| s.to_string())
                        .ok_or_else(|| anyhow::anyhow!("响应缺少 transferID"))
                });
                let _ = tx.try_send(UiEvent::OfferSent(r));
            });
        }
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

fn resolve_target_to_group(target: &ChatTarget) -> (String, String) {
    let Some(key) = target.key().map(|s| s.to_string()) else {
        return (String::new(), String::new());
    };
    if target.is_channel() {
        (String::new(), key)
    } else {
        (key, String::new())
    }
}

fn invoke_start_voice_call(ctx: &Rc<AppCtx>, target: &ChatTarget) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group });
            let _ = ipc.invoke("StartVoiceCall", Some(&body)).await;
        });
    }
}

fn invoke_accept_voice_call(ctx: &Rc<AppCtx>, target: &ChatTarget) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group, "remoteUDP": "" });
            let _ = ipc.invoke("AcceptVoiceCall", Some(&body)).await;
        });
    }
}

fn invoke_stop_voice_call(ctx: &Rc<AppCtx>, target: &ChatTarget) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group });
            let _ = ipc.invoke("StopVoiceCall", Some(&body)).await;
        });
    }
}

fn invoke_start_screen_share(ctx: &Rc<AppCtx>, target: &ChatTarget, fps: i32) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group, "fps": fps });
            let _ = ipc.invoke("StartScreenShare", Some(&body)).await;
        });
    }
}

fn invoke_stop_screen_share(ctx: &Rc<AppCtx>, target: &ChatTarget) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group });
            let _ = ipc.invoke("StopScreenShare", Some(&body)).await;
        });
    }
}

fn invoke_send_screen_annotation(
    ctx: &Rc<AppCtx>,
    target: &ChatTarget,
    norm_x: f64,
    norm_y: f64,
    note: &str,
) {
    let (to, group) = resolve_target_to_group(target);
    let note_owned = note.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "to": to,
                "group": group,
                "normX": norm_x,
                "normY": norm_y,
                "note": note_owned,
            });
            let _ = ipc.invoke("SendScreenAnnotation", Some(&body)).await;
        });
    }
}

#[allow(dead_code)]
fn invoke_collab_advanced_rpcs(
    ctx: &Rc<AppCtx>,
    target: &ChatTarget,
    dir_path: &str,
    subject: &str,
) {
    let (to, group) = resolve_target_to_group(target);
    let dir_owned = dir_path.to_string();
    let sub_owned = subject.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let sync_body = serde_json::json!({ "to": to, "group": group, "dirPath": dir_owned });
            let _ = ipc.invoke("SyncFolder", Some(&sync_body)).await;
            let issue_body =
                serde_json::json!({ "subject": sub_owned, "org": "LANChat", "ttlSec": 86400 });
            let _ = ipc.invoke("IssueTrustCert", Some(&issue_body)).await;
            let verify_body = serde_json::json!({ "token": sub_owned, "rootPub": "" });
            let _ = ipc.invoke("VerifyTrustCert", Some(&verify_body)).await;
            let _ = ipc.invoke::<()>("RunNetworkDiagnostics", None).await;
        });
    }
}

fn invoke_revoke_message(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str) {
    let (to, group) = resolve_target_to_group(target);
    let mid = msg_id.to_string();
    let key = target.key().unwrap_or("").to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group, "msgID": mid });
            let res = ipc
                .invoke("RevokeMessage", Some(&body))
                .await
                .map(|_| mid.clone());
            let _ = tx.try_send(UiEvent::TextSent(key, group, format!("/revoke {mid}"), res));
        });
    }
}

fn invoke_react_message(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str, emoji: &str) {
    let (to, group) = resolve_target_to_group(target);
    let mid = msg_id.to_string();
    let em = emoji.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "to": to,
                "group": group,
                "msgID": mid,
                "emoji": em,
            });
            let _ = ipc.invoke("ReactMessage", Some(&body)).await;
        });
    }
}

fn invoke_edit_message(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str, new_text: &str) {
    let (to, group) = resolve_target_to_group(target);
    let mid = msg_id.to_string();
    let txt = new_text.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "to": to,
                "group": group,
                "msgID": mid,
                "newText": txt,
            });
            let _ = ipc.invoke("EditMessage", Some(&body)).await;
            let _ = tx.try_send(UiEvent::Reconnected);
        });
    }
}

fn prompt_edit_message(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str, current_body: &str) {
    let entry = gtk::Entry::new();
    entry.set_text(current_body);
    entry.set_placeholder_text(Some("输入修改后的消息内容…"));
    let dialog = adw::AlertDialog::new(Some("✏️ 二次编辑消息"), None);
    dialog.set_extra_child(Some(&entry));
    dialog.add_responses(&[("cancel", "取消"), ("ok", "保存修改")]);
    dialog.set_response_appearance("ok", adw::ResponseAppearance::Suggested);
    dialog.set_close_response("cancel");
    let tgt = target.clone();
    let mid = msg_id.to_string();
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
                    let trimmed = entry.text().trim().to_string();
                    if !trimmed.is_empty() {
                        invoke_edit_message(&cx, &tgt, &mid, &trimmed);
                        cx.toast("✏️ 已同步二次编辑");
                    }
                }
            }
        ),
    );
}

fn invoke_pin_message(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str) {
    let (to, group) = resolve_target_to_group(target);
    let mid = msg_id.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group, "msgID": mid });
            let _ = ipc.invoke("PinMessage", Some(&body)).await;
        });
    }
}

#[allow(dead_code)]
fn invoke_mark_message_read(ctx: &Rc<AppCtx>, target: &ChatTarget, msg_id: &str) {
    let (to, group) = resolve_target_to_group(target);
    let mid = msg_id.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "to": to, "group": group, "msgID": mid });
            let _ = ipc.invoke("MarkMessageRead", Some(&body)).await;
        });
    }
}

fn invoke_set_session_e2ee(ctx: &Rc<AppCtx>, secret: &str) {
    let sec = secret.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "secret": sec });
            let _ = ipc.invoke("SetSessionE2EE", Some(&body)).await;
        });
    }
}

#[allow(dead_code)]
fn invoke_set_auto_clipboard(ctx: &Rc<AppCtx>, enabled: bool) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "enabled": enabled });
            let _ = ipc.invoke("SetAutoClipboard", Some(&body)).await;
        });
    }
}

#[allow(dead_code)]
fn invoke_set_dnd(ctx: &Rc<AppCtx>, enabled: bool) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "enabled": enabled });
            let _ = ipc.invoke("SetDND", Some(&body)).await;
        });
    }
}

fn invoke_get_history_page(ctx: &Rc<AppCtx>, conv_id: &str, before_ts: i64, limit: i32) {
    let cid = conv_id.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "convID": cid,
                "beforeTS": before_ts,
                "limit": limit,
            });
            let _ = ipc.invoke("GetHistoryPage", Some(&body)).await;
            let _ = tx.try_send(UiEvent::Reconnected);
        });
    }
}

fn invoke_verify_peer_fingerprint(ctx: &Rc<AppCtx>, target: &ChatTarget) {
    let Some(key) = target.key().map(|s| s.to_string()) else {
        return;
    };
    let group = if target.is_channel() {
        key.clone()
    } else {
        String::new()
    };
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        let peer_id = key.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "peerID": peer_id });
            let res = ipc
                .invoke("VerifyPeerFingerprint", Some(&body))
                .await
                .and_then(|v| {
                    v.get("fingerprint")
                        .and_then(|f| f.as_str())
                        .map(|s| format!("fp:{s}"))
                        .ok_or_else(|| anyhow::anyhow!("响应缺少 fingerprint"))
                });
            let _ = tx.try_send(UiEvent::TextSent(
                key,
                group,
                "VerifyPeerFingerprint".to_string(),
                res,
            ));
        });
    }
}

#[allow(dead_code)]
fn invoke_send_burn_message(ctx: &Rc<AppCtx>, target: &ChatTarget, text: &str, ttl_sec: i32) {
    let (to, group) = resolve_target_to_group(target);
    let txt = text.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "to": to,
                "group": group,
                "text": txt,
                "ttlSec": ttl_sec,
            });
            let _ = ipc.invoke("SendBurnMessage", Some(&body)).await;
        });
    }
}

fn invoke_sync_history_with_peer(ctx: &Rc<AppCtx>, target: &ChatTarget, since_ts: i64) {
    let (to, group) = resolve_target_to_group(target);
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "to": to,
                "group": group,
                "sinceTS": since_ts,
            });
            let _ = ipc.invoke("SyncHistoryWithPeer", Some(&body)).await;
            let _ = tx.try_send(UiEvent::Reconnected);
        });
    }
}

#[allow(dead_code)]
fn invoke_set_bandwidth_limit(ctx: &Rc<AppCtx>, limit_mbps: f64) {
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        crate::spawn_rt(async move {
            let body = serde_json::json!({ "limitMBps": limit_mbps });
            let _ = ipc.invoke("SetBandwidthLimit", Some(&body)).await;
        });
    }
}

fn invoke_check_silent_update(ctx: &Rc<AppCtx>, proxy: &str, download: bool) {
    let proxy_owned = proxy.to_string();
    if let Some(ipc) = ctx.ipc.borrow().clone() {
        let tx = ctx.ui_tx.clone();
        crate::spawn_rt(async move {
            let body = serde_json::json!({
                "proxy": proxy_owned,
                "download": download,
            });
            let res = ipc
                .invoke("CheckSilentUpdate", Some(&body))
                .await
                .and_then(|v| {
                    v.get("summary")
                        .and_then(|s| s.as_str())
                        .map(|s| {
                            if s.starts_with("update:") {
                                s.to_string()
                            } else {
                                format!("update:{s}")
                            }
                        })
                        .ok_or_else(|| anyhow::anyhow!("响应缺少 summary"))
                });
            let _ = tx.try_send(UiEvent::TextSent(
                "*".to_string(),
                "*".to_string(),
                "CheckSilentUpdate".to_string(),
                res,
            ));
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
                    cx.toast(if response == "check" {
                        "正在通过国内加速节点检查最新版本…"
                    } else {
                        "正在通过国内加速代理后台静默下载更新包…"
                    });
                    invoke_check_silent_update(&cx, &proxy, response == "download");
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
        move |win| {
            // 已连接中心节点时关闭主窗口自动最小化到后台/托盘常驻，保持 daemon 与消息/来电监听在线（v4.9.0）
            if cx.state.borrow().conn == ConnState::Connected {
                win.set_visible(false);
                notify_desktop_if_background(
                    &cx,
                    "LANChat 后台常驻运行中",
                    "主窗口已最小化到后台，收到新消息、文件或语音来电将立即唤醒提醒。",
                    false,
                );
                return glib::Propagation::Stop;
            }
            cx.stop.notify_waiters();
            if let Some(ipc) = cx.ipc.borrow().clone() {
                ipc.shutdown();
            }
            glib::Propagation::Proceed
        }
    ));
}

fn notify_desktop_if_background(ctx: &Rc<AppCtx>, title: &str, body: &str, urgent: bool) {
    if ctx.state.borrow().dnd_enabled {
        return;
    }
    let is_bg = !ctx.widgets.window.is_active() || !ctx.widgets.window.is_visible();
    if !is_bg && !urgent {
        return;
    }
    if let Some(app) = ctx.widgets.window.application() {
        let notif = gio::Notification::new(title);
        notif.set_body(Some(body));
        if urgent {
            notif.set_priority(gio::NotificationPriority::Urgent);
        }
        app.send_notification(Some("lanchat-event"), &notif);
    }
    let title_owned = title.to_string();
    let body_owned = body.to_string();
    std::thread::spawn(move || {
        let urgency = if urgent { "critical" } else { "normal" };
        let _ = std::process::Command::new("notify-send")
            .args(["-a", "LANChat", "-u", urgency, &title_owned, &body_owned])
            .status();
    });
    cx_play_call_ring_async();
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
        UiEvent::State(res) => {
            ctx.sync_in_flight.set(false);
            match res {
                Ok(state) => {
                    let was_connected = ctx.state.borrow().conn == ConnState::Connected;
                    ctx.state.borrow_mut().apply_full_state(state);
                    render_all(ctx, was_connected);
                }
                Err(e) => tracing::warn!("GetState 失败：{e}"),
            }
            if ctx.sync_pending.replace(false) {
                ctx.sync_state();
            }
        }
        UiEvent::Discovered(res) => match res {
            Ok(servers) => {
                let n = servers.len();
                if let Some(first) = servers.first() {
                    let cur_addr = ctx.widgets.addr_row.text();
                    if cur_addr.trim().is_empty() || cur_addr.trim().eq_ignore_ascii_case("auto") {
                        ctx.widgets.addr_row.set_text(&server_addr_with_path(first));
                    }
                }
                ctx.state.borrow_mut().discovered = servers;
                ctx.render_discovered();
                if n > 0 {
                    ctx.widgets.login_status.set_text(&format!(
                        "已自动发现 {n} 个可用局域网中心节点，可直接点击下方按钮登录"
                    ));
                    ctx.toast(&format!("发现 {n} 个局域网中心节点"));
                } else {
                    ctx.widgets
                        .login_status
                        .set_text("暂未发现外部节点，点击下方按钮将自动在本机自举中心节点");
                    ctx.toast("暂未发现外部节点，直接点击连接可自动自举");
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
                if let Some(call_res) = msg_id.strip_prefix("call:") {
                    if call_res == "stopped" || call_res == "idle" {
                        ctx.widgets.call_bar.set_visible(false);
                        ctx.widgets.header_call_btn.set_label("📞 语音通话");
                        ctx.toast("📵 已结束实时语音通话");
                    } else {
                        ctx.widgets.call_bar.set_visible(true);
                        ctx.widgets.call_label.set_text(&format!(
                            "📞 实时低延迟 P2P 语音通话中 ({call_res}) · 麦克风/扬声器已开启"
                        ));
                        ctx.widgets.header_call_btn.set_label("📵 挂断语音");
                        cx_play_call_ring_async();
                        ctx.toast("📞 实时语音通话已启动（PipeWire/ALSA 音频已连接）");
                    }
                    ctx.sync_state();
                    return;
                }
                if msg_id == "dnd:on" || msg_id == "dnd:off" {
                    let on = msg_id == "dnd:on";
                    ctx.state.borrow_mut().dnd_enabled = on;
                    ctx.toast(if on {
                        "🔕 已开启免打扰模式 (/dnd on)"
                    } else {
                        "🔔 已关闭免打扰模式 (/dnd off)"
                    });
                    return;
                }
                if msg_id.starts_with("screenshare:")
                    || msg_id.starts_with("sync:")
                    || msg_id.starts_with("gossip:")
                    || msg_id.starts_with("roomcode:")
                    || msg_id.starts_with("autoclip:")
                {
                    ctx.sync_state();
                    return;
                }
                if trimmed == "/voice" || trimmed.starts_with("/voice ") {
                    ctx.sync_state();
                    return;
                }
                if trimmed.starts_with("/react ")
                    || trimmed.starts_with("/edit ")
                    || trimmed.starts_with("/pin ")
                {
                    return;
                }
                if trimmed.starts_with("/revoke ") {
                    ctx.state.borrow_mut().revoke_message(&msg_id);
                    ctx.render_roster();
                    if ctx.state.borrow().current_chat.key() == Some(key.as_str()) {
                        ctx.render_messages();
                    }
                } else if trimmed.starts_with("/read ") {
                    let me = ctx.state.borrow().self_id.clone();
                    ctx.state.borrow_mut().mark_read(&msg_id, &me);
                    if ctx.state.borrow().current_chat.key() == Some(key.as_str()) {
                        ctx.render_messages();
                    }
                } else {
                    let now = now_secs();
                    let display_text = ctx.state.borrow().format_reply_text(&text);
                    let new_msg = ChatMessage {
                        peer_id: key.clone(),
                        sender_id: String::new(),
                        group,
                        msg_id,
                        text: display_text,
                        inbound: false,
                        revoked: false,
                        read_by: Vec::new(),
                        ts: now,
                        ..Default::default()
                    };
                    ctx.state.borrow_mut().push_message(new_msg.clone());
                    ctx.render_roster();
                    if ctx.state.borrow().current_chat.key() == Some(key.as_str()) {
                        ctx.append_single_message(&new_msg);
                    }
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
        "msg.updated" => {
            if let Some(hm) = p.get("message").and_then(|v| {
                serde_json::from_value::<crate::models::HistoryMessage>(v.clone()).ok()
            }) {
                ctx.state.borrow_mut().apply_history_message(hm);
                ctx.render_roster();
                ctx.render_messages();
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
            if msg_type == "SCREEN_FRAME" {
                if let Some(frame) = p.get("text").and_then(|v| v.as_str()) {
                    update_screen_canvas(ctx, frame);
                }
                return;
            }
            if msg_type == "SCREEN_SHARE" {
                let txt = p.get("text").and_then(|v| v.as_str()).unwrap_or("");
                if txt.contains("已结束") {
                    update_screen_canvas(ctx, "");
                }
            }
            if msg_type == "MSG_REVOKE" || msg_type == "MSG_BURN" {
                if ctx.state.borrow_mut().revoke_message(&msg_id) {
                    ctx.render_roster();
                    ctx.render_messages();
                }
                return;
            }
            if msg_type == "CLIP_SYNC" {
                let clip_text = p.get("text").and_then(|v| v.as_str()).unwrap_or("");
                if !clip_text.is_empty() {
                    let raw_clip = clip_text
                        .strip_prefix("📋 [剪贴板已同步] ")
                        .unwrap_or(clip_text);
                    if let Some(display) = Display::default() {
                        display.clipboard().set_text(raw_clip);
                    }
                    ctx.toast(clip_text);
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
            let new_msg = ChatMessage {
                peer_id: conv_key.clone(),
                sender_id,
                group,
                msg_id,
                text,
                inbound: true,
                revoked: false,
                read_by: Vec::new(),
                ts: now_secs(),
                ..Default::default()
            };
            ctx.state.borrow_mut().push_message(new_msg.clone());
            let is_current = ctx.state.borrow().current_chat.key() == Some(conv_key.as_str());
            let is_focused = ctx.widgets.window.is_active() && ctx.widgets.window.is_visible();
            if is_current && is_focused {
                ctx.state.borrow_mut().mark_conv_seen(&conv_key);
                ctx.append_single_message(&new_msg);
            } else {
                if is_current {
                    ctx.append_single_message(&new_msg);
                } else {
                    ctx.toast("收到新消息");
                }
                let sender_name = ctx
                    .state
                    .borrow()
                    .peer(&from)
                    .map(|p| p.nickname.clone())
                    .unwrap_or_else(|| from.clone());
                let is_mention = new_msg.text.contains('@');
                let notif_title = if is_mention {
                    format!("🔔 [有人@我] {sender_name}")
                } else {
                    format!("💬 新消息 · {sender_name}")
                };
                notify_desktop_if_background(ctx, &notif_title, &new_msg.text, is_mention);
            }
            ctx.render_roster();
        }
        "transfer.progress" => {
            if let Some(t) = p
                .get("transfer")
                .and_then(|v| serde_json::from_value::<Transfer>(v.clone()).ok())
            {
                let is_new_inbound_pending = t.direction
                    == crate::models::TransferDirection::Inbound
                    && t.state == crate::models::TransferState::Pending
                    && !ctx.state.borrow().transfers.iter().any(|x| x.id == t.id);
                let is_active = t.state == crate::models::TransferState::Active;
                let file_name = t.name.clone();
                ctx.state.borrow_mut().upsert_transfer(t);
                if is_new_inbound_pending {
                    notify_desktop_if_background(
                        ctx,
                        "📁 收到文件传输请求",
                        &format!("对方发来文件：{file_name}"),
                        false,
                    );
                }
                let now_ms = now_millis();
                if !is_active || now_ms.saturating_sub(ctx.last_transfer_render_ms.get()) >= 100 {
                    ctx.last_transfer_render_ms.set(now_ms);
                    ctx.render_transfers();
                }
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
        "call.state" => {
            let call_obj = p.get("call").unwrap_or(p);
            let state_str = call_obj
                .get("state")
                .and_then(|v| v.as_str())
                .unwrap_or("idle")
                .to_string();
            let peer_id = call_obj
                .get("peerId")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let group_id = call_obj
                .get("groupId")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let remote_addr = call_obj
                .get("remoteAddr")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            {
                let mut st = ctx.state.borrow_mut();
                st.voice_call.state = state_str.clone();
                st.voice_call.peer_id = peer_id.clone();
                st.voice_call.group_id = group_id;
                st.voice_call.remote_addr = remote_addr;
            }
            update_voice_call_banner(ctx);
        }
        "screen.frame" => {
            let frame = p
                .get("frameData")
                .or_else(|| p.get("frame"))
                .and_then(|v| v.as_str())
                .unwrap_or("");
            update_screen_canvas(ctx, frame);
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

fn update_voice_call_banner(ctx: &Rc<AppCtx>) {
    let (state_str, peer_id, caller_name) = {
        let st = ctx.state.borrow();
        let pid = st.voice_call.peer_id.clone();
        let cname = st
            .peer(&pid)
            .map(|p| p.nickname.clone())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| {
                if pid.is_empty() {
                    "局域网对端".to_string()
                } else {
                    pid.clone()
                }
            });
        (st.voice_call.state.clone(), pid, cname)
    };
    ctx.widgets.call_avatar.set_text(Some(&caller_name));
    if state_str == "ringing" {
        ctx.widgets.call_bar.set_visible(true);
        ctx.widgets.call_accept_btn.set_visible(true);
        ctx.widgets.call_reject_btn.set_visible(true);
        ctx.widgets.call_label.set_text(&format!(
            "🔔 [来电响铃] {caller_name} 邀请你进行实时语音通话"
        ));
        ctx.widgets.header_call_btn.set_label("📵 挂断语音");
        if !ctx.widgets.window.is_visible() {
            ctx.widgets.window.set_visible(true);
            ctx.widgets.window.present();
        }
        notify_desktop_if_background(
            ctx,
            "📞 局域网实时语音来电",
            &format!("{caller_name} ({peer_id}) 发起实时低延迟语音通话"),
            true,
        );
    } else if matches!(state_str.as_str(), "calling" | "active" | "in_call") {
        ctx.widgets.call_bar.set_visible(true);
        ctx.widgets.call_accept_btn.set_visible(false);
        ctx.widgets.call_reject_btn.set_visible(true);
        ctx.widgets.call_label.set_text(&format!(
            "📞 实时低延迟语音通话中 ({state_str}) · {caller_name}"
        ));
        ctx.widgets.header_call_btn.set_label("📵 挂断语音");
    } else {
        ctx.widgets.call_bar.set_visible(false);
        ctx.widgets.call_accept_btn.set_visible(false);
        ctx.widgets.header_call_btn.set_label("📞 语音通话");
    }
}

fn update_screen_canvas(ctx: &Rc<AppCtx>, frame: &str) {
    let trimmed = frame.trim();
    if trimmed.is_empty() {
        ctx.widgets.screen_bar.set_visible(false);
        ctx.widgets.header_screen_btn.set_label("🖥️ 屏幕投屏");
        return;
    }
    if let Some(b64) = extract_inline_image_b64(trimmed) {
        let bytes = glib::base64_decode(b64);
        if !bytes.is_empty() {
            let gbytes = glib::Bytes::from_owned(bytes);
            if let Ok(texture) = gdk::Texture::from_bytes(&gbytes) {
                ctx.widgets.screen_pic.set_paintable(Some(&texture));
                ctx.widgets.screen_bar.set_visible(true);
            }
        }
    }
}

fn render_all(ctx: &Rc<AppCtx>, was_connected: bool) {
    update_voice_call_banner(ctx);
    let screen_frame = {
        let st = ctx.state.borrow();
        if st.screen_share.active && !st.screen_share.latest_frame.is_empty() {
            Some(st.screen_share.latest_frame.clone())
        } else if !st.screen_share.active {
            Some(String::new())
        } else {
            None
        }
    };
    if let Some(f) = screen_frame {
        update_screen_canvas(ctx, &f);
    }
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
        if self.state.borrow().current_chat == target {
            return;
        }
        let had_unseen = target
            .key()
            .map(|k| self.state.borrow().unseen_count(k) > 0)
            .unwrap_or(false);
        if let Some(key) = target.key() {
            self.state.borrow_mut().mark_conv_seen(key);
        }
        {
            let mut st = self.state.borrow_mut();
            st.current_chat = target.clone();
            st.reply_target = None;
        }
        self.render_limit.set(60);
        self.preserve_scroll_from_bottom.set(None);
        self.widgets.reply_bar.set_visible(false);
        self.refresh_chat_header();
        if had_unseen {
            self.render_roster();
        } else {
            self.update_roster_selection_in_place(&target);
        }
        self.render_messages();
    }

    fn update_roster_selection_in_place(&self, target: &ChatTarget) {
        let targets = self.session_targets.borrow();
        let mut idx = 0usize;
        let mut child_opt = self.widgets.roster.first_child();
        while let Some(child) = child_opt {
            if let Some(t) = targets.get(idx) {
                if t == target {
                    child.add_css_class("qq-session-active");
                } else {
                    child.remove_css_class("qq-session-active");
                }
            }
            child_opt = child.next_sibling();
            idx += 1;
        }
    }

    fn refresh_chat_header(&self) {
        let st = self.state.borrow();
        let server_badge = link_mode_badge(&st.server_addr);
        match &st.current_chat {
            ChatTarget::None => {
                self.widgets.chat_avatar.set_text(Some("LANChat"));
                self.widgets.chat_header.set_text("选择一个会话开始聊天");
                self.widgets.chat_subtitle.set_text(&format!(
                    "{server_badge} · P2P 直连加密传输 · 中心节点信令转发"
                ));
                self.widgets.header_invite_btn.set_visible(false);
                self.widgets.header_leave_btn.set_visible(false);
            }
            ChatTarget::Channel(cid) if cid == "*" => {
                self.widgets.chat_avatar.set_text(Some("全员群"));
                self.widgets
                    .chat_header
                    .set_text("📢 局域网全员广播群 (G1)");
                self.widgets.chat_subtitle.set_text(&format!(
                    "● 在线成员 {} 人 · {} · 全员广播与 Swarm P2P 文件块分发",
                    st.peers.len() + 1,
                    server_badge
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
                    self.widgets.chat_subtitle.set_text(&format!(
                        "● {role} · {server_badge} · 支持群内聊天与 Swarm 文件分发"
                    ));
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
                    let peer_badge =
                        if is_tailscale_addr(&peer.status) || is_tailscale_addr(&st.server_addr) {
                            "🌐 Tailscale 直连"
                        } else {
                            "⚡ 局域网直连"
                        };
                    self.widgets.chat_avatar.set_text(Some(&peer.nickname));
                    self.widgets.chat_header.set_text(&peer.nickname);
                    self.widgets.chat_subtitle.set_text(&format!(
                        "● {} · {} · {}",
                        if peer.os.is_empty() { "LAN" } else { &peer.os },
                        if peer.status.is_empty() {
                            "在线"
                        } else {
                            &peer.status
                        },
                        peer_badge
                    ));
                } else {
                    self.widgets.chat_avatar.set_text(Some(pid));
                    self.widgets.chat_header.set_text(pid);
                    self.widgets
                        .chat_subtitle
                        .set_text(&format!("● 对端节点 · {server_badge}"));
                }
                self.widgets.header_invite_btn.set_visible(false);
                self.widgets.header_leave_btn.set_visible(false);
            }
        }
    }

    fn sync_state(&self) {
        if self.sync_in_flight.get() {
            self.sync_pending.set(true);
            return;
        }
        if let Some(ipc) = self.ipc.borrow().clone() {
            self.sync_in_flight.set(true);
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
            row.set_title("暂未扫到局域网或 Tailscale 节点，可点击上方「自动发现」或直接连接");
            row.set_sensitive(false);
            self.widgets.server_list.append(&row);
            return;
        }
        for srv in &st.discovered {
            let row = adw::ActionRow::new();
            let link_badge = link_mode_badge(&srv.addr);
            row.set_title(if srv.name.is_empty() {
                &srv.addr
            } else {
                &srv.name
            });
            row.set_subtitle(&format!(
                "{} · {} · 协议 {}",
                link_badge,
                server_addr_with_path(srv),
                srv.version
            ));
            let av = adw::Avatar::new(34, Some(&srv.name), true);
            row.add_prefix(&av);
            let link_lbl = gtk::Label::new(Some(link_badge));
            link_lbl.add_css_class("caption-heading");
            link_lbl.add_css_class("qq-tag-badge");
            row.add_suffix(&link_lbl);
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
        self.updating_roster.set(true);
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
                .map(|m| format_session_preview(&m.text))
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
                    .map(|m| format_session_preview(&m.text))
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

        // 3. 在线联系人（在「消息」页签按最新消息时间戳倒序排列；在「好友」页签按通讯录排列）
        if matches!(tab, NavTab::Sessions | NavTab::Contacts) {
            let mut sorted_peers: Vec<&crate::models::Peer> = st.peers.iter().collect();
            if tab == NavTab::Sessions {
                sorted_peers.sort_by(|a, b| {
                    let ts_a = st.last_message(&a.id).map(|m| m.ts).unwrap_or(0);
                    let ts_b = st.last_message(&b.id).map(|m| m.ts).unwrap_or(0);
                    ts_b.cmp(&ts_a)
                });
            }
            for peer in sorted_peers {
                if !q.is_empty()
                    && !peer.nickname.to_lowercase().contains(&q)
                    && !peer.os.to_lowercase().contains(&q)
                {
                    continue;
                }
                let preview = st
                    .last_message(&peer.id)
                    .map(|m| format_session_preview(&m.text))
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
        self.updating_roster.set(false);
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
        if let Some(pinned) = st.pinned_message(&conv_key) {
            let (clean_p, _) = crate::state::split_reactions(&pinned.text);
            let (_, p_body) = crate::state::split_quote(&clean_p);
            self.widgets
                .pinned_label
                .set_text(&format!("📌 [置顶] {}", format_session_preview(&p_body)));
            self.widgets.pinned_bar.set_visible(true);
        } else {
            self.widgets.pinned_bar.set_visible(false);
        }
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

        // 动态按需分页渲染消息（默认 60 条，向上滚动触顶或点击加载更多时按页扩展并保持滚动锚点，v7.1.0）
        let max_rendered = self.render_limit.get().max(60);
        let start_idx = msgs.len().saturating_sub(max_rendered);
        let slice: Vec<ChatMessage> = msgs[start_idx..].to_vec();
        let before_ts = slice.first().map(|m| m.ts).unwrap_or(0);
        let conv_key_for_page = conv_key.clone();
        drop(st);

        let load_more_btn =
            gtk::Button::with_label("📜 加载更早历史消息（或向上滑到顶部自动加载）");
        load_more_btn.add_css_class("flat");
        load_more_btn.add_css_class("caption");
        load_more_btn.set_halign(Align::Center);
        load_more_btn.set_margin_top(6);
        load_more_btn.set_margin_bottom(4);
        load_more_btn.connect_clicked(clone!(
            #[strong(rename_to = cx)]
            self,
            move |_| {
                let adj = cx.widgets.msg_scroll.vadjustment();
                cx.preserve_scroll_from_bottom
                    .set(Some((adj.upper() - adj.value()).max(0.0)));
                cx.render_limit
                    .set(cx.render_limit.get().max(60).saturating_add(30));
                invoke_get_history_page(&cx, &conv_key_for_page, before_ts, 30);
                cx.toast("📜 已请求加载更早历史消息");
            }
        ));
        self.widgets.msg_list.append(&load_more_btn);

        for m in &slice {
            let row_widget = self.build_message_row(m, &target, &self_nick);
            self.widgets.msg_list.append(&row_widget);
        }
        let adj = self.widgets.msg_scroll.vadjustment();
        if let Some(dist_from_bottom) = self.preserve_scroll_from_bottom.take() {
            let target_val = (adj.upper() - dist_from_bottom).clamp(adj.lower(), adj.upper());
            adj.set_value(target_val);
        } else {
            adj.set_value(adj.upper());
        }
    }

    fn append_single_message(self: &Rc<Self>, m: &ChatMessage) {
        let (target, self_nick, was_empty_hint) = {
            let st = self.state.borrow();
            let target = st.current_chat.clone();
            let self_nick = if st.nickname.trim().is_empty() {
                "我".to_string()
            } else {
                st.nickname.clone()
            };
            let count = target
                .key()
                .and_then(|k| st.messages.get(k))
                .map(|v| v.len())
                .unwrap_or(0);
            (target, self_nick, count <= 1)
        };
        if was_empty_hint {
            clear_listbox(&self.widgets.msg_list);
        }
        let row_widget = self.build_message_row(m, &target, &self_nick);
        self.widgets.msg_list.append(&row_widget);
        let adj = self.widgets.msg_scroll.vadjustment();
        adj.set_value(adj.upper());
    }

    fn build_message_row(
        self: &Rc<Self>,
        m: &ChatMessage,
        target: &ChatTarget,
        self_nick: &str,
    ) -> gtk::Box {
        let (sender_name, revoked_orig) = {
            let st = self.state.borrow();
            let name = if m.inbound {
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
                self_nick.to_string()
            };
            let orig = st.revoked_originals.get(&m.msg_id).cloned();
            (name, orig)
        };

        // QQ 风格居中灰色撤回提示条（无头像、无聊天气泡；自己撤回时附带蓝色「重新编辑」按钮）
        if m.revoked {
            let revoke_row = gtk::Box::new(Orientation::Horizontal, 6);
            revoke_row.set_halign(Align::Center);
            revoke_row.set_margin_top(4);
            revoke_row.set_margin_bottom(4);
            revoke_row.add_css_class("qq-revoke-pill");

            let notice_text = if m.inbound {
                format!("\"{sender_name}\" 撤回了一条消息")
            } else {
                "你撤回了一条消息".to_string()
            };
            let notice_lbl = gtk::Label::new(Some(&notice_text));
            notice_lbl.add_css_class("caption");
            notice_lbl.add_css_class("dim-label");
            revoke_row.append(&notice_lbl);

            if !m.inbound {
                if let Some(orig) = revoked_orig {
                    if !orig.is_empty() {
                        let reedit_btn = gtk::Button::with_label("重新编辑");
                        reedit_btn.add_css_class("flat");
                        reedit_btn.add_css_class("qq-reedit-btn");
                        reedit_btn.set_tooltip_text(Some("将撤回前的原文恢复到输入框重新编辑"));
                        reedit_btn.connect_clicked(clone!(
                            #[strong(rename_to = cx)]
                            self,
                            move |_| {
                                cx.widgets.input.buffer().set_text(&orig);
                                cx.widgets.input.grab_focus();
                            }
                        ));
                        revoke_row.append(&reedit_btn);
                    }
                }
            }
            return revoke_row;
        }

        let read_suffix = if !m.inbound && !m.read_by.is_empty() {
            if m.read_by.len() > 1 {
                format!(" · ✓ 已读({})", m.read_by.len())
            } else {
                " · ✓ 已读".to_string()
            }
        } else {
            String::new()
        };

        let pin_prefix = if m.pinned || m.text.starts_with("📌 ") {
            "📌 [置顶] · "
        } else {
            ""
        };
        let edit_suffix = if m.edited_at > 0 || m.text.ends_with(" (已编辑)") {
            " · (已编辑)"
        } else {
            ""
        };
        let meta_text = if m.inbound {
            format!(
                "{}{}  {}{}",
                pin_prefix,
                sender_name,
                format_hhmm(m.ts),
                edit_suffix
            )
        } else {
            format!(
                "{}{}{}{}",
                pin_prefix,
                format_hhmm(m.ts),
                read_suffix,
                edit_suffix
            )
        };
        let meta_line = gtk::Label::new(Some(&meta_text));
        meta_line.add_css_class("caption");
        meta_line.add_css_class("dim-label");
        meta_line.set_xalign(if m.inbound { 0.0 } else { 1.0 });

        let bubble_card = gtk::Box::new(Orientation::Vertical, 6);
        bubble_card.add_css_class(if m.inbound { "bubble-in" } else { "bubble-out" });
        bubble_card.set_tooltip_text(Some(
            "右键点击打开消息菜单（点赞 / 回复 / 复制 / 置顶 / 撤回）",
        ));

        let (clean_body, text_reactions) = crate::state::split_reactions(&m.text);
        let reactions: Vec<String> = if !m.reactions.is_empty() {
            m.reactions.keys().cloned().collect()
        } else {
            text_reactions
        };
        let (parsed_quote, body_only) = crate::state::split_quote(&clean_body);
        let quoted_opt = if !m.reply_quote.is_empty() {
            Some(m.reply_quote.clone())
        } else {
            parsed_quote
        };

        // QQ 气泡内嵌引用回复卡片
        if let Some(quoted_str) = &quoted_opt {
            let quote_box = gtk::Box::new(Orientation::Horizontal, 6);
            quote_box.add_css_class(if m.inbound {
                "qq-quote-in"
            } else {
                "qq-quote-out"
            });
            let quote_lbl = gtk::Label::new(Some(quoted_str));
            quote_lbl.set_xalign(0.0);
            quote_lbl.set_wrap(true);
            quote_lbl.set_max_width_chars(48);
            quote_lbl.add_css_class("caption");
            quote_box.append(&quote_lbl);
            bubble_card.append(&quote_box);
        }

        let is_cas_img = !m.media_path.is_empty()
            && (m.media_mime.starts_with("image/")
                || m.media_path.ends_with(".png")
                || m.media_path.ends_with(".jpg")
                || m.media_path.ends_with(".jpeg")
                || m.media_path.ends_with(".gif")
                || m.media_path.ends_with(".webp"));
        let has_inline_img = extract_inline_image_b64(&clean_body).is_some() || is_cas_img;
        if has_inline_img {
            let cache_key = if !m.msg_id.is_empty() {
                m.msg_id.clone()
            } else if !m.media_path.is_empty() {
                format!("cas:{}", m.media_path)
            } else {
                let b64 = extract_inline_image_b64(&clean_body).unwrap_or_default();
                format!("img:{}:{}", b64.len(), &b64[..b64.len().min(32)])
            };
            let tex_opt = {
                if let Some(cached) = self.texture_cache.borrow().get(&cache_key) {
                    Some(cached.clone())
                } else if let Some(b64) = extract_inline_image_b64(&clean_body) {
                    let bytes = glib::base64_decode(b64);
                    if !bytes.is_empty() {
                        let gbytes = glib::Bytes::from_owned(bytes);
                        if let Ok(texture) = gdk::Texture::from_bytes(&gbytes) {
                            self.texture_cache
                                .borrow_mut()
                                .insert(cache_key, texture.clone());
                            Some(texture)
                        } else {
                            None
                        }
                    } else {
                        None
                    }
                } else if is_cas_img {
                    let gfile = gio::File::for_path(&m.media_path);
                    if let Ok(texture) = gdk::Texture::from_file(&gfile) {
                        self.texture_cache
                            .borrow_mut()
                            .insert(cache_key, texture.clone());
                        Some(texture)
                    } else {
                        None
                    }
                } else {
                    None
                }
            };
            if let Some(texture) = tex_opt {
                let pic = gtk::Picture::for_paintable(&texture);
                pic.set_can_shrink(true);
                pic.set_content_fit(gtk::ContentFit::Contain);
                pic.set_size_request(180, 130);
                pic.set_tooltip_text(Some("点击打开原图灯箱预览"));
                let img_click = gtk::GestureClick::new();
                let tex_for_zoom = texture.clone();
                img_click.connect_released(clone!(
                    #[strong(rename_to = cx)]
                    self,
                    move |_, _, _, _| {
                        open_image_lightbox(&cx, &tex_for_zoom);
                    }
                ));
                pic.add_controller(img_click);
                bubble_card.append(&pic);
            }
        }

        let inline_audio_b64 = extract_inline_audio_b64(&body_only).map(|s| s.to_string());
        let is_voice_msg = body_only.contains("🎤 [语音消息")
            || inline_audio_b64.is_some()
            || m.media_mime.starts_with("audio/");
        let display_body = if has_inline_img {
            "🖼️ [图片 · 点击放大预览]".to_string()
        } else if let Some(idx) = body_only.find(" data:audio/") {
            body_only[..idx].to_string()
        } else {
            body_only.clone()
        };
        let escaped = glib::markup_escape_text(&display_body).to_string();
        let bubble = gtk::Label::new(None);
        bubble.set_use_markup(true);
        bubble.set_selectable(true);
        bubble.set_label(&escaped);
        bubble.set_wrap(true);
        bubble.set_max_width_chars(56);
        bubble.set_xalign(0.0);
        bubble_card.append(&bubble);

        // QQ 语音气泡播放按钮 + 波形播放进度条（v7.1.0）：调用本机 PipeWire / PulseAudio / ALSA 播放语音
        if is_voice_msg {
            let voice_row = gtk::Box::new(Orientation::Horizontal, 8);
            let play_btn = gtk::Button::with_label("▶️ 播放语音");
            play_btn.add_css_class("flat");
            play_btn.add_css_class("qq-ctx-btn");
            let wave_bar = gtk::ProgressBar::new();
            wave_bar.set_fraction(0.0);
            wave_bar.set_text(Some("▂▄▆█▄▂▆█▃▅▂"));
            wave_bar.set_show_text(true);
            wave_bar.set_valign(Align::Center);
            wave_bar.set_size_request(128, -1);
            let audio_b64 = inline_audio_b64.clone();
            let msg_id_for_play = m.msg_id.clone();
            let wave_weak = wave_bar.downgrade();
            play_btn.connect_clicked(clone!(
                #[strong(rename_to = cx)]
                self,
                move |btn| {
                    btn.set_label("🔊 播放中…");
                    play_voice_message(&cx, &msg_id_for_play, audio_b64.as_deref());
                    let btn_weak = btn.downgrade();
                    let w_weak = wave_weak.clone();
                    let step = Rc::new(Cell::new(0u32));
                    glib::timeout_add_local(std::time::Duration::from_millis(160), move || {
                        let cur = step.get() + 1;
                        step.set(cur);
                        if let Some(wb) = w_weak.upgrade() {
                            wb.set_fraction((cur as f64 / 10.0).clamp(0.0, 1.0));
                        }
                        if cur >= 10 {
                            if let Some(wb) = w_weak.upgrade() {
                                wb.set_fraction(0.0);
                            }
                            if let Some(b) = btn_weak.upgrade() {
                                b.set_label("▶️ 播放语音");
                            }
                            glib::ControlFlow::Break
                        } else {
                            glib::ControlFlow::Continue
                        }
                    });
                }
            ));
            voice_row.append(&play_btn);
            voice_row.append(&wave_bar);
            bubble_card.append(&voice_row);
        }

        // NTQQ 暗色等宽代码块卡片（支持 ```lang ... ```）
        if !has_inline_img && body_only.contains("```") {
            if let (Some(first), Some(last)) = (body_only.find("```"), body_only.rfind("```")) {
                if last > first + 3 {
                    let inner = &body_only[first + 3..last];
                    let code_str = inner
                        .find('\n')
                        .map(|nl| &inner[nl + 1..])
                        .unwrap_or(inner)
                        .trim();
                    if !code_str.is_empty() {
                        let code_lbl = gtk::Label::new(Some(code_str));
                        code_lbl.set_xalign(0.0);
                        code_lbl.set_selectable(true);
                        code_lbl.set_wrap(true);
                        code_lbl.add_css_class("monospace");
                        code_lbl.add_css_class("qq-code-card");
                        bubble_card.append(&code_lbl);
                    }
                }
            }
        }

        bubble_card.set_halign(if m.inbound { Align::Start } else { Align::End });

        // 右键点击气泡按需懒加载弹出 QQ 风格操作菜单（避免每条消息预先创建 Popover + 11 个按钮并防止 GDK surface 泄漏）
        if !m.msg_id.is_empty() {
            let rclick = gtk::GestureClick::new();
            rclick.set_button(3);
            let msg_clone = m.clone();
            let target_clone = target.clone();
            let sender_for_reply = sender_name.clone();
            let body_for_reply = display_body.clone();
            let bubble_weak = bubble_card.downgrade();
            rclick.connect_pressed(clone!(
                #[strong(rename_to = cx)]
                self,
                move |_, _, _, _| {
                    let Some(parent_box) = bubble_weak.upgrade() else {
                        return;
                    };
                    show_message_context_popover(
                        &cx,
                        &parent_box,
                        &msg_clone,
                        &target_clone,
                        &sender_for_reply,
                        &body_for_reply,
                    );
                }
            ));
            bubble_card.add_controller(rclick);
        }

        let col = gtk::Box::new(Orientation::Vertical, 4);
        col.append(&meta_line);
        col.append(&bubble_card);

        // 气泡下方：已触发的点赞/表情回应胶囊徽章行（仅在有人点赞时显示）
        if !reactions.is_empty() {
            let reactions_box = gtk::Box::new(Orientation::Horizontal, 4);
            reactions_box.set_halign(if m.inbound { Align::Start } else { Align::End });
            for emoji in &reactions {
                let pill = gtk::Button::with_label(&format!("{emoji} 1"));
                pill.add_css_class("flat");
                pill.add_css_class("qq-reaction-pill");
                pill.set_tooltip_text(Some("点击追加点赞回应"));
                if !m.msg_id.is_empty() {
                    let mid_pill = m.msg_id.clone();
                    let tgt_pill = target.clone();
                    let emoji_pill = emoji.clone();
                    pill.connect_clicked(clone!(
                        #[strong(rename_to = cx)]
                        self,
                        move |_| {
                            invoke_react_message(&cx, &tgt_pill, &mid_pill, &emoji_pill);
                        }
                    ));
                }
                reactions_box.append(&pill);
            }
            col.append(&reactions_box);
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
        row
    }

    fn render_transfers(self: &Rc<Self>) {
        if self.widgets.right_drawer.is_visible() {
            self.populate_transfer_listbox(&self.widgets.drawer_transfers_list);
        }
        if self.widgets.transfers_win.is_visible() {
            self.populate_transfer_listbox(&self.widgets.transfers_list);
        }
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

            let is_dir_bundle = t.name.ends_with(".lanchat-dir.tar.gz") || t.name.ends_with(".zip");
            let icon_prefix = if is_dir_bundle {
                "📁 文件夹"
            } else {
                "📄"
            };
            let name_label = gtk::Label::builder()
                .label(if t.kind == crate::models::PathKind::Channel {
                    format!(
                        "{} {} · #{} · {}",
                        icon_prefix,
                        t.direction.label(),
                        t.group_id,
                        t.name
                    )
                } else {
                    format!("{} {} · {}", icon_prefix, t.direction.label(), t.name)
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

/// 从 `🎤 [语音消息 N"] data:audio/<mime>;base64,<b64>` 中提取内联音频 Base64 数据。
fn extract_inline_audio_b64(text: &str) -> Option<&str> {
    let pos = text.find("data:audio/")?;
    let sub = &text[pos..];
    let idx = sub.find(";base64,")?;
    let b64 = sub[idx + ";base64,".len()..].trim();
    if b64.is_empty() {
        None
    } else {
        Some(b64)
    }
}

/// 启动 Linux 本地麦克风录音子进程（优先 PipeWire `pw-record`，回退 ALSA `arecord` / `ffmpeg`）。
fn spawn_voice_recorder(wav_path: &std::path::Path) -> Option<std::process::Child> {
    let out = wav_path.to_string_lossy().to_string();
    let candidates: &[(&str, &[&str])] = &[
        (
            "pw-record",
            &["--rate=16000", "--channels=1", "--format=s16", &out],
        ),
        (
            "arecord",
            &[
                "-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "wav", &out,
            ],
        ),
        (
            "ffmpeg",
            &[
                "-y",
                "-loglevel",
                "quiet",
                "-f",
                "pulse",
                "-i",
                "default",
                "-ar",
                "16000",
                "-ac",
                "1",
                &out,
            ],
        ),
    ];
    for (bin, args) in candidates {
        if let Ok(child) = std::process::Command::new(bin)
            .args(*args)
            .stdin(std::process::Stdio::null())
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .spawn()
        {
            return Some(child);
        }
    }
    None
}

/// 按需构建并弹出右键气泡菜单（关闭后自动 unparent 释放 GDK Popover 资源）。
fn show_message_context_popover(
    ctx: &Rc<AppCtx>,
    parent_box: &gtk::Box,
    m: &ChatMessage,
    target: &ChatTarget,
    sender_for_reply: &str,
    body_for_reply: &str,
) {
    let ctx_pop = gtk::Popover::new();
    ctx_pop.set_has_arrow(true);
    ctx_pop.set_autohide(true);
    let menu_col = gtk::Box::new(Orientation::Vertical, 2);
    menu_col.set_margin_top(4);
    menu_col.set_margin_bottom(4);
    menu_col.set_margin_start(4);
    menu_col.set_margin_end(4);

    let quick_emoji_row = gtk::Box::new(Orientation::Horizontal, 2);
    for quick_emoji in ["👍", "❤️", "😂", "🔥", "🎉", "🚀"] {
        let mid_q = m.msg_id.clone();
        let tgt_q = target.clone();
        let em_q = quick_emoji.to_string();
        let q_btn = gtk::Button::with_label(quick_emoji);
        q_btn.add_css_class("flat");
        q_btn.connect_clicked(clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[weak]
            ctx_pop,
            move |_| {
                ctx_pop.popdown();
                invoke_react_message(&cx, &tgt_q, &mid_q, &em_q);
            }
        ));
        quick_emoji_row.append(&q_btn);
    }
    menu_col.append(&quick_emoji_row);

    let mid_react = m.msg_id.clone();
    let tgt_react = target.clone();
    let btn_react = gtk::Button::with_label("👍  点赞回应");
    btn_react.add_css_class("flat");
    btn_react.add_css_class("qq-ctx-btn");
    btn_react.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        #[weak]
        ctx_pop,
        move |_| {
            ctx_pop.popdown();
            invoke_react_message(&cx, &tgt_react, &mid_react, "👍");
        }
    ));
    menu_col.append(&btn_react);

    let msg_clone = m.clone();
    let sender_str = sender_for_reply.to_string();
    let body_str = body_for_reply.to_string();
    let btn_reply = gtk::Button::with_label("💬  引用回复");
    btn_reply.add_css_class("flat");
    btn_reply.add_css_class("qq-ctx-btn");
    btn_reply.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        #[weak]
        ctx_pop,
        move |_| {
            ctx_pop.popdown();
            cx.state.borrow_mut().reply_target = Some(msg_clone.clone());
            cx.widgets
                .reply_label
                .set_text(&format!("回复 {}：{}", sender_str, body_str));
            cx.widgets.reply_bar.set_visible(true);
            cx.widgets.input.grab_focus();
        }
    ));
    menu_col.append(&btn_reply);

    let copy_str = body_for_reply.to_string();
    let btn_copy = gtk::Button::with_label("📋  复制文本");
    btn_copy.add_css_class("flat");
    btn_copy.add_css_class("qq-ctx-btn");
    btn_copy.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        #[weak]
        ctx_pop,
        move |_| {
            ctx_pop.popdown();
            if let Some(display) = Display::default() {
                display.clipboard().set_text(&copy_str);
                cx.toast("已复制消息内容");
            }
        }
    ));
    menu_col.append(&btn_copy);

    let mid_pin = m.msg_id.clone();
    let tgt_pin = target.clone();
    let btn_pin = gtk::Button::with_label("📌  置顶消息");
    btn_pin.add_css_class("flat");
    btn_pin.add_css_class("qq-ctx-btn");
    btn_pin.connect_clicked(clone!(
        #[strong(rename_to = cx)]
        ctx,
        #[weak]
        ctx_pop,
        move |_| {
            ctx_pop.popdown();
            invoke_pin_message(&cx, &tgt_pin, &mid_pin);
        }
    ));
    menu_col.append(&btn_pin);

    if !m.inbound {
        let mid_edit = m.msg_id.clone();
        let tgt_edit = target.clone();
        let body_edit = body_for_reply.to_string();
        let btn_edit = gtk::Button::with_label("✏️  编辑消息");
        btn_edit.add_css_class("flat");
        btn_edit.add_css_class("qq-ctx-btn");
        btn_edit.connect_clicked(clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[weak]
            ctx_pop,
            move |_| {
                ctx_pop.popdown();
                prompt_edit_message(&cx, &tgt_edit, &mid_edit, &body_edit);
            }
        ));
        menu_col.append(&btn_edit);

        let mid_rev = m.msg_id.clone();
        let tgt_rev = target.clone();
        let btn_revoke = gtk::Button::with_label("↩️  撤回消息");
        btn_revoke.add_css_class("flat");
        btn_revoke.add_css_class("qq-ctx-btn");
        btn_revoke.connect_clicked(clone!(
            #[strong(rename_to = cx)]
            ctx,
            #[weak]
            ctx_pop,
            move |_| {
                ctx_pop.popdown();
                invoke_revoke_message(&cx, &tgt_rev, &mid_rev);
            }
        ));
        menu_col.append(&btn_revoke);
    }

    ctx_pop.set_child(Some(&menu_col));
    ctx_pop.set_parent(parent_box);
    ctx_pop.connect_closed(|pop| {
        pop.unparent();
    });
    ctx_pop.popup();
}

/// 格式化会话列表预览文案：剥离内联图片/音频 Base64 载荷并限制长度，避免 Pango 排版超大字符串卡顿。
fn format_session_preview(text: &str) -> String {
    let trimmed = text.trim();
    if trimmed.contains("data:image/") {
        return "🖼️ [图片]".to_string();
    }
    let clean = if let Some(idx) = trimmed.find(" data:audio/") {
        &trimmed[..idx]
    } else {
        trimmed
    };
    let single_line = clean.lines().next().unwrap_or(clean);
    let mut out: String = single_line.chars().take(64).collect();
    if single_line.chars().count() > 64 {
        out.push('…');
    }
    out
}

fn now_millis() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or(0)
}

/// 生成一段 16kHz 16-bit 单声道双音提示 WAV（当无物理麦克风或播放无内联音频的语音气泡时兜底）。
fn write_fallback_voice_wav(wav_path: &std::path::Path, duration_secs: u32) -> std::io::Result<()> {
    let sample_rate = 16000u32;
    let secs = duration_secs.clamp(1, 5);
    let num_samples = sample_rate * secs;
    let data_bytes = num_samples * 2;
    let mut buf = Vec::with_capacity(44 + data_bytes as usize);
    buf.extend_from_slice(b"RIFF");
    buf.extend_from_slice(&(36 + data_bytes).to_le_bytes());
    buf.extend_from_slice(b"WAVEfmt ");
    buf.extend_from_slice(&16u32.to_le_bytes());
    buf.extend_from_slice(&1u16.to_le_bytes()); // PCM
    buf.extend_from_slice(&1u16.to_le_bytes()); // 1 channel
    buf.extend_from_slice(&sample_rate.to_le_bytes());
    buf.extend_from_slice(&(sample_rate * 2).to_le_bytes());
    buf.extend_from_slice(&2u16.to_le_bytes());
    buf.extend_from_slice(&16u16.to_le_bytes());
    buf.extend_from_slice(b"data");
    buf.extend_from_slice(&data_bytes.to_le_bytes());
    for i in 0..num_samples {
        let t = i as f32 / sample_rate as f32;
        let freq = if (i / (sample_rate / 2)).is_multiple_of(2) {
            523.25_f32
        } else {
            659.25_f32
        };
        let env = (1.0 - (t / secs as f32)).max(0.15);
        let sample = ((t * freq * std::f32::consts::TAU).sin() * 9000.0 * env) as i16;
        buf.extend_from_slice(&sample.to_le_bytes());
    }
    std::fs::write(wav_path, buf)
}

/// 异步调用系统音频播放器（`pw-play` / `paplay` / `aplay`）播放指定 WAV 文件。
fn play_wav_file_async(wav_path: PathBuf) {
    std::thread::spawn(move || {
        let path_str = wav_path.to_string_lossy().to_string();
        for (bin, args) in [
            ("pw-play", vec![path_str.as_str()]),
            ("paplay", vec![path_str.as_str()]),
            ("aplay", vec!["-q", path_str.as_str()]),
            (
                "ffplay",
                vec![
                    "-nodisp",
                    "-autoexit",
                    "-loglevel",
                    "quiet",
                    path_str.as_str(),
                ],
            ),
        ] {
            if let Ok(status) = std::process::Command::new(bin)
                .args(&args)
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
            {
                if status.success() {
                    break;
                }
            }
        }
    });
}

/// 发起实时语音通话时的呼叫提示音。
fn cx_play_call_ring_async() {
    let ring_path = std::env::temp_dir().join("lanchat-call-ring.wav");
    if write_fallback_voice_wav(&ring_path, 1).is_ok() {
        play_wav_file_async(ring_path);
    }
}

/// 播放语音气泡（若有内联 base64 音频则解码播放，否则合成语音提示音播放）。
fn play_voice_message(ctx: &Rc<AppCtx>, msg_id: &str, audio_b64: Option<&str>) {
    let safe_id: String = msg_id
        .chars()
        .filter(|c| c.is_ascii_alphanumeric() || *c == '-' || *c == '_')
        .collect();
    let wav_path = std::env::temp_dir().join(format!(
        "lanchat-play-{}.wav",
        if safe_id.is_empty() {
            "voice"
        } else {
            &safe_id
        }
    ));
    let mut wrote = false;
    if let Some(b64) = audio_b64 {
        let raw = glib::base64_decode(b64);
        if raw.len() > 44 && std::fs::write(&wav_path, &raw).is_ok() {
            wrote = true;
        }
    }
    if !wrote {
        let _ = write_fallback_voice_wav(&wav_path, 2);
    }
    ctx.toast("🔊 正在播放语音消息…");
    play_wav_file_async(wav_path);
}

/// 打开 CAS 富媒体原图灯箱预览窗口（v7.1.0）。
fn open_image_lightbox(ctx: &Rc<AppCtx>, texture: &gdk::Texture) {
    let pic = gtk::Picture::for_paintable(texture);
    pic.set_can_shrink(true);
    pic.set_content_fit(gtk::ContentFit::Contain);
    pic.set_hexpand(true);
    pic.set_vexpand(true);

    let win = gtk::Window::builder()
        .title("🖼️ 原图灯箱预览")
        .transient_for(&ctx.widgets.window)
        .modal(true)
        .default_width(760)
        .default_height(540)
        .child(&pic)
        .build();
    win.present();
}

fn server_addr_with_path(srv: &ServerInfo) -> String {
    let addr = &srv.addr;
    if addr.contains('/') {
        addr.clone()
    } else {
        format!("{addr}/lctp")
    }
}

fn is_tailscale_addr(raw: &str) -> bool {
    let mut s = raw.trim();
    for p in ["ws://", "wss://", "http://", "https://"] {
        if let Some(rest) = s.strip_prefix(p) {
            s = rest;
            break;
        }
    }
    if let Some((host_port, _)) = s.split_once('/') {
        s = host_port;
    }
    if let Some((host, _)) = s.split_once(':') {
        s = host;
    }
    let parts: Vec<&str> = s.trim().split('.').collect();
    if parts.len() == 4 {
        if let (Ok(a), Ok(b)) = (parts[0].parse::<u8>(), parts[1].parse::<u8>()) {
            return a == 100 && (64..=127).contains(&b);
        }
    }
    false
}

fn link_mode_badge(addr: &str) -> &'static str {
    if is_tailscale_addr(addr) {
        "🌐 Tailscale 直连"
    } else {
        "⚡ 局域网直连"
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
    .qq-revoke-pill {
        background: alpha(#8a94a6, 0.16);
        border-radius: 999px;
        padding: 3px 12px;
    }
    .qq-reedit-btn {
        color: #0099ff;
        font-size: 0.82em;
        font-weight: 700;
        padding: 0px 4px;
        min-height: 18px;
    }
    .qq-quote-in {
        background: alpha(#8a94a6, 0.12);
        border-left: 3px solid #0099ff;
        border-radius: 6px;
        padding: 5px 8px;
    }
    .qq-quote-out {
        background: alpha(white, 0.18);
        border-left: 3px solid white;
        border-radius: 6px;
        padding: 5px 8px;
    }
    .qq-reply-banner {
        background: alpha(#0099ff, 0.09);
        border-radius: 8px;
        padding: 4px 10px;
    }
    .qq-pinned-banner {
        background: alpha(#f59e0b, 0.14);
        color: #b45309;
        padding: 6px 14px;
    }
    .qq-call-banner {
        background: alpha(#10b981, 0.14);
        color: #047857;
        padding: 6px 14px;
    }
    .qq-screen-stage {
        background: #111827;
        color: white;
        padding: 8px 14px;
    }
    .qq-code-card {
        background: #1e293b;
        color: #e2e8f0;
        border-radius: 6px;
        padding: 8px 10px;
        font-size: 0.86em;
    }
    .qq-reply-accent {
        background: #0099ff;
        border-radius: 2px;
    }
    .qq-reply-text {
        color: #0077ee;
        font-weight: 600;
    }
    .qq-ctx-btn {
        padding: 5px 12px;
        border-radius: 6px;
        font-size: 0.88em;
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

    #[test]
    fn tailscale_and_lan_link_badge_classification() {
        assert!(is_tailscale_addr("100.105.48.87:7878"));
        assert!(is_tailscale_addr("ws://100.93.183.47:7878/lanchat"));
        assert!(!is_tailscale_addr("192.168.1.52:7878"));
        assert_eq!(link_mode_badge("100.83.15.53:7878"), "🌐 Tailscale 直连");
        assert_eq!(link_mode_badge("192.168.1.52:7878"), "⚡ 局域网直连");
    }
}
