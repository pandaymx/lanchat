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

    public ObservableCollection<ChatMessage> Messages { get; } = [];

    public ObservableCollection<string> CurrentMembers { get; } = [];

    /// <summary>收到对端文件邀请（state=pending 的 inbound 传输），请求 UI 弹接收确认。</summary>
    public event Action<Transfer>? IncomingFileOffer;

    /// <summary>请求 UI 弹出文件选择器（聊天内点「文件」）。</summary>
    public event Action? FilePickRequested;

    public ChatViewModel(AppServices services, UiThread? ui = null)
    {
        _services = services;
        _ui = ui;
    }

    /// <summary>当前会话标题：频道名 / 联系人昵称。</summary>
    public string ConversationTitle =>
        CurrentChannel?.Name ?? CurrentPeer?.Nickname ?? "";

    /// <summary>是否处于频道会话且当前用户已加入（可发消息/文件）。</summary>
    public bool CanSend =>
        CurrentPeer is not null ||
        (CurrentChannel is not null && IsMember(CurrentChannel));

    /// <summary>处于未加入的频道会话（头部显示「加入频道」）。</summary>
    public bool CanJoinChannel =>
        CurrentChannel is not null && !IsMember(CurrentChannel);

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
                AppendMessage(new ChatMessage
                {
                    MsgId = msgId,
                    FromId = SelfId,
                    FromName = "我",
                    GroupId = CurrentChannel.Id,
                    Text = text,
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
            AppendMessage(new ChatMessage
            {
                MsgId = msgId,
                FromId = SelfId,
                FromName = "我",
                Text = text,
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

    private bool IsMember(Channel channel) => channel.Members.Contains(SelfId);

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
            OnPropertyChanged(nameof(CanSend));
            OnPropertyChanged(nameof(CanJoinChannel));
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

        // 同一消息去重（重发通知）
        if (!string.IsNullOrEmpty(message.MsgId) && list.Any(m => m.MsgId == message.MsgId))
        {
            return;
        }

        list.Add(message);
        if ((CurrentChannel?.Id ?? CurrentPeer?.Id) is string currentKey && currentKey == key)
        {
            Messages.Add(message);
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

    partial void OnCurrentPeerChanged(Peer? value)
    {
        OnPropertyChanged(nameof(ConversationTitle));
        OnPropertyChanged(nameof(CanSend));
        OnPropertyChanged(nameof(CanJoinChannel));
    }

    partial void OnCurrentChannelChanged(Channel? value)
    {
        OnPropertyChanged(nameof(ConversationTitle));
        OnPropertyChanged(nameof(CanSend));
        OnPropertyChanged(nameof(CanJoinChannel));
    }
}
