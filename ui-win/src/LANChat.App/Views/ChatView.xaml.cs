using LANChat.Models;
using LANChat.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.Storage.Pickers;
using WinRT.Interop;

namespace LANChat.Views;

/// <summary>
/// 聊天区：Enter 发送、文件选择、接收文件确认弹窗。
/// picker 需要窗口 HWND 初始化（解包应用同样适用）。
/// </summary>
public sealed partial class ChatView : UserControl
{
    private ShellViewModel? _shell;
    private MainWindow? _mainWindow;

    public ChatView()
    {
        InitializeComponent();
    }

    /// <summary>由 MainWindow 在 Shell 就绪后调用。</summary>
    public void AttachViewModel(ShellViewModel shell, MainWindow mainWindow)
    {
        _shell = shell;
        _mainWindow = mainWindow;
        DataContext = shell;
        shell.Chat.FilePickRequested += OnFilePickRequested;
        shell.Chat.IncomingFileOffer += OnIncomingFileOffer;
    }

    private void DraftBox_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            _ = _shell?.Chat.SendCommand.ExecuteAsync(null);
            e.Handled = true;
        }
    }

    private async void OnFilePickRequested(Models.Peer peer)
    {
        if (_shell is null || _mainWindow is null)
        {
            return;
        }

        var picker = new FileOpenPicker
        {
            ViewMode = PickerViewMode.List,
            SuggestedStartLocation = PickerLocationId.DocumentsLibrary,
        };
        picker.FileTypeFilter.Add("*");
        InitializeWithWindow.Initialize(picker, WindowNative.GetWindowHandle(_mainWindow));

        var file = await picker.PickSingleFileAsync();
        if (file is not null)
        {
            await _shell.Chat.OfferFileAsync(file.Path);
        }
    }

    private async void OnIncomingFileOffer(Transfer transfer)
    {
        if (_shell is null || _mainWindow is null)
        {
            return;
        }

        // pending 通知可能在首次进入会话前到达；去重已在传输列表处理，弹窗只需应答一次
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "接收文件",
            Content = $"{transfer.Name}（{FormatSize(transfer.Size)}）\n是否接收并选择保存位置？",
            PrimaryButtonText = "接收",
            CloseButtonText = "拒绝",
            DefaultButton = ContentDialogButton.Primary,
        };

        var result = await dialog.ShowAsync();
        if (result != ContentDialogResult.Primary)
        {
            await _shell.Chat.RespondFileAsync(transfer, false, "");
            return;
        }

        var folderPicker = new FolderPicker
        {
            SuggestedStartLocation = PickerLocationId.Downloads,
        };
        folderPicker.FileTypeFilter.Add("*");
        InitializeWithWindow.Initialize(folderPicker, WindowNative.GetWindowHandle(_mainWindow));

        var folder = await folderPicker.PickSingleFolderAsync();
        if (folder is null)
        {
            await _shell.Chat.RespondFileAsync(transfer, false, "");
            return;
        }

        var dest = System.IO.Path.Combine(folder.Path, transfer.Name);
        await _shell.Chat.RespondFileAsync(transfer, true, dest);
    }

    private static string FormatSize(long bytes)
    {
        string[] units = ["B", "KB", "MB", "GB", "TB"];
        double size = bytes;
        var unit = 0;
        while (size >= 1024 && unit < units.Length - 1)
        {
            size /= 1024;
            unit++;
        }
        return $"{size:0.##} {units[unit]}";
    }
}
