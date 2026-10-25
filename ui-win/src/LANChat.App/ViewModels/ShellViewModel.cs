using System.Text.Json;
using CommunityToolkit.Mvvm.ComponentModel;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>顶层壳：连接状态在「登录页」与「主界面」之间切换。</summary>
public sealed partial class ShellViewModel : ViewModelBase, IDisposable
{
    private readonly AppServices _services;
    private readonly UiThread _ui;

    public LoginViewModel Login { get; }
    public RosterViewModel Roster { get; }
    public ChannelsViewModel Channels { get; }
    public ChatViewModel Chat { get; }
    public TransfersViewModel Transfers { get; }
    public SettingsViewModel Settings { get; }

    /// <summary>当前用户 id（频道成员判定用）。</summary>
    public string? SelfId { get; private set; }

    [ObservableProperty]
    private ConnState _conn = ConnState.Disconnected;

    /// <summary>主界面当前显示：聊天或设置。</summary>
    [ObservableProperty]
    private bool _showSettings;

    /// <summary>左侧深色导航栏当前激活标签（messages / contacts / channels / transfers）。</summary>
    [ObservableProperty]
    private string _activeNavTab = "messages";

    /// <summary>右侧传输抽屉是否展开。</summary>
    [ObservableProperty]
    private bool _showTransfersDrawer = true;

    /// <summary>已连接到中心节点。</summary>
    public bool IsConnected => Conn == ConnState.Connected;

    /// <summary>未连接，展示登录页。</summary>
    public bool ShowLogin => !IsConnected;

    /// <summary>当前用户昵称展示（用于导航栏头像 Tooltip 与设置页）。</summary>
    public string SelfNickname =>
        string.IsNullOrWhiteSpace(Settings.Nickname) ? "局域网用户" : Settings.Nickname;

    /// <summary>当前用户圆形头像首字。</summary>
    public string SelfInitial =>
        string.IsNullOrWhiteSpace(SelfNickname) ? "Q" : SelfNickname.Trim()[..1].ToUpperInvariant();

    public bool IsNavMessages => !ShowSettings && ActiveNavTab == "messages";
    public bool IsNavContacts => !ShowSettings && ActiveNavTab == "contacts";
    public bool IsNavChannels => !ShowSettings && ActiveNavTab == "channels";
    public bool IsNavTransfers => ShowTransfersDrawer;

    public ShellViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
        Login = new LoginViewModel(services, ui);
        Roster = new RosterViewModel(services, ui);
        Channels = new ChannelsViewModel(services, ui);
        Chat = new ChatViewModel(services, ui);
        Transfers = new TransfersViewModel(services, ui);
        Settings = new SettingsViewModel(services, ui);

