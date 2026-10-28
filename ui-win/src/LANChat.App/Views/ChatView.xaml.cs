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
        shell.Chat.StickerPickRequested += OnStickerPickRequested;
        shell.Chat.IncomingFileOffer += OnIncomingFileOffer;
    }

    private async void JoinChannelButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell?.Chat.CurrentChannel is { } channel)
        {
            await _shell.Channels.JoinAsync(channel);
        }
    }

    private async void LeaveChannelButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell?.Chat.CurrentChannel is { } channel && channel.Id != "*")
        {
            try
            {
                await _shell.Channels.LeaveAsync(channel);
            }
            catch
            {
                // 退出失败忽略
            }
        }
    }

    private void ReplyMessageButton_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: ChatMessage msg })
        {
            _shell?.Chat.PrepareReply(msg);
            DraftBox.Focus(FocusState.Programmatic);
        }
    }

    private void ReplyMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (sender is MenuFlyoutItem { Tag: ChatMessage msg })
        {
            _shell?.Chat.PrepareReply(msg);
            DraftBox.Focus(FocusState.Programmatic);
        }
    }

    private void CancelReplyButton_Click(object sender, RoutedEventArgs e)
    {
        _shell?.Chat.CancelReply();
        DraftBox.Focus(FocusState.Programmatic);
    }

    private void ReEditMessageButton_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: ChatMessage msg })
        {
            _shell?.Chat.ReEditMessage(msg);
            DraftBox.Focus(FocusState.Programmatic);
            DraftBox.SelectionStart = DraftBox.Text.Length;
        }
    }

    private void CopyMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (sender is MenuFlyoutItem { Tag: ChatMessage msg } && !string.IsNullOrEmpty(msg.BodyText))
        {
            var pkg = new Windows.ApplicationModel.DataTransfer.DataPackage();
            pkg.SetText(msg.BodyText);
            Windows.ApplicationModel.DataTransfer.Clipboard.SetContent(pkg);
        }
    }

    private async void RevokeMessageButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && sender is Button { Tag: ChatMessage msg })
        {
            await _shell.Chat.RevokeMessageByUiAsync(msg);
        }
    }

    private async void RevokeMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && sender is MenuFlyoutItem { Tag: ChatMessage msg })
        {
            await _shell.Chat.RevokeMessageByUiAsync(msg);
        }
    }

    private async void ReactMessageButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && sender is Button { Tag: ChatMessage msg })
        {
            await _shell.Chat.ReactMessageByUiAsync(msg, "👍");
        }
    }

    private async void ReactMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && sender is MenuFlyoutItem { Tag: ChatMessage msg })
        {
            await _shell.Chat.ReactMessageByUiAsync(msg, "👍");
        }
    }

    private async void PinMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && sender is MenuFlyoutItem { Tag: ChatMessage msg })
        {
            await _shell.Chat.PinMessageByUiAsync(msg);
        }
    }

    private async void EditMenuFlyoutItem_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is null || sender is not MenuFlyoutItem { Tag: ChatMessage msg })
        {
            return;
        }
        var editBox = new TextBox
        {
            Text = msg.BodyText,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            PlaceholderText = "输入修改后的消息内容…",
        };
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "✏️ 二次编辑消息",
            Content = editBox,
            PrimaryButtonText = "保存修改",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Primary,
        };
        var result = await dialog.ShowAsync();
        if (result == ContentDialogResult.Primary && !string.IsNullOrWhiteSpace(editBox.Text))
        {
            await _shell.Chat.EditMessageByUiAsync(msg, editBox.Text.Trim());
        }
    }

    private async void LoadHistoryPageButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null)
        {
            await _shell.Chat.LoadMoreHistoryPageAsync(30);
        }
    }

    private async void VoiceButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null)
        {
            await _shell.Chat.SendVoiceAsync();
        }
    }

    private async void FingerprintButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null)
        {
            await _shell.Chat.VerifyFingerprintAsync();
        }
    }

    private void EmojiItem_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: string emoji })
        {
            _shell?.Chat.InsertEmoji(emoji);
            EmojiFlyout.Hide();
            DraftBox.Focus(FocusState.Programmatic);
            DraftBox.SelectionStart = DraftBox.Text.Length;
        }
    }

    private async void ToggleE2eeButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null)
        {
            await _shell.Chat.ToggleE2eeAsync();
        }
    }

    private async void ScreenShareImage_Tapped(object sender, TappedRoutedEventArgs e)
    {
        if (_shell is not null && sender is FrameworkElement elem)
        {
            var pt = e.GetPosition(elem);
            var w = Math.Max(1.0, elem.ActualWidth);
            var h = Math.Max(1.0, elem.ActualHeight);
            await _shell.Chat.AnnotateScreenAtAsync(pt.X / w, pt.Y / h);
        }
    }

    private void SearchMessagesButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is not null && !string.IsNullOrWhiteSpace(SearchHistoryBox.Text))
        {
            _shell.Chat.ExecuteSearchQuery(SearchHistoryBox.Text);
            SearchHistoryBox.Text = "";
        }
    }

    private async void MembersButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is null || _shell.Chat.CurrentChannel is not { } channel)
        {
            return;
        }

        var panel = new StackPanel { Spacing = 6 };
        if (!string.IsNullOrEmpty(channel.Topic))
        {
            panel.Children.Add(new TextBlock { Text = $"主题：{channel.Topic}", Opacity = 0.75 });
        }

        foreach (var member in _shell.Chat.CurrentMembers)
        {
            panel.Children.Add(new TextBlock { Text = member });
        }

        var inviteBox = new TextBox
        {
            PlaceholderText = "输入要邀请的成员 ID（仅频道所有者可用）",
        };
        if (channel.OwnerId == _shell.SelfId)
        {
            panel.Children.Add(inviteBox);
        }

        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = channel.Private ? $"频道成员（私有 · {channel.Name}）" : $"频道成员（{channel.Name}）",
            Content = new ScrollViewer { Content = panel, MaxHeight = 320 },
            PrimaryButtonText = channel.OwnerId == _shell.SelfId ? "邀请" : "",
            SecondaryButtonText = "退出频道",
            CloseButtonText = "关闭",
        };
        var result = await dialog.ShowAsync();
        if (result == ContentDialogResult.Primary && !string.IsNullOrWhiteSpace(inviteBox.Text))
        {
            try
            {
                await _shell.Channels.InviteAsync(channel, inviteBox.Text.Trim());
            }
            catch
            {
                // 邀请失败忽略
            }
        }
        else if (result == ContentDialogResult.Secondary)
        {
            try
            {
                await _shell.Channels.LeaveAsync(channel);
            }
            catch
            {
                // 退出失败忽略
            }
        }
    }

    private void DraftBox_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            _ = _shell?.Chat.SendCommand.ExecuteAsync(null);
            e.Handled = true;
        }
    }

    private async void OnStickerPickRequested()
    {
        if (_shell is null || _mainWindow is null)
        {
            return;
        }

        var picker = new FileOpenPicker
        {
            ViewMode = PickerViewMode.Thumbnail,
            SuggestedStartLocation = PickerLocationId.PicturesLibrary,
        };
        picker.FileTypeFilter.Add(".png");
        picker.FileTypeFilter.Add(".jpg");
        picker.FileTypeFilter.Add(".jpeg");
        picker.FileTypeFilter.Add(".gif");
        picker.FileTypeFilter.Add(".webp");
        InitializeWithWindow.Initialize(picker, WindowNative.GetWindowHandle(_mainWindow));

        var file = await picker.PickSingleFileAsync();
        if (file is not null)
        {
            await _shell.Chat.SendStickerAsync(file.Path);
        }
    }

    private async void OnFilePickRequested()
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

    private async void PickFolderButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is null || _mainWindow is null || !_shell.Chat.CanSend)
        {
            return;
        }

        var folderPicker = new FolderPicker
        {
            SuggestedStartLocation = PickerLocationId.DocumentsLibrary,
        };
        folderPicker.FileTypeFilter.Add("*");
        InitializeWithWindow.Initialize(folderPicker, WindowNative.GetWindowHandle(_mainWindow));

        var folder = await folderPicker.PickSingleFolderAsync();
        if (folder is not null && !string.IsNullOrEmpty(folder.Path))
        {
            await _shell.Chat.OfferFileAsync(folder.Path);
        }
    }

    private async void PasteClipboardButton_Click(object sender, RoutedEventArgs e)
    {
        if (_shell is null || !_shell.Chat.CanSend)
        {
            return;
        }
        try
        {
            var dataView = Windows.ApplicationModel.DataTransfer.Clipboard.GetContent();
            if (dataView.Contains(Windows.ApplicationModel.DataTransfer.StandardDataFormats.StorageItems))
            {
                var items = await dataView.GetStorageItemsAsync();
                foreach (var item in items)
                {
                    if (!string.IsNullOrEmpty(item.Path))
                    {
                        await _shell.Chat.OfferFileAsync(item.Path);
                    }
                }
                return;
            }
            if (dataView.Contains(Windows.ApplicationModel.DataTransfer.StandardDataFormats.Bitmap))
            {
                var streamRef = await dataView.GetBitmapAsync();
                using var stream = await streamRef.OpenReadAsync();
                using var mem = new System.IO.MemoryStream();
                await System.IO.WindowsRuntimeStreamExtensions.AsStreamForRead(stream).CopyToAsync(mem);
                var b64 = System.Convert.ToBase64String(mem.ToArray());
                if (!string.IsNullOrEmpty(b64))
                {
                    await _shell.Chat.OfferFileAsync($"data:image/png;base64,{b64}");
                    return;
                }
            }
            if (dataView.Contains(Windows.ApplicationModel.DataTransfer.StandardDataFormats.Text))
            {
                var text = await dataView.GetTextAsync();
                if (!string.IsNullOrWhiteSpace(text))
                {
                    _shell.Chat.Draft = $"/clip {text.Trim()}";
                    await _shell.Chat.SendCommand.ExecuteAsync(null);
                }
            }
        }
        catch
        {
            // 剪贴板为空或无权限时忽略
        }
    }

    private void ChatView_DragOver(object sender, DragEventArgs e)
    {
        if (_shell?.Chat.CanSend == true && e.DataView.Contains(Windows.ApplicationModel.DataTransfer.StandardDataFormats.StorageItems))
        {
            e.AcceptedOperation = Windows.ApplicationModel.DataTransfer.DataPackageOperation.Copy;
            e.DragUIOverride.Caption = e.Modifiers.HasFlag(Windows.ApplicationModel.DataTransfer.DragDrop.DragDropModifiers.Shift)
                ? "松开鼠标增量同步文件夹 (SyncFolder)"
                : "松开鼠标直传文件或文件夹 (.lanchat-dir.tar.gz)";
        }
    }

    private async void ChatView_Drop(object sender, DragEventArgs e)
    {
        if (_shell is null || !_shell.Chat.CanSend)
        {
            return;
        }
        if (!e.DataView.Contains(Windows.ApplicationModel.DataTransfer.StandardDataFormats.StorageItems))
        {
            return;
        }
        var shiftSync = e.Modifiers.HasFlag(Windows.ApplicationModel.DataTransfer.DragDrop.DragDropModifiers.Shift);
        var items = await e.DataView.GetStorageItemsAsync();
        foreach (var item in items)
        {
            if (!string.IsNullOrEmpty(item.Path))
            {
                if (shiftSync && item is Windows.Storage.IStorageFolder)
                {
                    await _shell.Chat.SyncFolderAsync(item.Path);
                }
                else
                {
                    await _shell.Chat.OfferFileAsync(item.Path);
                }
            }
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
            Title = transfer.IsFolderBundle ? "📁 接收文件夹（自动解包还原）" : "接收文件",
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
