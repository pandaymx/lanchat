using System.Collections.ObjectModel;
using System.Text.Json;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>聊天：会话切换、消息收发、文件发送与接收确认入口。</summary>
public sealed partial class ChatViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread _ui;
    private readonly Dictionary<string, List<ChatMessage>> _conversations = new();

    [ObservableProperty]
    private Peer? _currentPeer;

    [ObservableProperty]
    private string _draft = "";

    public ObservableCollection<ChatMessage> Messages { get; } = [];

    /// <summary>收到对端文件邀请（state=pending 的 inbound 传输），请求 UI 弹接收确认。</summary>
    public event Action<Transfer>? IncomingFileOffer;

    /// <summary>请求 UI 弹出文件选择器（聊天内点「文件」），参数为当前会话对端。</summary>
    public event Action<Peer>? FilePickRequested;

    public ChatViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
    }

    /// <summary>切换当前会话。</summary>
    public void SelectConversation(Peer? peer)
    {
        CurrentPeer = peer;
        Messages.Clear();
        if (peer is null)
        {
            return;
        }

        if (_conversations.TryGetValue(peer.Id, out var history))
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
        if (CurrentPeer is null || string.IsNullOrEmpty(text))
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
                FromId = _services.Settings.Nickname,
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
        if (CurrentPeer is not null)
        {
            FilePickRequested?.Invoke(CurrentPeer);
        }
    }

    /// <summary>文件选择器选定后调用：发起 OfferFile。</summary>
    public async Task OfferFileAsync(string path)
    {
        if (CurrentPeer is null || string.IsNullOrEmpty(path))
        {
            return;
        }

        try
        {
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

    /// <summary>解析 msg.received 通知。</summary>
    public void HandleMessageReceived(JsonElement parameters)
    {
        var from = parameters.GetProperty("from").GetString() ?? "";
        var msgId = parameters.TryGetProperty("msgId", out var m) ? m.GetString() ?? "" : "";
        var type = parameters.TryGetProperty("type", out var t) ? t.GetString() ?? "text" : "text";
        var text = parameters.TryGetProperty("text", out var x) ? x.GetString() ?? "" : "";

        var peer = ResolvePeer(from);
        AppendMessage(new ChatMessage
        {
            MsgId = msgId,
            FromId = from,
            FromName = peer?.Nickname ?? from,
            Text = text,
            Type = type,
            IsSelf = false,
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

    /// <summary>联系人解析器（由 Shell 注入 RosterViewModel.FindPeer）。</summary>
    public Func<string, Peer?>? PeerResolver { get; set; }

    private Peer? ResolvePeer(string peerId) => PeerResolver?.Invoke(peerId);

    private void AppendMessage(ChatMessage message)
    {
        var key = message.IsSelf && CurrentPeer is not null
            ? CurrentPeer.Id
            : message.FromId;

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
        if (CurrentPeer is not null &&
            (key == CurrentPeer.Id || (message.IsSelf && key == CurrentPeer.Id)))
        {
            Messages.Add(message);
        }
    }

    private void AppendSystemMessage(string text)
    {
        if (CurrentPeer is null)
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
}
