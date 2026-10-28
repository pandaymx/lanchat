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

    public SettingsViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
        Nickname = services.Settings.Nickname;
        DownloadDir = services.Settings.DownloadDir;
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

    private void PostStatus(string message) =>
        _ui.Post(() =>
        {
            StatusMessage = message;
            OnPropertyChanged(nameof(StatusMessage));
        });
}
