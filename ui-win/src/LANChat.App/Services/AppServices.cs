using LANChat.Ipc;

namespace LANChat.Services;

/// <summary>
/// 全局服务注册中心（轻量手工 DI）：持有 IpcClient、配置与 daemon 启动器。
/// App 启动时初始化，VM 通过 App.Services 访问。
/// </summary>
public sealed class AppServices : IDisposable
{
    public IpcClient Ipc { get; }
    public Settings Settings { get; }
    public DaemonLauncher Daemon { get; }

    private AppServices(IpcClient ipc, Settings settings, DaemonLauncher daemon)
    {
        Ipc = ipc;
        Settings = settings;
        Daemon = daemon;
    }

    /// <summary>创建并初始化全部服务（daemon 拉起 + IPC 连接）。</summary>
    public static async Task<AppServices> CreateAsync(CancellationToken cancellationToken = default)
    {
        var settings = Settings.Load();
        var daemon = new DaemonLauncher();
        var ipc = new IpcClient();
        var services = new AppServices(ipc, settings, daemon);

        await daemon.EnsureRunningAsync(settings, cancellationToken).ConfigureAwait(false);
        await ipc.StartAsync().ConfigureAwait(false);

        return services;
    }

    public void Dispose()
    {
        Ipc.DisposeAsync().AsTask().GetAwaiter().GetResult();
        Daemon.Dispose();
    }
}
