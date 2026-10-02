using LANChat.Services;
using LANChat.ViewModels;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;

namespace LANChat;

/// <summary>
/// 应用程序入口对象。
/// </summary>
public partial class App : Application
{
    private MainWindow? _window;
    private AppServices? _services;
    private ShellViewModel? _shell;

    public App()
    {
        InitializeComponent();
        UnhandledException += OnUnhandledException;
    }

    protected override async void OnLaunched(LaunchActivatedEventArgs args)
    {
        _window = new MainWindow();
        _window.Activate();

        var uiThread = new UiThread(DispatcherQueue.GetForCurrentThread());

        try
        {
            _services = await AppServices.CreateAsync();
        }
        catch (Exception ex)
        {
            _window.ShowFatalError($"初始化失败：{ex.Message}");
            return;
        }

        _shell = new ShellViewModel(_services, uiThread);
        _shell.Chat.PeerResolver = id => _shell.Roster.FindPeer(id);
        _shell.Roster.SelectedPeerChanged += peer => _shell.Chat.SelectConversation(peer);
        _shell.Channels.SelectedChannelChanged += channel => _shell.Chat.SelectChannelConversation(channel);

        _window.Initialize(_shell, uiThread);
        await _shell.InitializeAsync();
    }

    private static void OnUnhandledException(object sender, Microsoft.UI.Xaml.UnhandledExceptionEventArgs args)
    {
        // 防止后台通知解析异常导致进程崩溃
        args.Handled = true;
    }
}
