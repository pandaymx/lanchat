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
        Title = "LANChat";
        AppWindow.Resize(new Windows.Graphics.SizeInt32(960, 640));
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

    private void OnShellPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (_shell is null || e.PropertyName is not (nameof(ShellViewModel.IsConnected) or nameof(ShellViewModel.ShowLogin)))
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
