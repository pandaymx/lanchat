using LANChat.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace LANChat.Views;

/// <summary>联系人/频道列表；底部按钮切换设置页。</summary>
public sealed partial class RosterView : UserControl
{
    public RosterView()
    {
        InitializeComponent();
    }

    private void SettingsButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is ShellViewModel shell)
        {
            shell.ShowSettings = true;
        }
    }

    private async void NewChannelButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is not ShellViewModel shell)
        {
            return;
        }

        var nameBox = new TextBox
        {
            PlaceholderText = "频道名称",
            MaxLength = 64,
        };
        var topicBox = new TextBox
        {
            PlaceholderText = "频道主题（可选）",
            MaxLength = 128,
        };
        var privateCheck = new CheckBox
        {
            Content = "私有频道（仅邀请可加入）",
        };
        var form = new StackPanel { Spacing = 8 };
        form.Children.Add(nameBox);
        form.Children.Add(topicBox);
        form.Children.Add(privateCheck);

        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "新建频道",
            Content = form,
            PrimaryButtonText = "创建",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Primary,
        };

        var result = await dialog.ShowAsync();
        if (result != ContentDialogResult.Primary)
        {
            return;
        }

        var name = nameBox.Text.Trim();
        if (string.IsNullOrEmpty(name))
        {
            return;
        }

        try
        {
            await shell.Channels.CreateAsync(name, topicBox.Text.Trim(), privateCheck.IsChecked == true);
        }
        catch
        {
            // channel.updated 未到达时不做额外提示；后续通知或重试可见
        }
    }

    private void BroadcastSessionButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is ShellViewModel shell)
        {
            shell.Roster.SelectedPeer = null;
            shell.Channels.SelectedChannel = null;
            shell.ShowSettings = false;
            shell.Chat.SelectBroadcastConversation();
        }
    }

    private async void RefreshChannelsButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is not ShellViewModel shell)
        {
            return;
        }
        try
        {
            var list = await shell.Channels.ListAsync();
            shell.Channels.ReplaceChannels(list, shell.SelfId);
        }
        catch
        {
            // 刷新失败保留原列表
        }
    }

    private async void JoinChannelButton_Click(object sender, RoutedEventArgs e)
    {
        if (DataContext is ShellViewModel shell &&
            sender is Button { Tag: ChannelItem item })
        {
            await shell.Channels.JoinAsync(item.Channel);
        }
    }
}