        Settings.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName == nameof(SettingsViewModel.Nickname))
            {
                OnPropertyChanged(nameof(SelfNickname));
                OnPropertyChanged(nameof(SelfInitial));
            }
        };

        _services.Ipc.Notification += OnNotification;
    }

    public void SelectNavTab(string tab)
    {
        if (tab == "settings")
        {
            ShowSettings = !ShowSettings;
            NotifyNavState();
            return;
        }

        if (tab == "transfers")
        {
            ShowTransfersDrawer = !ShowTransfersDrawer;
            NotifyNavState();
            return;
        }

        ShowSettings = false;
        ActiveNavTab = tab;
        NotifyNavState();
    }

    partial void OnShowSettingsChanged(bool value) => NotifyNavState();
    partial void OnActiveNavTabChanged(string value) => NotifyNavState();
    partial void OnShowTransfersDrawerChanged(bool value) => NotifyNavState();

    private void NotifyNavState()
    {
        OnPropertyChanged(nameof(IsNavMessages));
        OnPropertyChanged(nameof(IsNavContacts));
        OnPropertyChanged(nameof(IsNavChannels));
        OnPropertyChanged(nameof(IsNavTransfers));
    }

    /// <summary>启动后先全量同步一次状态。</summary>
    public async Task InitializeAsync()
    {
        try
        {
            var state = await _services.Ipc.InvokeAsync<State>("GetState");
            ApplyState(state);
        }
        catch
        {
            // daemon 尚未就绪时等待 conn.changed
        }
    }

    private void OnNotification(string method, JsonElement parameters)
    {
        switch (method)
        {
            case "conn.changed":
                var state = parameters.GetProperty("state").GetString();
                var conn = ParseConn(state);
                var reason = parameters.TryGetProperty("reason", out var r) ? r.GetString() ?? "" : "";
                _ui.Post(() =>
                {
                    Conn = conn;
                    OnPropertyChanged(nameof(IsConnected));
                    OnPropertyChanged(nameof(ShowLogin));
                    Login.HandleConnChanged(conn, reason);
                    Roster.HandleConnChanged(conn);
                    Channels.HandleConnChanged(conn);
                });
                break;
            case "peer.joined":
                var peer = parameters.GetProperty("peer").Deserialize<Peer>(Ipc.IpcClient.JsonOptions)!;
                _ui.Post(() => Roster.AddPeer(peer));
                break;
            case "peer.left":
                var peerId = parameters.GetProperty("peerId").GetString() ?? "";
                _ui.Post(() => Roster.RemovePeer(peerId));
                break;
            case "msg.received":
                _ui.Post(() => Chat.HandleMessageReceived(parameters));
                break;
            case "channel.updated":
                var channels = parameters.GetProperty("channels")
                    .Deserialize<IReadOnlyList<Channel>>(Ipc.IpcClient.JsonOptions) ?? [];
                _ui.Post(() => ApplyChannels(channels));
                break;
            case "transfer.progress":
                var transfer = parameters.GetProperty("transfer")
                    .Deserialize<Transfer>(Ipc.IpcClient.JsonOptions)!;
                _ui.Post(() =>
                {
                    Transfers.Upsert(transfer);
                    Chat.HandleTransferProgress(transfer);
                });
                break;
            case "transfer.done":
                var doneId = parameters.GetProperty("transferId").GetString() ?? "";
                _ui.Post(() => Transfers.MarkDone(doneId));
                break;
            case "transfer.failed":
                var failedId = parameters.GetProperty("transferId").GetString() ?? "";
                var failedReason = parameters.GetProperty("reason").GetString() ?? "";
                _ui.Post(() =>
                {
                    Transfers.MarkFailed(failedId, failedReason);
                    Chat.HandleTransferFailed(failedId, failedReason);
                });
                break;
            case "group.matrix":
                var matrixTransferId = parameters.TryGetProperty("transferId", out var tid) ? tid.GetString() ?? "" : "";
                var haveBitmap = parameters.TryGetProperty("haveBitmap", out var hb) ? hb.GetString() ?? "" : "";
                _ui.Post(() => Transfers.ApplyGroupMatrix(matrixTransferId, haveBitmap));
                break;
        }
    }

    private void ApplyState(State state)
    {
        _ui.Post(() =>
        {
            Conn = state.Conn;
            OnPropertyChanged(nameof(IsConnected));
            OnPropertyChanged(nameof(ShowLogin));
            SelfId = state.SelfId;
            Chat.SelfId = state.SelfId ?? "";
            if (!string.IsNullOrWhiteSpace(state.Nickname) && string.IsNullOrWhiteSpace(Settings.Nickname))
            {
                Settings.Nickname = state.Nickname;
            }
            OnPropertyChanged(nameof(SelfNickname));
            OnPropertyChanged(nameof(SelfInitial));
            Roster.ReplacePeers(state.Peers);
            Transfers.ReplaceTransfers(state.Transfers);
            ApplyChannels(state.Channels);
            Chat.ApplyHistory(state.Messages);
            Login.Initialize(state);
        });
    }

    /// <summary>把 channel.updated / GetState 的频道快照同步到列表与当前会话。</summary>
    private void ApplyChannels(IReadOnlyList<Channel> channels)
    {
        Channels.ReplaceChannels(channels, SelfId);
        if (Chat.CurrentChannel is { Id: var currentId })
        {
            var updated = channels.FirstOrDefault(c => c.Id == currentId);
            Chat.HandleChannelUpdated(updated);
        }
    }

    internal static ConnState ParseConn(string? value) => value switch
    {
        "connected" => ConnState.Connected,
        "connecting" => ConnState.Connecting,
        "auth_failed" => ConnState.AuthFailed,
        _ => ConnState.Disconnected,
    };

    public void Dispose()
    {
        _services.Ipc.Notification -= OnNotification;
    }
}
