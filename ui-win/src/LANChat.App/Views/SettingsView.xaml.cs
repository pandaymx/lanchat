using LANChat.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Storage.Pickers;
using WinRT.Interop;

namespace LANChat.Views;

/// <summary>设置页：选择默认下载目录需要窗口 HWND 初始化 FolderPicker。</summary>
public sealed partial class SettingsView : UserControl
{
    private ShellViewModel? _shell;
    private MainWindow? _mainWindow;

    public SettingsView()
    {
        InitializeComponent();
    }

    /// <summary>由 MainWindow 在 Shell 就绪后调用。</summary>
    public void AttachViewModel(ShellViewModel shell, MainWindow mainWindow)
    {
        _shell = shell;
        _mainWindow = mainWindow;
        DataContext = shell;
    }

    private void Back_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null)
        {
            _shell.ShowSettings = false;
        }
    }

    private async void BrowseDir_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is null || _mainWindow is null)
        {
            return;
        }

        var picker = new FolderPicker
        {
            SuggestedStartLocation = PickerLocationId.Downloads,
        };
        picker.FileTypeFilter.Add("*");
        InitializeWithWindow.Initialize(picker, WindowNative.GetWindowHandle(_mainWindow));

        var folder = await picker.PickSingleFolderAsync();
        if (folder is not null)
        {
            _shell.Settings.SetDownloadDir(folder.Path);
        }
    }
}
