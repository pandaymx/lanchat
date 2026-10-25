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

    public ObservableCollection<ChatMessage> Messages { get; } = [];

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
            if (CurrentChannel is not null)
            {
                if (CurrentChannel.Id == "*")
                {
                    return "● G1 全员广播群 · Swarm P2P 多源并发直连就绪";
                }
                var topicPart = string.IsNullOrWhiteSpace(CurrentChannel.Topic)
                    ? "G2 自定义频道"
                    : CurrentChannel.Topic;
                return $"👥 {CurrentChannel.Members.Count} 名成员 · {topicPart}";
            }

            if (CurrentPeer is not null)
            {
                var osPart = string.IsNullOrWhiteSpace(CurrentPeer.Os) ? "LAN" : CurrentPeer.Os;
                return $"● 在线 ({osPart}) · P2P 高速直连就绪";
            }

            return "从左侧选择在线好友、全员广播群或 G2 频道开始交流";
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

    public string E2eeButtonText => IsE2eeEnabled ? "🔐 E2EE 已启用" : "🔐 端到端加密 (/e2ee)";

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
        ShowConversation(peer?.Id);
    }

    /// <summary>切换到频道会话。</summary>
    public void SelectChannelConversation(Channel? channel)
    {
        CurrentChannel = channel;
        CurrentPeer = null;
        ShowConversation(channel?.Id);
        RefreshMembers(channel);
    }

    private void ShowConversation(string? key)
    {
        Messages.Clear();
        if (key is null)
        {
            return;
        }

        if (_conversations.TryGetValue(key, out var history))
        {
            foreach (var message in history)
            {
                Messages.Add(message);
            }
        }
    }

    [RelayCommand]
    private async Task SendAsync()
    {
        var text = Draft.Trim();
        if (string.IsNullOrEmpty(text))
        {
            return;
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
            });
        }
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

        if (type == "MSG_REVOKE")
        {
            RevokeMessage(msgId);
            return;
        }

        if (type == "MSG_READ")
        {
            MarkRead(msgId, from);
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

    /// <summary>进度通知：pending inbound 时触发接收确认弹窗。</summary>
    public void HandleTransferProgress(Transfer transfer)
    {
        if (transfer.Direction == TransferDirection.Inbound &&
            transfer.State == TransferState.Pending)
        {
            IncomingFileOffer?.Invoke(transfer);
        }
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

    private Peer? ResolvePeer(string peerId) => PeerResolver?.Invoke(peerId);

    private bool IsMember(Channel channel) =>
        channel.Id == "*" || channel.Members.Contains(SelfId);

    /// <summary>快捷引用回复某条消息。</summary>
    public void PrepareReply(ChatMessage? message)
    {
        if (message is null || string.IsNullOrEmpty(message.MsgId))
        {
            return;
        }
        Draft = $"/reply {message.MsgId} ";
    }

    /// <summary>快捷撤回自己发送的消息。</summary>
    public async Task RevokeMessageByUiAsync(ChatMessage? message)
    {
        if (message is null || !message.CanRevoke)
        {
            return;
        }
        var prevDraft = Draft;
        Draft = $"/revoke {message.MsgId}";
        await SendAsync();
        Draft = prevDraft;
    }

    /// <summary>快捷切换端到端加密（/e2ee on | off）。</summary>
    public async Task ToggleE2eeAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var nextState = !IsE2eeEnabled;
        var prevDraft = Draft;
        Draft = nextState ? "/e2ee on" : "/e2ee off";
        await SendAsync();
        IsE2eeEnabled = nextState;
        OnPropertyChanged(nameof(E2eeButtonText));
        Draft = prevDraft;
    }

    /// <summary>M15: 核验当前会话 X25519 安全码指纹 (/fingerprint)。</summary>
    public async Task VerifyFingerprintAsync()
    {
        if (!CanSend)
        {
            return;
        }
        var prevDraft = Draft;
        Draft = "/fingerprint";
        await SendAsync();
        Draft = prevDraft;
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
            : (message.IsSelf ? (CurrentPeer?.Id ?? message.FromId) : message.FromId);

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
                list[existingIdx] = message;
                if ((CurrentChannel?.Id ?? CurrentPeer?.Id) is string activeKey && activeKey == key &&
                    existingIdx < Messages.Count)
                {
                    Messages[existingIdx] = message;
                }
                return;
            }
        }

        list.Add(message);
        if ((CurrentChannel?.Id ?? CurrentPeer?.Id) is string currentKey && currentKey == key)
        {
            Messages.Add(message);
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
                    var updated = list[i] with
                    {
                        Revoked = true,
                        Text = "消息已撤回",
                        FromName = list[i].IsSelf ? "我" : list[i].FromName,
                    };
                    list[i] = updated;
                    if ((CurrentChannel?.Id ?? CurrentPeer?.Id) == key && i < Messages.Count)
                    {
                        Messages[i] = updated;
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
                return $"「{orig.Text}」\n{body}";
            }
        }
        return $"[re:{targetId}] {body}";
    }

    partial void OnCurrentPeerChanged(Peer? value) => NotifyConversationHeaderChanged();

    partial void OnCurrentChannelChanged(Channel? value) => NotifyConversationHeaderChanged();

    private void NotifyConversationHeaderChanged()
    {
        OnPropertyChanged(nameof(ConversationTitle));
        OnPropertyChanged(nameof(ConversationSubtitle));
        OnPropertyChanged(nameof(ConversationAvatarText));
        OnPropertyChanged(nameof(HasActiveConversation));
        OnPropertyChanged(nameof(IsChannelConversation));
        OnPropertyChanged(nameof(IsBroadcastConversation));
        OnPropertyChanged(nameof(CanSend));
        OnPropertyChanged(nameof(CanJoinChannel));
    }
}
