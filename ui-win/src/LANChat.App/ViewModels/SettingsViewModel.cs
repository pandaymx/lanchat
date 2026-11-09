using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>昵称与默认下载目录设置。昵称经 SetNickname 同步给 daemon，下载目录仅本地。</summary>
public sealed partial class SettingsViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread _ui;

    [ObservableProperty]
    private string _nickname = "";

    [ObservableProperty]
    private string _downloadDir = "";

    [ObservableProperty]
    private string _updateProxy = "";

    [ObservableProperty]
    private string _updateStatus = "内置国内加速节点：ghfast.top · gh-proxy.com · ghproxy.net · mirror.ghproxy.com · ghps.cc";

    [ObservableProperty]
    private string _statusMessage = "";

    [ObservableProperty]
    private string _pairCodeInput = "";

    [ObservableProperty]
    private string _pairDeviceStatus = "支持桌面端与移动端通过 6 位动态配对码绑定同一主身份，实时加密镜像聊天记录与剪贴板";

    [ObservableProperty]
    private string _archivePassphrase = "";

    [ObservableProperty]
    private string _archivePath = "";

    [ObservableProperty]
    private string _archiveStatus = "使用 PBKDF2-HMAC-SHA256 + AES-256-GCM 将本地历史消息与 CAS 富媒体打包为单个 .lcha 加密归档";

    public SettingsViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
        Nickname = services.Settings.Nickname;
        DownloadDir = services.Settings.DownloadDir;
        ArchivePath = string.IsNullOrWhiteSpace(DownloadDir)
            ? System.IO.Path.Combine(System.Environment.GetFolderPath(System.Environment.SpecialFolder.UserProfile), "Downloads", "lanchat-backup.lcha")
            : System.IO.Path.Combine(DownloadDir, "lanchat-backup.lcha");
    }

    /// <summary>保存昵称：先同步 daemon 成功后再落本地配置。</summary>
    [RelayCommand]
    private async Task SaveNicknameAsync()
    {
        var name = Nickname.Trim();
        try
        {
            await _services.Ipc.InvokeAsync<object>("SetNickname", new { name });
            _services.Settings.Nickname = name;
            _services.Settings.Save();
            PostStatus("昵称已保存");
        }
        catch (Ipc.IpcException ex)
        {
            PostStatus($"保存失败：{ex.Message}");
        }
    }

    /// <summary>选择并保存默认下载目录（FolderPicker 在 View 层调用，结果回填）。</summary>
    [RelayCommand]
    private void SaveDownloadDir()
    {
        _services.Settings.DownloadDir = DownloadDir;
        _services.Settings.Save();
        _ = _services.Ipc.InvokeAsync("PickDownloadDir", new { path = DownloadDir });
        PostStatus("下载目录已保存");
    }

    internal void SetDownloadDir(string path)
    {
        DownloadDir = path;
        SaveDownloadDir();
    }

    /// <summary>通过国内加速代理链检测最新 Release 版本（不打开浏览器，契约 v1.5 CheckSilentUpdate RPC）。</summary>
    [RelayCommand]
    private async Task CheckUpdateAsync()
    {
        PostUpdateStatus("正在通过国内加速节点检测最新版本…");
        try
        {
            var res = await _services.Ipc.InvokeAsync("CheckSilentUpdate", new { proxy = UpdateProxy.Trim(), download = false });
            var raw = res.TryGetProperty("summary", out var prop) ? prop.GetString() ?? "" : "";
            var summary = raw.StartsWith("update:", StringComparison.Ordinal) ? raw[7..] : raw;
            PostUpdateStatus(summary);
            PostStatus(summary);
        }
        catch (Exception ex)
        {
            PostUpdateStatus($"检测更新失败：{ex.Message}");
        }
    }

    /// <summary>通过国内加速代理链静默下载最新安装包到本地下载目录（无需调用浏览器，契约 v1.5 CheckSilentUpdate RPC）。</summary>
    [RelayCommand]
    private async Task SilentDownloadUpdateAsync()
    {
        var proxyArg = UpdateProxy.Trim();
        PostUpdateStatus("正在通过国内加速代理静默下载最新安装包（免浏览器）…");
        try
        {
            var res = await _services.Ipc.InvokeAsync("CheckSilentUpdate", new { proxy = proxyArg, download = true });
            var raw = res.TryGetProperty("summary", out var prop) ? prop.GetString() ?? "" : "";
            var summary = raw.StartsWith("update:", StringComparison.Ordinal) ? raw[7..] : raw;
            PostUpdateStatus(summary);
            PostStatus(summary);
        }
        catch (Exception ex)
        {
            PostUpdateStatus($"静默下载失败：{ex.Message}");
        }
    }

    private void PostUpdateStatus(string message) =>
        _ui.Post(() =>
        {
            UpdateStatus = message;
            OnPropertyChanged(nameof(UpdateStatus));
        });

    /// <summary>生成 6 位多设备动态配对码（契约 v1.7 PairDevice RPC）。</summary>
    [RelayCommand]
    private async Task GeneratePairCodeAsync()
    {
        try
        {
            var res = await _services.Ipc.InvokeAsync("PairDevice", new { action = "generate", code = "", peerID = "" });
            var status = res.TryGetProperty("status", out var prop) ? prop.GetString() ?? "" : res.ToString();
            _ui.Post(() =>
            {
                PairDeviceStatus = $"🔑 已生成配对码：{status}";
                OnPropertyChanged(nameof(PairDeviceStatus));
            });
            PostStatus($"已生成多设备配对码：{status}");
        }
        catch (Exception ex)
        {
            PostStatus($"生成配对码失败：{ex.Message}");
        }
    }

    /// <summary>使用 6 位配对码绑定对端设备（契约 v1.7 PairDevice RPC）。</summary>
    [RelayCommand]
    private async Task BindPairCodeAsync()
    {
        var code = PairCodeInput.Trim();
        if (string.IsNullOrEmpty(code))
        {
            PostStatus("请输入对端展示的 6 位配对码");
            return;
        }
        try
        {
            var res = await _services.Ipc.InvokeAsync("PairDevice", new { action = "bind", code, peerID = "" });
            var status = res.TryGetProperty("status", out var prop) ? prop.GetString() ?? "" : res.ToString();
            _ui.Post(() =>
            {
                PairDeviceStatus = $"🔗 配对成功：{status}";
                PairCodeInput = "";
                OnPropertyChanged(nameof(PairDeviceStatus));
                OnPropertyChanged(nameof(PairCodeInput));
            });
            PostStatus($"多设备配对完成：{status}");
        }
        catch (Exception ex)
        {
            PostStatus($"配对绑定失败：{ex.Message}");
        }
    }

    /// <summary>查询已绑定的同账号设备列表（契约 v1.7 ListPairedDevices RPC）。</summary>
    [RelayCommand]
    private async Task RefreshPairedDevicesAsync()
    {
        try
        {
            var res = await _services.Ipc.InvokeAsync("ListPairedDevices", new { filter = "" });
            var devices = res.TryGetProperty("devices", out var prop) ? prop.GetString() ?? "" : res.ToString();
            _ui.Post(() =>
            {
                PairDeviceStatus = string.IsNullOrWhiteSpace(devices) || devices == "[]"
                    ? "📱 当前暂无已绑定设备，可先生成配对码或输入对端配对码"
                    : $"📱 已配对设备：{devices}";
                OnPropertyChanged(nameof(PairDeviceStatus));
            });
        }
        catch (Exception ex)
        {
            PostStatus($"查询已绑定设备失败：{ex.Message}");
        }
    }

    /// <summary>导出全量加密归档包（契约 v1.7 ExportHistoryArchive RPC）。</summary>
    [RelayCommand]
    private async Task ExportArchiveAsync()
    {
        var dest = ArchivePath.Trim();
        var pass = ArchivePassphrase.Trim();
        if (string.IsNullOrEmpty(dest) || string.IsNullOrEmpty(pass))
        {
            PostStatus("请填写归档保存路径与加密口令");
            return;
        }
        try
        {
            var res = await _services.Ipc.InvokeAsync("ExportHistoryArchive", new { destPath = dest, passphrase = pass });
            var status = res.TryGetProperty("status", out var prop) ? prop.GetString() ?? "" : res.ToString();
            _ui.Post(() =>
            {
                ArchiveStatus = $"📤 归档导出完成：{status}";
                OnPropertyChanged(nameof(ArchiveStatus));
            });
            PostStatus($"加密归档已导出：{status}");
        }
        catch (Exception ex)
        {
            PostStatus($"导出加密归档失败：{ex.Message}");
        }
    }

    /// <summary>从全量加密归档包导入恢复（契约 v1.7 ImportHistoryArchive RPC）。</summary>
    [RelayCommand]
    private async Task ImportArchiveAsync()
    {
        var src = ArchivePath.Trim();
        var pass = ArchivePassphrase.Trim();
        if (string.IsNullOrEmpty(src) || string.IsNullOrEmpty(pass))
        {
            PostStatus("请填写归档文件路径与解密口令");
            return;
        }
        try
        {
            var res = await _services.Ipc.InvokeAsync("ImportHistoryArchive", new { srcPath = src, passphrase = pass });
            var status = res.TryGetProperty("status", out var prop) ? prop.GetString() ?? "" : res.ToString();
            _ui.Post(() =>
            {
                ArchiveStatus = $"📥 归档恢复完成：{status}";
                OnPropertyChanged(nameof(ArchiveStatus));
            });
            PostStatus($"加密归档已恢复：{status}");
        }
        catch (Exception ex)
        {
            PostStatus($"导入加密归档失败：{ex.Message}");
        }
    }

    private void PostStatus(string message) =>
        _ui.Post(() =>
        {
            StatusMessage = message;
            OnPropertyChanged(nameof(StatusMessage));
        });
}
