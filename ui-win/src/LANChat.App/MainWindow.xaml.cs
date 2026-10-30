using System.ComponentModel;
using LANChat.Services;
using LANChat.ViewModels;
using LANChat.Views;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace LANChat;

/// <summary>
/// 主窗口：在登录页与三栏主界面之间切换。
/// </summary>
public sealed partial class MainWindow : Window
{
    private ShellViewModel? _shell;

    public MainWindow()
    {
        InitializeComponent();
        Title = "LANChat 局域网即时通信";
        AppWindow.Resize(new Windows.Graphics.SizeInt32(1180, 760));
        AppWindow.Closing += (_, args) =>
        {
            if (_shell is { IsConnected: true })
            {
                args.Cancel = true;
                _shell.SetTrayResidency(true);
                AppWindow.Hide();
            }
        };
    }

    /// <summary>从系统托盘恢复主窗口并清除后台未读角标（v8.3.0）。</summary>
    public void RestoreFromTray()
    {
        AppWindow.Show();
        Activate();
        _shell?.SetTrayResidency(false);
    }

    /// <summary>分发系统原生通知栏快捷操作（快捷回复 / 接听来电 / 接收文件，v8.3.0）。</summary>
    public async Task HandleNotificationQuickActionAsync(string action, string payload = "")
    {
        if (_shell is null)
        {
            return;
        }
        switch (action)
        {
            case "quick_reply":
                await _shell.QuickReplyLastNotificationAsync(payload);
                break;
            case "accept_call":
                RestoreFromTray();
                await _shell.QuickAcceptIncomingCallAsync();
                break;
            case "accept_file":
                await _shell.QuickAcceptPendingFileAsync(payload);
                break;
        }
    }

    /// <summary>绑定 Shell 并导航到登录页。</summary>
    public void Initialize(ShellViewModel shell, UiThread uiThread)
    {
        _shell = shell;
        RootGrid.DataContext = shell;
        shell.PropertyChanged += OnShellPropertyChanged;

        LoginFrame.Navigate(typeof(LoginPage), shell);

        // 聊天区文件选择/接收确认事件在 View 层处理（需要窗口句柄初始化 picker）
        var chatView = FindDescendant<ChatView>(MainGrid);
        chatView?.AttachViewModel(shell, this);

        var settingsView = FindDescendant<SettingsView>(MainGrid);
        settingsView?.AttachViewModel(shell, this);
    }

    private void NavMessages_Click(object sender, RoutedEventArgs e) =>
        _shell?.SelectNavTab("messages");

    private void NavContacts_Click(object sender, RoutedEventArgs e) =>
        _shell?.SelectNavTab("contacts");

    private void NavChannels_Click(object sender, RoutedEventArgs e) =>
        _shell?.SelectNavTab("channels");

    private void NavTransfers_Click(object sender, RoutedEventArgs e) =>
        _shell?.SelectNavTab("transfers");

    private void NavSettings_Click(object sender, RoutedEventArgs e) =>
        _shell?.SelectNavTab("settings");

    private void OnShellPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (_shell is null)
        {
            return;
        }

        if (e.PropertyName is nameof(ShellViewModel.TrayWindowTitle)
            or nameof(ShellViewModel.UnreadCount)
            or nameof(ShellViewModel.IsTrayResident))
        {
            Title = _shell.TrayWindowTitle;
        }

        if (e.PropertyName is not (nameof(ShellViewModel.IsConnected) or nameof(ShellViewModel.ShowLogin)))
        {
            return;
        }

        LoginFrame.Visibility = _shell.IsConnected ? Visibility.Collapsed : Visibility.Visible;
        MainGrid.Visibility = _shell.IsConnected ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>显示初始化致命错误（如找不到 daemon）。</summary>
    public void ShowFatalError(string message)
    {
        FatalErrorHost.Visibility = Visibility.Visible;
        FatalErrorHost.Content = new TextBlock
        {
            Text = message,
            TextWrapping = TextWrapping.Wrap,
            Foreground = (Brush)Application.Current.Resources["SystemFillColorCriticalBrush"],
        };
    }

    /// <summary>在可视化树中按类型查找后代元素。</summary>
    private static T? FindDescendant<T>(DependencyObject root) where T : DependencyObject
    {
        var count = VisualTreeHelper.GetChildrenCount(root);
        for (var i = 0; i < count; i++)
        {
            var child = VisualTreeHelper.GetChild(root, i);
            if (child is T matched)
            {
                return matched;
            }
            var nested = FindDescendant<T>(child);
            if (nested is not null)
            {
                return nested;
            }
        }
        return null;
    }
}
