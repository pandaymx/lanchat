using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>登录页：自动发现服务器、选择/手填地址、输入 PSK 并连接。</summary>
public sealed partial class LoginViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread _ui;

    [ObservableProperty]
    private string _manualAddr = "";

    [ObservableProperty]
    private string _psk = "";

    [ObservableProperty]
    private ServerInfo? _selectedServer;

    [ObservableProperty]
    private bool _isBusy;

    [ObservableProperty]
    private string _statusMessage = "";

    [ObservableProperty]
    private bool _hasError;

    public ObservableCollection<ServerInfo> Servers { get; } = [];

    /// <summary>选中服务器需要 PSK（authMode=psk）时显示口令框。</summary>
    public bool RequiresPsk =>
        string.Equals(SelectedServer?.AuthMode, "psk", StringComparison.OrdinalIgnoreCase);

    public LoginViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
    }

    /// <summary>GetState 初始化：已连接时无需处理。</summary>
    public void Initialize(State state)
    {
        if (!string.IsNullOrEmpty(state.Server))
        {
            ManualAddr = state.Server;
        }
    }

    [RelayCommand]
    private async Task BrowseAsync()
    {
        IsBusy = true;
        ClearError();
        try
        {
            var servers = await _services.Ipc.InvokeAsync<List<ServerInfo>>("BrowseServers");
            Servers.Clear();
            foreach (var server in servers)
            {
                Servers.Add(server);
            }

            StatusMessage = servers.Count == 0
                ? "未发现局域网内的服务器，可手动输入地址"
                : $"发现 {servers.Count} 个服务器";
        }
        catch (Exception ex)
        {
            ShowError($"发现失败：{ex.Message}");
        }
        finally
        {
            IsBusy = false;
        }
    }

    [RelayCommand]
    private async Task ConnectAsync()
    {
        var addr = BuildAddr();
        if (string.IsNullOrWhiteSpace(addr))
        {
            ShowError("请选择服务器或输入服务器地址");
            return;
        }

        if (RequiresPsk && string.IsNullOrWhiteSpace(Psk))
        {
            ShowError("该服务器需要口令，请输入 PSK");
            return;
        }

        IsBusy = true;
        ClearError();
        StatusMessage = "正在连接…";
        try
        {
            await _services.Ipc.InvokeAsync("Connect", new
            {
                addr,
                psk = Psk,
            });
            // 最终结果由 conn.changed 通知驱动；此处仅等待状态切换
            StatusMessage = "连接请求已发送";
        }
        catch (Exception ex)
        {
            ShowError($"连接失败：{ex.Message}");
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>conn.changed 回调：auth_failed 时提示口令错误。</summary>
    public void HandleConnChanged(ConnState conn, string reason)
    {
        switch (conn)
        {
            case ConnState.Connected:
                ClearError();
                StatusMessage = "已连接";
                break;
            case ConnState.Connecting:
                ClearError();
                StatusMessage = "正在连接…";
                break;
            case ConnState.AuthFailed:
                ShowError("口令错误，认证被拒绝");
                break;
            case ConnState.Disconnected:
                if (!string.IsNullOrEmpty(reason))
                {
                    ShowError($"连接断开：{reason}");
                }
                break;
        }
    }

    /// <summary>构造 Connect 地址：mDNS 候选为 host:port（daemon 默认 / 路径），手动框原样传入。</summary>
    private string BuildAddr()
    {
        if (SelectedServer is not null && !string.IsNullOrEmpty(SelectedServer.Addr))
        {
            return SelectedServer.Addr;
        }
        return ManualAddr.Trim();
    }

    partial void OnSelectedServerChanged(ServerInfo? value)
    {
        OnPropertyChanged(nameof(RequiresPsk));
        if (value is not null)
        {
            ManualAddr = value.Addr;
            Psk = "";
            ClearError();
        }
    }

    private void ShowError(string message)
    {
        HasError = true;
        StatusMessage = message;
    }

    private void ClearError()
    {
        HasError = false;
    }
}
