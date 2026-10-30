using System.Collections.ObjectModel;
using System.Text.Json;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>聊天：单播与频道会话切换、消息收发、文件发送与接收确认入口。</summary>
public sealed partial class ChatViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread? _ui;
    private readonly Dictionary<string, List<ChatMessage>> _conversations = new();

    [ObservableProperty]
    private Peer? _currentPeer;

    [ObservableProperty]
    private Channel? _currentChannel;

    [ObservableProperty]
    private string _draft = "";

    [ObservableProperty]
    private bool _isE2eeEnabled;

    [ObservableProperty]
    private bool _isInVoiceCall;

    [ObservableProperty]
    private bool _isIncomingCallRinging;

    [ObservableProperty]
    private bool _isScreenSharing;

    [ObservableProperty]
    private string _voiceCallStatusText = "";

    [ObservableProperty]
    private string _screenShareFrameSource = "";

    [ObservableProperty]
    private ChatMessage? _replyingTo;

    public ObservableCollection<ChatMessage> Messages { get; } = [];

    /// <summary>当前会话是否存在置顶消息横幅（NTQQ 顶部黄色/浅蓝置顶条）。</summary>
    public bool HasPinnedBanner => !string.IsNullOrWhiteSpace(PinnedBannerText);

    /// <summary>当前会话最新置顶消息摘要文案。</summary>
    public string PinnedBannerText
    {
        get
        {
            var pinned = Messages.LastOrDefault(m =>
                !m.Revoked && (m.Pinned || m.Text.StartsWith("📌 ", StringComparison.Ordinal)));
            return pinned is null ? "" : $"📌 [置顶] {pinned.FromName}：{pinned.BodyText}";
        }
    }

    /// <summary>当前用户在 G2 频道中的角色与禁言徽章（群主 / 管理员 / 禁言中，v8.1.0）。</summary>
    public string ChannelRoleBadgeText
    {
        get
        {
            if (CurrentChannel is null || CurrentChannel.Id == "*" || string.IsNullOrEmpty(SelfId))
            {
                return "";
            }
            var badges = new List<string>();
            if (CurrentChannel.OwnerId == SelfId)
            {
                badges.Add("👑 群主");
            }
            else if (CurrentChannel.Admins.Contains(SelfId))
            {
                badges.Add("🛡️ 管理员");
            }
            else if (IsMember(CurrentChannel))
            {
                badges.Add("👤 成员");
            }
            if (CurrentChannel.Muted.Contains(SelfId))
            {
                badges.Add("🔇 禁言中");
            }
            return string.Join(" · ", badges);
        }
    }

    /// <summary>当前 G2 频道是否存在尚未被本机确认的必读公告（v8.1.0）。</summary>
    public bool HasUnackedAnnouncement =>
        CurrentChannel is not null &&
        CurrentChannel.Id != "*" &&
        !string.IsNullOrWhiteSpace(CurrentChannel.Topic) &&
        !string.IsNullOrEmpty(SelfId) &&
        IsMember(CurrentChannel) &&
        !CurrentChannel.AnnouncementAck.Contains(SelfId);

    /// <summary>必读频道公告横幅文案（v8.1.0）。</summary>
    public string AnnouncementBannerText =>
        CurrentChannel is null || string.IsNullOrWhiteSpace(CurrentChannel.Topic)
            ? ""
            : $"📢 [必读频道公告] {CurrentChannel.Topic}（已确认 {CurrentChannel.AnnouncementAck.Count}/{Math.Max(1, CurrentChannel.Members.Count)}）";

    /// <summary>是否展示 NTQQ 顶部实时语音通话状态栏。</summary>
    public bool HasVoiceCallBanner => IsInVoiceCall || IsIncomingCallRinging || !string.IsNullOrWhiteSpace(VoiceCallStatusText);

    /// <summary>是否展示 NTQQ 顶部实时屏幕共享画布舞台。</summary>
    public bool HasScreenShareStage => IsScreenSharing || !string.IsNullOrWhiteSpace(ScreenShareFrameSource);

    public ObservableCollection<string> CurrentMembers { get; } = [];

    /// <summary>收到对端文件邀请（state=pending 的 inbound 传输），请求 UI 弹接收确认。</summary>
    public event Action<Transfer>? IncomingFileOffer;

    /// <summary>请求 UI 弹出文件选择器（聊天内点「文件」）。</summary>
    public event Action? FilePickRequested;

    /// <summary>请求 UI 弹出贴纸选择器（聊天内点「贴纸」）。</summary>
    public event Action? StickerPickRequested;

    public ChatViewModel(AppServices services, UiThread? ui = null)
    {
        _services = services;
        _ui = ui;
    }

    /// <summary>当前会话标题：频道名 / 联系人昵称。</summary>
    public string ConversationTitle =>
        CurrentChannel is not null
            ? (CurrentChannel.Id == "*" ? "📢 局域网全员广播群" : $"# {CurrentChannel.Name}")
            : (CurrentPeer?.Nickname ?? "选择联系人或频道开始聊天");

    /// <summary>聊天区顶部副标题（在线状态 / P2P 状态 / 频道人数与主题）。</summary>
    public string ConversationSubtitle
    {
        get
        {
            var linkBadge = ServerInfo.IsTailscaleAddress(ServerAddr) || ServerInfo.IsTailscaleAddress(CurrentPeer?.Status)
                ? "🌐 Tailscale 直连"
                : "⚡ 局域网直连";

            if (CurrentChannel is not null)
            {
                if (CurrentChannel.Id == "*")
                {
                    return $"● G1 全员广播群 · {linkBadge} · Swarm P2P 多源并发直连就绪";
                }
                var topicPart = string.IsNullOrWhiteSpace(CurrentChannel.Topic)
                    ? "G2 自定义频道"
                    : CurrentChannel.Topic;
                var roleBadge = ChannelRoleBadgeText;
                var rolePrefix = string.IsNullOrWhiteSpace(roleBadge) ? "" : $"{roleBadge} · ";
                return $"{rolePrefix}👥 {CurrentChannel.Members.Count} 名成员 · {linkBadge} · {topicPart}";
            }

            if (CurrentPeer is not null)
            {
                var osPart = string.IsNullOrWhiteSpace(CurrentPeer.Os) ? "LAN" : CurrentPeer.Os;
                return $"● 在线 ({osPart}) · {linkBadge} · P2P 高速直连就绪";
            }

            return $"从左侧选择在线好友、全员广播群或 G2 频道开始交流 · {linkBadge}";
        }
    }

    /// <summary>聊天区顶部圆形头像徽章文字。</summary>
    public string ConversationAvatarText =>
        CurrentChannel is not null
            ? (CurrentChannel.Id == "*" ? "📢" : "#")
            : (CurrentPeer?.AvatarInitial ?? "💬");

    public bool HasActiveConversation => CurrentPeer is not null || CurrentChannel is not null;

    /// <summary>是否为 G2 自定义频道会话（非全员广播）。</summary>
    public bool IsChannelConversation => CurrentChannel is not null && CurrentChannel.Id != "*";

    /// <summary>是否为 G1 局域网全员广播群会话。</summary>
    public bool IsBroadcastConversation => CurrentChannel?.Id == "*";

    public string E2eeButtonText => IsE2eeEnabled ? "🔐 已加密" : "🔐 加密";

    public string VoiceCallButtonText => IsInVoiceCall ? "📵 挂断语音" : "📞 语音通话";

    public string ScreenShareButtonText => IsScreenSharing ? "⏹ 停止投屏" : "🖥️ 屏幕共享";

    /// <summary>是否正在引用回复某条消息。</summary>
    public bool HasReplyTarget => ReplyingTo is not null;

    /// <summary>输入框上方的 QQ 引用回复预览条文案。</summary>
    public string ReplyTargetSummary =>
        ReplyingTo is null
            ? ""
            : $"回复 {(string.IsNullOrWhiteSpace(ReplyingTo.FromName) ? "对方" : ReplyingTo.FromName)}：{ReplyingTo.BodyText}";

    /// <summary>是否处于频道会话且当前用户已加入（可发消息/文件）。</summary>
    public bool CanSend =>
        CurrentPeer is not null ||
        (CurrentChannel is not null && IsMember(CurrentChannel));

    /// <summary>处于未加入的频道会话（头部显示「加入频道」）。</summary>
    public bool CanJoinChannel =>
        CurrentChannel is not null && !IsMember(CurrentChannel);

    /// <summary>切换到 G1 局域网全员广播群会话（group = "*"）。</summary>
    public void SelectBroadcastConversation()
    {
        var broadcast = new Channel
        {
            Id = "*",
            Name = "局域网全员广播群",
            Topic = "G1 全员广播 · Swarm P2P 极速分发",
            Members = string.IsNullOrEmpty(SelfId) ? [] : [SelfId],
        };
        SelectChannelConversation(broadcast);
    }

    /// <summary>切换到单播会话。</summary>
    public void SelectConversation(Peer? peer)
    {
        CurrentPeer = peer;
        CurrentChannel = null;
        ReplyingTo = null;
        ShowConversation(peer?.Id);
    }

    /// <summary>切换到频道会话。</summary>
    public void SelectChannelConversation(Channel? channel)
    {
        CurrentChannel = channel;
        CurrentPeer = null;
        ReplyingTo = null;
        ShowConversation(channel?.Id);
        RefreshMembers(channel);
    }

    private void ShowConversation(string? key)
    {
        Messages.Clear();
        if (key is null)
        {
            NotifyPinnedBannerChanged();
            return;
        }

        if (_conversations.TryGetValue(key, out var history))
        {
            foreach (var message in history)
            {
                Messages.Add(message);
            }
        }
        NotifyPinnedBannerChanged();
    }

    [RelayCommand]
    private async Task SendAsync()
    {
        var text = Draft.Trim();
        if (string.IsNullOrEmpty(text))
        {
            return;
        }

        if (ReplyingTo is not null && !text.StartsWith("/", StringComparison.Ordinal))
        {
            text = $"/reply {ReplyingTo.MsgId} {text}";
            ReplyingTo = null;
        }

        if (text.StartsWith("/search ", StringComparison.Ordinal))
        {
            var query = text[8..].Trim();
            var activeKey = CurrentChannel?.Id ?? CurrentPeer?.Id ?? "";
            var count = SearchConversation(activeKey, query).Count;
            AppendSystemMessage($"搜索“{query}”：共 {count} 条匹配消息");
            Draft = "";
            return;
        }

        if (CurrentChannel is not null)
        {
            if (!IsMember(CurrentChannel))
            {
                AppendSystemMessage("请先加入频道再发言");
                return;
            }

            try
            {
                var result = await _services.Ipc.InvokeAsync("SendText", new
                {
                    to = "",
                    text,
                    group = CurrentChannel.Id,
                });
                var msgId = result.TryGetProperty("msgID", out var id) ? id.GetString() ?? "" : "";
                if (msgId is "e2ee:on" or "e2ee:off")
                {
                    AppendSystemMessage(msgId == "e2ee:on" ? "已启用端到端消息加密" : "已关闭端到端消息加密");
                    Draft = "";
                    return;
                }
                if (msgId.StartsWith("fp:", StringComparison.Ordinal))
                {
                    AppendSystemMessage($"🛡️ 安全码指纹核验：{msgId[3..]}");
                    Draft = "";
                    return;
                }
                if (msgId.StartsWith("bwlimit:", StringComparison.Ordinal))
                {
                    AppendSystemMessage($"⚡ 传输带宽限速已设置为：{msgId[8..]} MB/s");
                    Draft = "";
                    return;
                }
                if (msgId.StartsWith("status:", StringComparison.Ordinal))
                {
                    AppendSystemMessage($"📊 诊断状态：{msgId[7..]}");
                    Draft = "";
                    return;
                }
                if (msgId.StartsWith("update:", StringComparison.Ordinal))
                {
                    AppendSystemMessage($"🚀 版本更新：{msgId[7..]}");
                    Draft = "";
                    return;
                }
                if (text.StartsWith("/revoke ", StringComparison.Ordinal))
                {
                    RevokeMessage(text[8..].Trim());
                    Draft = "";
                    return;
                }
                if (text.StartsWith("/read ", StringComparison.Ordinal))
                {
                    MarkRead(text[6..].Trim(), SelfId);
                    Draft = "";
                    return;
                }
                if (text.StartsWith("/react ", StringComparison.Ordinal))
                {
                    var parts = text[7..].Trim().Split(' ', 2, StringSplitOptions.RemoveEmptyEntries);
                    var emoji = parts.Length >= 2 ? parts[1] : "👍";
                    UpdateMessageDisplay(msgId, m => m.Text.Contains($"[{emoji}]", StringComparison.Ordinal) ? m.Text : $"{m.Text} [{emoji}]");
                    Draft = "";
                    return;
                }
                if (text.StartsWith("/edit ", StringComparison.Ordinal))
                {
                    var parts = text[6..].Trim().Split(' ', 2, StringSplitOptions.RemoveEmptyEntries);
                    if (parts.Length >= 2)
                    {
                        UpdateMessageDisplay(msgId, _ => $"{parts[1]} (已编辑)");
                    }
                    Draft = "";
                    return;
                }
                if (text.StartsWith("/pin ", StringComparison.Ordinal))
                {
                    UpdateMessageDisplay(msgId, m => m.Text.StartsWith("📌 ", StringComparison.Ordinal) ? m.Text : $"📌 {m.Text}");
                    Draft = "";
                    return;
                }
                AppendMessage(new ChatMessage
                {
                    MsgId = msgId,
                    FromId = SelfId,
                    FromName = "我",
                    GroupId = CurrentChannel.Id,
                    Text = FormatReplyText(text),
                    Type = "text",
                    IsSelf = true,
                });
                Draft = "";
            }
            catch (Exception ex)
            {
                AppendSystemMessage($"发送失败：{ex.Message}");
            }

            return;
        }

        if (CurrentPeer is null)
        {
            return;
        }

        try
        {
            var result = await _services.Ipc.InvokeAsync("SendText", new
            {
                to = CurrentPeer.Id,
                text,
            });
            var msgId = result.TryGetProperty("msgID", out var id) ? id.GetString() ?? "" : "";
            if (msgId is "e2ee:on" or "e2ee:off")
            {
                AppendSystemMessage(msgId == "e2ee:on" ? "已启用端到端消息加密" : "已关闭端到端消息加密");
                Draft = "";
                return;
            }
            if (msgId.StartsWith("fp:", StringComparison.Ordinal))
            {
                AppendSystemMessage($"🛡️ 安全码指纹核验：{msgId[3..]}");
                Draft = "";
                return;
            }
            if (msgId.StartsWith("bwlimit:", StringComparison.Ordinal))
            {
                AppendSystemMessage($"⚡ 传输带宽限速已设置为：{msgId[8..]} MB/s");
                Draft = "";
                return;
            }
            if (msgId.StartsWith("status:", StringComparison.Ordinal))
            {
                AppendSystemMessage($"📊 诊断状态：{msgId[7..]}");
                Draft = "";
                return;
            }
            if (text.StartsWith("/revoke ", StringComparison.Ordinal))
            {
                RevokeMessage(text[8..].Trim());
                Draft = "";
                return;
            }
            if (text.StartsWith("/read ", StringComparison.Ordinal))
            {
                MarkRead(text[6..].Trim(), SelfId);
                Draft = "";
                return;
            }
            if (text.StartsWith("/react ", StringComparison.Ordinal))
            {
                var parts = text[7..].Trim().Split(' ', 2, StringSplitOptions.RemoveEmptyEntries);
                var emoji = parts.Length >= 2 ? parts[1] : "👍";
                UpdateMessageDisplay(msgId, m => m.Text.Contains($"[{emoji}]", StringComparison.Ordinal) ? m.Text : $"{m.Text} [{emoji}]");
                Draft = "";
                return;
            }
            if (text.StartsWith("/edit ", StringComparison.Ordinal))
            {
                var parts = text[6..].Trim().Split(' ', 2, StringSplitOptions.RemoveEmptyEntries);
                if (parts.Length >= 2)
                {
                    UpdateMessageDisplay(msgId, _ => $"{parts[1]} (已编辑)");
                }
                Draft = "";
                return;
            }
            if (text.StartsWith("/pin ", StringComparison.Ordinal))
            {
                UpdateMessageDisplay(msgId, m => m.Text.StartsWith("📌 ", StringComparison.Ordinal) ? m.Text : $"📌 {m.Text}");
                Draft = "";
                return;
            }
            AppendMessage(new ChatMessage
            {
                MsgId = msgId,
                FromId = SelfId,
                FromName = "我",
                Text = FormatReplyText(text),
                Type = "text",
                IsSelf = true,
            });
            Draft = "";
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"发送失败：{ex.Message}");
        }
    }

    [RelayCommand]
    private void PickFile()
    {
        if (CanSend)
        {
            FilePickRequested?.Invoke();
        }
    }

    [RelayCommand]
    private void PickSticker()
    {
        if (CanSend)
        {
            StickerPickRequested?.Invoke();
        }
    }

    /// <summary>贴纸选择器选定后调用：发送贴纸消息（单播或频道）。</summary>
    public async Task SendStickerAsync(string path)
    {
        if (string.IsNullOrEmpty(path))
        {
            return;
        }

        var target = CurrentChannel?.Id ?? CurrentPeer?.Id ?? "";
        if (string.IsNullOrEmpty(target))
        {
            return;
        }

        try
        {
            var result = await _services.Ipc.InvokeAsync("SendSticker", new
            {
                to = target,
                path,
            });
            var msgId = result.TryGetProperty("msgID", out var id) ? id.GetString() ?? "" : "";
            AppendMessage(new ChatMessage
            {
                MsgId = msgId,
                FromId = SelfId,
                FromName = "我",
                GroupId = CurrentChannel?.Id,
                Text = $"[贴纸: {Path.GetFileName(path)}]",
                Type = "sticker",
                IsSelf = true,
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"贴纸发送失败：{ex.Message}");
        }
    }

    /// <summary>文件选择器选定后调用：单播走 OfferFile，频道走 OfferFileToGroup。</summary>
    public async Task OfferFileAsync(string path)
    {
        if (string.IsNullOrEmpty(path))
        {
            return;
        }

        try
        {
            if (CurrentChannel is not null)
            {
                await _services.Ipc.InvokeAsync("OfferFileToGroup", new
                {
                    group = CurrentChannel.Id,
                    path,
                });
                AppendSystemMessage($"已向频道 {CurrentChannel.Name} 发送文件：{Path.GetFileName(path)}");
                return;
            }

            if (CurrentPeer is null)
            {
                return;
            }

            await _services.Ipc.InvokeAsync("OfferFile", new
            {
                to = CurrentPeer.Id,
                path,
            });
            AppendSystemMessage($"已向 {CurrentPeer.Nickname} 发送文件：{Path.GetFileName(path)}");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"文件发送失败：{ex.Message}");
        }
    }

    /// <summary>从 GetState 恢复本地历史消息。</summary>
    public void ApplyHistory(IReadOnlyList<HistoryMessage> messages)
    {
        foreach (var hm in messages)
        {
            if (hm.Id.StartsWith("fp-", StringComparison.Ordinal) ||
                hm.Id.StartsWith("status-", StringComparison.Ordinal) ||
                hm.Id.StartsWith("update-", StringComparison.Ordinal) ||
                ChatMessage.IsSystemDiagnosticText(hm.Text))
            {
                continue;
            }

            var isSelf = !string.IsNullOrEmpty(SelfId) && hm.From == SelfId;
            var peerOrFrom = isSelf ? (!string.IsNullOrEmpty(hm.To) ? hm.To! : hm.From) : hm.From;
            var baseName = isSelf ? "我" : (ResolvePeer(hm.From)?.Nickname ?? hm.From);
            var senderName = isSelf && !hm.Revoked && hm.ReadBy.Count > 0
                ? $"{baseName} · ✓ 已读"
                : baseName;
            AppendMessage(new ChatMessage
            {
                MsgId = hm.Id,
                FromId = peerOrFrom,
                FromName = senderName,
                GroupId = string.IsNullOrEmpty(hm.Group) ? null : hm.Group,
                Text = hm.Text,
                Type = string.IsNullOrEmpty(hm.Type) ? "text" : hm.Type,
                IsSelf = isSelf,
                Timestamp = hm.Ts > 0 ? DateTimeOffset.FromUnixTimeMilliseconds(hm.Ts) : DateTimeOffset.Now,
                Revoked = hm.Revoked,
                ReadBy = hm.ReadBy,
                ReplyQuote = hm.ReplyQuote,
                StructuredReactions = hm.Reactions,
                EditedAt = hm.EditedAt,
                Pinned = hm.Pinned,
                MediaMime = hm.MediaMime,
                MediaPath = hm.MediaPath,
            });
        }
    }

    /// <summary>处理契约 v1.1 msg.updated 通知：按结构化 HistoryMessage 直接替换对应 MsgId 的模型。</summary>
    public void HandleMessageUpdated(JsonElement parameters)
    {
        if (!parameters.TryGetProperty("message", out var msgElem))
        {
            return;
        }
        var hm = msgElem.Deserialize<HistoryMessage>(Ipc.IpcClient.JsonOptions);
        if (hm is null || string.IsNullOrEmpty(hm.Id))
        {
            return;
        }
        ApplyHistory([hm]);
    }

    /// <summary>解析 msg.received 通知，按 group 归类到频道会话。</summary>
    public void HandleMessageReceived(JsonElement parameters)
    {
        var from = parameters.GetProperty("from").GetString() ?? "";
        var group = parameters.TryGetProperty("group", out var g) && g.ValueKind == JsonValueKind.String
            ? g.GetString()
            : null;
        var msgId = parameters.TryGetProperty("msgId", out var m) ? m.GetString() ?? "" : "";
        var type = parameters.TryGetProperty("type", out var t) ? t.GetString() ?? "text" : "text";
        var text = parameters.TryGetProperty("text", out var x) ? x.GetString() ?? "" : "";

        if (type is "MSG_REVOKE" or "MSG_BURN")
        {
            RevokeMessage(msgId);
            return;
        }

        if (type == "SCREEN_FRAME")
        {
            if (!string.IsNullOrWhiteSpace(text))
            {
                ScreenShareFrameSource = text;
                IsScreenSharing = true;
                NotifyCollabBannerChanged();
            }
            return;
        }

        if (type == "CLIP_SYNC")
        {
            var rawClip = text.StartsWith("📋 [剪贴板已同步] ", StringComparison.Ordinal)
                ? text["📋 [剪贴板已同步] ".Length..]
                : text;
            if (!string.IsNullOrEmpty(rawClip))
            {
                try
                {
                    var pkg = new Windows.ApplicationModel.DataTransfer.DataPackage();
                    pkg.SetText(rawClip);
                    Windows.ApplicationModel.DataTransfer.Clipboard.SetContent(pkg);
                }
                catch
                {
                    // 后台剪贴板异常忽略
                }
            }
            return;
        }

        if (type == "MSG_READ")
        {
            MarkRead(msgId, from);
            return;
        }

        if (type is "MSG_REACT" or "MSG_EDIT" or "MSG_PIN")
        {
            UpdateMessageDisplay(msgId, _ => text);
            return;
        }

        var senderName = ResolvePeer(from)?.Nickname ?? from;

        AppendMessage(new ChatMessage
        {
            MsgId = msgId,
            FromId = from,
            FromName = senderName,
            GroupId = group,
            Text = text,
            Type = type,
            IsSelf = from == SelfId,
        });
    }

    /// <summary>同步 GetState 返回的契约 v1.2 语音与投屏状态。</summary>
    public void ApplyCollabState(VoiceCallState? voiceCall, ScreenShareState? screenShare)
    {
        if (voiceCall is not null)
        {
            IsIncomingCallRinging = voiceCall.State == "ringing";
            IsInVoiceCall = voiceCall.State is "calling" or "active" or "in_call";
            var callerName = ResolvePeer(voiceCall.PeerId ?? "")?.Nickname ?? voiceCall.PeerId ?? "局域网对端";
            VoiceCallStatusText = IsIncomingCallRinging
                ? $"🔔 [来电响铃] {callerName} 邀请你进行实时语音通话"
                : (IsInVoiceCall
                    ? $"📞 实时语音通话中 · 收发帧 {voiceCall.FramesIn}/{voiceCall.FramesOut}"
                    : "");
        }
        if (screenShare is not null)
        {
            IsScreenSharing = screenShare.Active;
            if (!string.IsNullOrWhiteSpace(screenShare.LatestFrame))
            {
                ScreenShareFrameSource = screenShare.LatestFrame!;
            }
        }
        NotifyCollabBannerChanged();
    }

    /// <summary>处理契约 v1.2 call.state 通知。</summary>
    public void HandleCallStateChanged(JsonElement parameters)
    {
        var callElem = parameters.TryGetProperty("call", out var c) && c.ValueKind == JsonValueKind.Object ? c : parameters;
        var state = callElem.TryGetProperty("state", out var s) ? s.GetString() ?? "idle" : "idle";
        var peerId = callElem.TryGetProperty("peerId", out var p) ? p.GetString() ?? "" : "";
        var callerName = ResolvePeer(peerId)?.Nickname ?? peerId;
        IsIncomingCallRinging = state == "ringing";
        IsInVoiceCall = state is "calling" or "active" or "in_call";
        VoiceCallStatusText = IsIncomingCallRinging
            ? $"🔔 [来电响铃] {callerName} 邀请你进行实时低延迟语音通话"
            : (IsInVoiceCall
                ? $"📞 正在与 {callerName} 实时低延迟语音通话 ({state})"
                : "");
        NotifyCollabBannerChanged();
    }

    /// <summary>处理契约 v1.2 screen.frame 通知。</summary>
    public void HandleScreenFrameReceived(JsonElement parameters)
    {
        var frame = parameters.TryGetProperty("frameData", out var fd)
            ? fd.GetString() ?? ""
            : (parameters.TryGetProperty("frame", out var f) ? f.GetString() ?? "" : "");
        if (!string.IsNullOrWhiteSpace(frame))
        {
            ScreenShareFrameSource = frame;
            IsScreenSharing = true;
            NotifyCollabBannerChanged();
        }
        else
        {
            ScreenShareFrameSource = "";
            IsScreenSharing = false;
            NotifyCollabBannerChanged();
        }
    }

    /// <summary>进度通知：pending inbound 时触发接收确认弹窗，并在会话流内插入/更新 QQ 风格文件卡片消息。</summary>
    public void HandleTransferProgress(Transfer transfer)
    {
        if (transfer.Direction == TransferDirection.Inbound &&
            transfer.State == TransferState.Pending)
        {
            IncomingFileOffer?.Invoke(transfer);
        }

        var pathBadge = transfer.ViaRelay ? "⚠ AES-GCM 加密中继" : "⚡ P2P 高速直连";
        var dirLabel = transfer.Direction == TransferDirection.Inbound ? "📥 收到文件" : "📤 发送文件";
        var pct = transfer.Size > 0 ? Math.Clamp((int)(transfer.BytesDone * 100 / transfer.Size), 0, 100) : 0;
        var peerId = transfer.PeerId ?? "";
        var senderName = ResolvePeer(peerId)?.Nickname ?? peerId;
        AppendMessage(new ChatMessage
        {
            MsgId = $"file-{transfer.Id}",
            FromId = peerId,
            FromName = transfer.Direction == TransferDirection.Outbound ? "我" : senderName,
            GroupId = string.IsNullOrEmpty(transfer.GroupId) ? null : transfer.GroupId,
            Text = $"{dirLabel}：{transfer.Name}\n📊 进度 {pct}% · {pathBadge} · 状态 {transfer.State}",
            Type = "file",
            IsSelf = transfer.Direction == TransferDirection.Outbound,
        });
    }

    public void HandleTransferFailed(string transferId, string reason)
    {
        AppendSystemMessage($"传输失败：{reason}");
    }

    /// <summary>接收确认结果回调（由 View 层弹窗后触发）。</summary>
    public async Task RespondFileAsync(Transfer transfer, bool accept, string dest)
    {
        try
        {
            await _services.Ipc.InvokeAsync("RespondFile", new
            {
                transferID = transfer.Id,
                accept,
                dest,
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"应答文件失败：{ex.Message}");
        }
    }

    /// <summary>频道成员 / 昵称解析器（由 Shell 注入 RosterViewModel.FindPeer）。</summary>
    public Func<string, Peer?>? PeerResolver { get; set; }

    /// <summary>当前用户 id（由 Shell 随 State 注入）。</summary>
    public string SelfId { get; set; } = "";

    /// <summary>当前连接的服务器地址（由 Shell 随 State 注入，用于判定 ⚡ 局域网直连 / 🌐 Tailscale 直连）。</summary>
    public string ServerAddr { get; set; } = "";

    private Peer? ResolvePeer(string peerId) => PeerResolver?.Invoke(peerId);

    private bool IsMember(Channel channel) =>
        channel.Id == "*" || channel.Members.Contains(SelfId);

    /// <summary>快捷引用回复某条消息（展示在输入框上方 QQ 引用预览条）。</summary>
    public void PrepareReply(ChatMessage? message)
    {
        if (message is null || string.IsNullOrEmpty(message.MsgId) || message.Revoked)
        {
            return;
        }
        ReplyingTo = message;
    }

    /// <summary>取消当前引用回复。</summary>
    public void CancelReply() => ReplyingTo = null;

    /// <summary>点击已撤回消息旁边的「重新编辑」：将撤回前的原文放回输入框。</summary>
    public void ReEditMessage(ChatMessage? message)
    {
        if (message is null || string.IsNullOrWhiteSpace(message.OriginalText))
        {
            return;
        }
        Draft = message.OriginalText;
    }

    /// <summary>快捷撤回自己发送的消息（契约 v1.4 显式 RevokeMessage RPC）。</summary>
    public async Task RevokeMessageByUiAsync(ChatMessage? message)
    {
        if (message is null || !message.CanRevoke)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("RevokeMessage", new
            {
                to = CurrentPeer?.Id ?? "",
                group = CurrentChannel?.Id ?? "",
                msgID = message.MsgId,
            });
            RevokeMessage(message.MsgId);
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"撤回消息失败：{ex.Message}");
        }
    }

    /// <summary>M20 / 契约 v1.4: 快捷对消息添加表情回应（显式 ReactMessage RPC）。</summary>
    public async Task ReactMessageByUiAsync(ChatMessage? message, string emoji = "👍")
    {
        if (message is null || string.IsNullOrEmpty(message.MsgId) || message.Revoked)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("ReactMessage", new
            {
                to = CurrentPeer?.Id ?? "",
                group = CurrentChannel?.Id ?? "",
                msgID = message.MsgId,
                emoji,
            });
            UpdateMessageDisplay(message.MsgId, m => m.Text.Contains($" [{emoji}]", StringComparison.Ordinal) ? m.Text : $"{m.Text} [{emoji}]");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"表情回应失败：{ex.Message}");
        }
    }

    /// <summary>契约 v1.4: 原地二次编辑消息（显式 EditMessage RPC）。</summary>
    public async Task EditMessageByUiAsync(ChatMessage? message, string newText)
    {
        var trimmed = (newText ?? "").Trim();
        if (message is null || string.IsNullOrEmpty(message.MsgId) || message.Revoked || string.IsNullOrEmpty(trimmed))
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("EditMessage", new
            {
                to = CurrentPeer?.Id ?? "",
                group = CurrentChannel?.Id ?? "",
                msgID = message.MsgId,
                newText = trimmed,
            });
            UpdateMessageDisplay(message.MsgId, _ => $"{trimmed} (已编辑)");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"编辑消息失败：{ex.Message}");
        }
    }

    /// <summary>M20 / 契约 v1.4: 快捷置顶消息（显式 PinMessage RPC）。</summary>
    public async Task PinMessageByUiAsync(ChatMessage? message)
    {
        if (message is null || string.IsNullOrEmpty(message.MsgId) || message.Revoked)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("PinMessage", new
            {
                to = CurrentPeer?.Id ?? "",
                group = CurrentChannel?.Id ?? "",
                msgID = message.MsgId,
            });
            UpdateMessageDisplay(message.MsgId, m => m.Text.StartsWith("📌 ", StringComparison.Ordinal) ? m.Text : $"📌 {m.Text}");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"置顶消息失败：{ex.Message}");
        }
    }

    /// <summary>契约 v1.4: 标记消息已读并上报已读回执（显式 MarkMessageRead RPC）。</summary>
    public async Task MarkMessageReadByUiAsync(ChatMessage? message)
    {
        if (message is null || string.IsNullOrEmpty(message.MsgId) || message.Revoked)
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("MarkMessageRead", new
            {
                to = CurrentPeer?.Id ?? "",
                group = CurrentChannel?.Id ?? "",
                msgID = message.MsgId,
            });
            MarkRead(message.MsgId, SelfId);
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"标记已读失败：{ex.Message}");
        }
    }

    /// <summary>快捷切换端到端加密（契约 v1.4 显式 SetSessionE2EE RPC）。</summary>
    public async Task ToggleE2eeAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var nextState = !IsE2eeEnabled;
        try
        {
            await _services.Ipc.InvokeAsync("SetSessionE2EE", new
            {
                secret = nextState ? "lanchat-qq-key" : "",
            });
            IsE2eeEnabled = nextState;
            OnPropertyChanged(nameof(E2eeButtonText));
            AppendSystemMessage(nextState ? "🔐 已开启会话端到端加密" : "🔓 已关闭会话端到端加密");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"切换端到端加密失败：{ex.Message}");
        }
    }

    /// <summary>契约 v1.4: 开启或关闭跨设备剪贴板自动监听同步（显式 SetAutoClipboard RPC）。</summary>
    public async Task SetAutoClipboardAsync(bool enabled)
    {
        try
        {
            await _services.Ipc.InvokeAsync("SetAutoClipboard", new { enabled });
            AppendSystemMessage(enabled ? "📋 已开启跨设备剪贴板自动同步" : "📋 已关闭跨设备剪贴板自动同步");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"设置自动剪贴板失败：{ex.Message}");
        }
    }

    /// <summary>契约 v1.4: 开启或关闭全局免打扰模式（显式 SetDND RPC）。</summary>
    public async Task SetDndAsync(bool enabled)
    {
        try
        {
            await _services.Ipc.InvokeAsync("SetDND", new { enabled });
            AppendSystemMessage(enabled ? "🔕 已开启免打扰模式" : "🔔 已关闭免打扰模式");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"设置免打扰失败：{ex.Message}");
        }
    }

    /// <summary>契约 v1.4: 按会话与时间戳游标分页拉取历史消息（显式 GetHistoryPage RPC）。</summary>
    public async Task LoadMoreHistoryPageAsync(long beforeTs = 0, int limit = 50)
    {
        var convID = CurrentChannel?.Id ?? CurrentPeer?.Id ?? "";
        if (string.IsNullOrEmpty(convID))
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync("GetHistoryPage", new
            {
                convID,
                beforeTS = beforeTs,
                limit,
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"拉取历史分页失败：{ex.Message}");
        }
    }

    /// <summary>M15 / v7.0: 核验当前会话 X25519 安全码指纹（VerifyPeerFingerprint RPC）。</summary>
    public async Task VerifyFingerprintAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var peerId = CurrentPeer?.Id ?? CurrentChannel?.Id ?? "";
        try
        {
            var res = await _services.Ipc.InvokeAsync("VerifyPeerFingerprint", new { peerID = peerId });
            var fp = res.TryGetProperty("fingerprint", out var prop) ? prop.GetString() ?? "" : "";
            if (!string.IsNullOrEmpty(fp))
            {
                AppendSystemMessage($"🛡️ 安全码指纹核验：{fp}");
            }
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"安全码核验失败：{ex.Message}");
        }
    }

    /// <summary>v7.0: 发送阅后即焚加密消息（SendBurnMessage RPC）。</summary>
    public async Task SendBurnMessageByUiAsync(string text, int ttlSec = 10)
    {
        if (!CanSend || string.IsNullOrWhiteSpace(text))
        {
            return;
        }
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SendBurnMessage", new
            {
                to,
                group = grp,
                text = text.Trim(),
                ttlSec,
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"发送阅后即焚失败：{ex.Message}");
        }
    }

    /// <summary>v7.0: 动态设置全局 P2P 传输带宽限速（SetBandwidthLimit RPC）。</summary>
    public async Task SetBandwidthLimitByUiAsync(double limitMBps)
    {
        try
        {
            await _services.Ipc.InvokeAsync<object>("SetBandwidthLimit", new
            {
                limitMBps,
            });
            AppendSystemMessage(limitMBps <= 0 ? "⚡ 已解除 P2P 传输带宽限速" : $"⚡ 已将 P2P 传输带宽限速设为 {limitMBps:F1} MB/s");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"设置带宽限速失败：{ex.Message}");
        }
    }

    /// <summary>M17: 快捷发送语音消息 (/voice)。</summary>
    public async Task SendVoiceAsync(int seconds = 3)
    {
        if (!CanSend)
        {
            return;
        }
        var prevDraft = Draft;
        Draft = $"/voice {seconds}";
        await SendAsync();
        Draft = prevDraft;
    }

    /// <summary>往输入框插入 Emoji 表情。</summary>
    public void InsertEmoji(string emoji)
    {
        if (string.IsNullOrEmpty(emoji))
        {
            return;
        }
        Draft += emoji;
    }

    /// <summary>按关键字搜索当前会话聊天记录。</summary>
    public void ExecuteSearchQuery(string query)
    {
        var q = query.Trim();
        if (string.IsNullOrEmpty(q))
        {
            return;
        }
        var activeKey = CurrentChannel?.Id ?? CurrentPeer?.Id ?? "";
        var matches = SearchConversation(activeKey, q);
        AppendSystemMessage($"🔍 搜索“{q}”：共找到 {matches.Count} 条匹配记录");
    }

    private void RefreshMembers(Channel? channel)
    {
        CurrentMembers.Clear();
        if (channel is null)
        {
            return;
        }

        foreach (var memberId in channel.Members)
        {
            CurrentMembers.Add(ResolvePeer(memberId)?.Nickname ?? memberId);
        }
    }

    /// <summary>频道成员变化（channel.updated）后刷新当前频道与成员列表。</summary>
    public void HandleChannelUpdated(Channel? channel)
    {
        if (channel is not null)
        {
            CurrentChannel = channel;
            NotifyConversationHeaderChanged();
            RefreshMembers(channel);
        }
    }

    private void AppendMessage(ChatMessage message)
    {
        var key = !string.IsNullOrEmpty(message.GroupId)
            ? message.GroupId!
            : (message.IsSelf
                ? (!string.IsNullOrEmpty(message.FromId) && message.FromId != SelfId
                    ? message.FromId
                    : (CurrentPeer?.Id ?? message.FromId))
                : message.FromId);

        if (!_conversations.TryGetValue(key, out var list))
        {
            list = [];
            _conversations[key] = list;
        }

        // 同一消息去重或状态更新（如撤回 / 已读）
        if (!string.IsNullOrEmpty(message.MsgId))
        {
            var existingIdx = list.FindIndex(m => m.MsgId == message.MsgId);
            if (existingIdx >= 0)
            {
                var prev = list[existingIdx];
                var merged = message.Revoked && string.IsNullOrWhiteSpace(message.OriginalText)
                    ? message with { OriginalText = !string.IsNullOrWhiteSpace(prev.OriginalText) ? prev.OriginalText : prev.BodyText }
                    : message;
                list[existingIdx] = merged;
                if ((CurrentChannel?.Id ?? CurrentPeer?.Id) is string activeKey && activeKey == key &&
                    existingIdx < Messages.Count)
                {
                    Messages[existingIdx] = merged;
                    NotifyPinnedBannerChanged();
                }
                return;
            }
        }

        list.Add(message);
        if ((CurrentChannel?.Id ?? CurrentPeer?.Id) is string currentKey && currentKey == key)
        {
            Messages.Add(message);
            NotifyPinnedBannerChanged();
        }
    }

    private void RevokeMessage(string msgId)
    {
        if (string.IsNullOrEmpty(msgId))
        {
            return;
        }

        foreach (var (key, list) in _conversations)
        {
            for (var i = 0; i < list.Count; i++)
            {
                if (list[i].MsgId == msgId)
                {
                    var orig = !string.IsNullOrWhiteSpace(list[i].OriginalText)
                        ? list[i].OriginalText
                        : (list[i].Revoked ? "" : list[i].BodyText);
                    var updated = list[i] with
                    {
                        Revoked = true,
                        OriginalText = orig,
                        Text = "消息已撤回",
                        FromName = list[i].IsSelf ? "我" : list[i].FromName,
                    };
                    list[i] = updated;
                    if ((CurrentChannel?.Id ?? CurrentPeer?.Id) == key && i < Messages.Count)
                    {
                        Messages[i] = updated;
                        NotifyPinnedBannerChanged();
                    }
                }
            }
        }
    }

    private void UpdateMessageDisplay(string msgId, Func<ChatMessage, string> transform)
    {
        if (string.IsNullOrEmpty(msgId))
        {
            return;
        }

        foreach (var (key, list) in _conversations)
        {
            for (var i = 0; i < list.Count; i++)
            {
                if (list[i].MsgId == msgId && !list[i].Revoked)
                {
                    var updated = list[i] with { Text = transform(list[i]) };
                    list[i] = updated;
                    if ((CurrentChannel?.Id ?? CurrentPeer?.Id) == key && i < Messages.Count)
                    {
                        Messages[i] = updated;
                        NotifyPinnedBannerChanged();
                    }
                }
            }
        }
    }

    private void MarkRead(string msgId, string reader)
    {
        if (string.IsNullOrEmpty(msgId) || string.IsNullOrEmpty(reader))
        {
            return;
        }

        foreach (var (key, list) in _conversations)
        {
            for (var i = 0; i < list.Count; i++)
            {
                if (list[i].MsgId == msgId && !list[i].ReadBy.Contains(reader))
                {
                    var readBy = list[i].ReadBy.Concat([reader]).ToList();
                    var fromName = list[i].IsSelf && !list[i].Revoked
                        ? "我 · ✓ 已读"
                        : list[i].FromName;
                    var updated = list[i] with
                    {
                        ReadBy = readBy,
                        FromName = fromName,
                    };
                    list[i] = updated;
                    if ((CurrentChannel?.Id ?? CurrentPeer?.Id) == key && i < Messages.Count)
                    {
                        Messages[i] = updated;
                        NotifyPinnedBannerChanged();
                    }
                }
            }
        }
    }

    private void AppendSystemMessage(string text)
    {
        if (CurrentChannel is null && CurrentPeer is null)
        {
            return;
        }
        Messages.Add(new ChatMessage
        {
            FromName = "系统",
            Text = text,
            Type = "system",
        });
    }

    private List<ChatMessage> SearchConversation(string key, string query)
    {
        if (string.IsNullOrWhiteSpace(query) || !_conversations.TryGetValue(key, out var list))
        {
            return [];
        }
        return list.Where(m => !m.Revoked && m.Text.Contains(query, StringComparison.OrdinalIgnoreCase)).ToList();
    }

    private string FormatReplyText(string raw)
    {
        var trimmed = raw.Trim();
        if (trimmed.Equals("/voice", StringComparison.Ordinal) ||
            trimmed.StartsWith("/voice ", StringComparison.Ordinal))
        {
            var arg = trimmed[6..].Trim();
            var sec = string.IsNullOrEmpty(arg) ? "3" : arg;
            return $"🎤 [语音消息 {sec}\"]";
        }
        if (!trimmed.StartsWith("/reply ", StringComparison.Ordinal))
        {
            return raw;
        }
        var rest = trimmed[7..].Trim();
        var parts = rest.Split(' ', 2, StringSplitOptions.RemoveEmptyEntries);
        if (parts.Length < 2)
        {
            return raw;
        }
        var targetId = parts[0].Trim();
        var body = parts[1].Trim();
        foreach (var list in _conversations.Values)
        {
            var orig = list.FirstOrDefault(m => m.MsgId == targetId && !m.Revoked);
            if (orig is not null)
            {
                return $"「{orig.CleanText}」\n{body}";
            }
        }
        return $"[re:{targetId}] {body}";
    }

    partial void OnCurrentPeerChanged(Peer? value) => NotifyConversationHeaderChanged();

    partial void OnCurrentChannelChanged(Channel? value) => NotifyConversationHeaderChanged();

    partial void OnReplyingToChanged(ChatMessage? value)
    {
        OnPropertyChanged(nameof(HasReplyTarget));
        OnPropertyChanged(nameof(ReplyTargetSummary));
    }

    partial void OnIsInVoiceCallChanged(bool value)
    {
        OnPropertyChanged(nameof(VoiceCallButtonText));
        NotifyCollabBannerChanged();
    }

    partial void OnIsScreenSharingChanged(bool value)
    {
        OnPropertyChanged(nameof(ScreenShareButtonText));
        if (!value)
        {
            ScreenShareFrameSource = "";
        }
        NotifyCollabBannerChanged();
    }

    private void NotifyPinnedBannerChanged()
    {
        OnPropertyChanged(nameof(HasPinnedBanner));
        OnPropertyChanged(nameof(PinnedBannerText));
    }

    private void NotifyCollabBannerChanged()
    {
        OnPropertyChanged(nameof(HasVoiceCallBanner));
        OnPropertyChanged(nameof(IsIncomingCallRinging));
        OnPropertyChanged(nameof(HasScreenShareStage));
    }

    private (string To, string Group) ResolveCollabTarget()
    {
        if (CurrentChannel is not null)
        {
            return ("", CurrentChannel.Id);
        }
        return (CurrentPeer?.Id ?? "", "");
    }

    [RelayCommand]
    public async Task AcceptIncomingCallAsync()
    {
        IsIncomingCallRinging = false;
        IsInVoiceCall = true;
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("AcceptVoiceCall", new
            {
                to,
                group = grp,
                remoteUDP = "",
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"接听失败：{ex.Message}");
        }
        NotifyCollabBannerChanged();
    }

    [RelayCommand]
    public async Task RejectOrHangupCallAsync()
    {
        IsIncomingCallRinging = false;
        IsInVoiceCall = false;
        VoiceCallStatusText = "";
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("StopVoiceCall", new
            {
                to,
                group = grp,
            });
        }
        catch
        {
            // ignore
        }
        NotifyCollabBannerChanged();
    }

    /// <summary>v3.0/v4.8/v5.0: 屏幕共享激光笔批注（SendScreenAnnotation RPC）。</summary>
    [RelayCommand]
    public async Task AnnotateScreenAsync()
    {
        await AnnotateScreenAtAsync(0.5, 0.5);
    }

    /// <summary>v4.8/v5.0: 点击投屏画面按归一化坐标发送激光笔红点批注。</summary>
    public async Task AnnotateScreenAtAsync(double normX, double normY)
    {
        if (!CanSend)
        {
            return;
        }
        var x = Math.Clamp(normX, 0.0, 1.0);
        var y = Math.Clamp(normY, 0.0, 1.0);
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SendScreenAnnotation", new
            {
                to,
                group = grp,
                normX = x,
                normY = y,
                note = "🔴 激光笔点击批注",
            });
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"激光批注失败：{ex.Message}");
        }
    }

    /// <summary>v3.0/v4.0/v7.0: P2P 离线历史消息漫游同步（SyncHistoryWithPeer RPC）。</summary>
    [RelayCommand]
    public async Task SyncHistoryAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SyncHistoryWithPeer", new
            {
                to,
                group = grp,
                sinceTS = 0L,
            });
            AppendSystemMessage("🔄 已触发 P2P 加密历史漫游增量同步");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"历史漫游同步失败：{ex.Message}");
        }
    }

    /// <summary>v5.0: 增量块去重目录同步（SyncFolder RPC）。</summary>
    public async Task SyncFolderExplicitAsync(string dirPath)
    {
        var (to, grp) = ResolveCollabTarget();
        await _services.Ipc.InvokeAsync<object>("SyncFolder", new
        {
            to,
            group = grp,
            dirPath,
        });
    }

    /// <summary>v5.2: 拖拽或选取目录触发增量同步。</summary>
    public Task SyncFolderAsync(string dirPath) => SyncFolderExplicitAsync(dirPath);

    /// <summary>v5.0: 签发与校验零信任节点准入证书（IssueTrustCert / VerifyTrustCert RPC）。</summary>
    public async Task<string> IssueAndVerifyTrustCertAsync(string subject, string org)
    {
        _ = await _services.Ipc.InvokeAsync<object>("IssueTrustCert", new
        {
            subject,
            org,
            ttlSec = 86400 * 30,
        });
        _ = await _services.Ipc.InvokeAsync<object>("VerifyTrustCert", new
        {
            token = subject,
            rootPub = "",
        });
        return subject;
    }

    /// <summary>v3.0/v4.0/v5.0: 局域网全链路诊断雷达（RunNetworkDiagnostics RPC）。</summary>
    [RelayCommand]
    public async Task RunNetDiagAsync()
    {
        try
        {
            var res = await _services.Ipc.InvokeAsync<System.Text.Json.JsonElement>("RunNetworkDiagnostics");
            var report = res.TryGetProperty("report", out var prop) ? prop.GetString() : res.ToString();
            AppendSystemMessage($"📡 链路诊断：{report}");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"链路诊断失败：{ex.Message}");
        }
    }

    [RelayCommand]
    public async Task ToggleScreenShareAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var (to, grp) = ResolveCollabTarget();
        try
        {
            if (IsScreenSharing)
            {
                IsScreenSharing = false;
                await _services.Ipc.InvokeAsync<object>("StopScreenShare", new
                {
                    to,
                    group = grp,
                });
            }
            else
            {
                IsScreenSharing = true;
                await _services.Ipc.InvokeAsync<object>("StartScreenShare", new
                {
                    to,
                    group = grp,
                    fps = 24,
                });
            }
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"屏幕共享操作失败：{ex.Message}");
        }
    }

    public bool IsVoiceMuted { get; private set; }
    public string VoiceMuteButtonText => IsVoiceMuted ? "🔇 已静音" : "🎙️ 静音";

    [RelayCommand]
    public async Task ToggleVoiceMuteAsync()
    {
        if (!CanSend)
        {
            return;
        }
        IsVoiceMuted = !IsVoiceMuted;
        OnPropertyChanged(nameof(IsVoiceMuted));
        OnPropertyChanged(nameof(VoiceMuteButtonText));
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SendText", new
            {
                to,
                text = IsVoiceMuted ? "/call mute" : "/call unmute",
                group = grp,
            });
        }
        catch
        {
            // ignore
        }
    }

    public bool IsScreenCropped { get; private set; }
    public string ScreenCropButtonText => IsScreenCropped ? "🖥️ 恢复全屏" : "🗔 窗口裁剪";

    [RelayCommand]
    public async Task ToggleScreenCropAsync()
    {
        if (!CanSend)
        {
            return;
        }
        IsScreenCropped = !IsScreenCropped;
        OnPropertyChanged(nameof(IsScreenCropped));
        OnPropertyChanged(nameof(ScreenCropButtonText));
        var (to, grp) = ResolveCollabTarget();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SendText", new
            {
                to,
                text = IsScreenCropped ? "/screenshare region 0.05,0.05,0.90,0.90" : "/screenshare region 0,0,0,0",
                group = grp,
            });
        }
        catch
        {
            // ignore
        }
    }

    [RelayCommand]
    public async Task ToggleVoiceCallAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var (to, grp) = ResolveCollabTarget();
        try
        {
            if (IsInVoiceCall)
            {
                IsInVoiceCall = false;
                await _services.Ipc.InvokeAsync<object>("StopVoiceCall", new
                {
                    to,
                    group = grp,
                });
            }
            else
            {
                IsInVoiceCall = true;
                await _services.Ipc.InvokeAsync<object>("StartVoiceCall", new
                {
                    to,
                    group = grp,
                });
            }
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"语音通话操作失败：{ex.Message}");
        }
    }

    /// <summary>执行频道精细化权限治理（契约 v1.6 ChannelManageMember RPC）。</summary>
    public async Task ChannelManageMemberAsync(string channelId, string memberId, string action)
    {
        if (string.IsNullOrWhiteSpace(channelId) || string.IsNullOrWhiteSpace(action))
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync<object>("ChannelManageMember", new
            {
                channelID = channelId,
                memberID = memberId ?? string.Empty,
                action,
            });
            AppendSystemMessage($"🛡️ 频道治理已执行：{action} ({memberId})");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"频道治理失败：{ex.Message}");
        }
    }

    /// <summary>向中心节点加密离线投递箱投递 X25519 密文信封（契约 v1.6 EnqueueOfflineMailbox RPC）。</summary>
    public async Task EnqueueOfflineMailboxAsync(string to, string text, string recipientPub = "")
    {
        if (string.IsNullOrWhiteSpace(to) || string.IsNullOrWhiteSpace(text))
        {
            return;
        }
        try
        {
            await _services.Ipc.InvokeAsync<System.Text.Json.JsonElement>("EnqueueOfflineMailbox", new
            {
                to,
                text,
                recipientPub,
            });
            AppendSystemMessage("📬 已投递至中心节点加密离线信箱");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"离线信箱投递失败：{ex.Message}");
        }
    }

    /// <summary>拉取、解密并销毁中心节点暂存的离线加密信封（契约 v1.6 DrainOfflineMailbox RPC）。</summary>
    public async Task DrainOfflineMailboxAsync(string fromPeer = "")
    {
        try
        {
            var res = await _services.Ipc.InvokeAsync<System.Text.Json.JsonElement>("DrainOfflineMailbox", new
            {
                fromPeer,
            });
            var status = res.TryGetProperty("status", out var prop) ? prop.GetString() : res.ToString();
            AppendSystemMessage($"📬 离线信箱已收取：{status}");
        }
        catch (Exception ex)
        {
            AppendSystemMessage($"离线信箱拉取失败：{ex.Message}");
        }
    }

    /// <summary>确认当前 G2 频道的必读公告回执（v8.1.0）。</summary>
    [RelayCommand]
    public async Task AckChannelAnnouncementAsync()
    {
        if (CurrentChannel is null || CurrentChannel.Id == "*" || string.IsNullOrEmpty(SelfId))
        {
            return;
        }
        var ch = CurrentChannel;
        if (!ch.AnnouncementAck.Contains(SelfId))
        {
            var nextAck = ch.AnnouncementAck.Concat([SelfId]).Distinct().ToList();
            CurrentChannel = ch with { AnnouncementAck = nextAck };
            NotifyConversationHeaderChanged();
        }
        await ChannelManageMemberAsync(ch.Id, SelfId, "ack_announcement");
    }

    private void NotifyConversationHeaderChanged()
    {
        OnPropertyChanged(nameof(ConversationTitle));
        OnPropertyChanged(nameof(ConversationSubtitle));
        OnPropertyChanged(nameof(ConversationAvatarText));
        OnPropertyChanged(nameof(ChannelRoleBadgeText));
        OnPropertyChanged(nameof(HasUnackedAnnouncement));
        OnPropertyChanged(nameof(AnnouncementBannerText));
        OnPropertyChanged(nameof(HasActiveConversation));
        OnPropertyChanged(nameof(IsChannelConversation));
        OnPropertyChanged(nameof(IsBroadcastConversation));
        OnPropertyChanged(nameof(CanSend));
        OnPropertyChanged(nameof(CanJoinChannel));
    }
}
