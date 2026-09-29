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
        PostStatus("下载目录已保存");
    }

    internal void SetDownloadDir(string path)
    {
        DownloadDir = path;
        SaveDownloadDir();
    }

    private void PostStatus(string message) =>
        _ui.Post(() =>
        {
            StatusMessage = message;
            OnPropertyChanged(nameof(StatusMessage));
        });
}
