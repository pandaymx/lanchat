using System.IO.Pipes;
using System.Text.Json;
using System.Text.Json.Serialization;
using Windows.ApplicationModel;
using Windows.Storage;

namespace LANChat.Services;

/// <summary>
/// MSIX 模式下确保本机 lanchat-daemon.exe 在运行。
/// 打包后无法用 Process.Start 直接拉起包内 exe，故：
/// 1) 把 daemon 参数写入共享的 LocalState\daemon-config.json；
/// 2) 用 FullTrustProcessLauncher 拉起包内 LANChat.Launcher.exe（完全信任）；
/// 3) Launcher 读取配置再以命令行方式启动 daemon（Go 侧零改动）。
/// </summary>
public sealed class DaemonLauncher : IDisposable
{
    private const string ConfigFile = "daemon-config.json";
    private const string ParameterGroupId = "lanchat";

    private readonly string _pipePath;
    private bool _launchedByUs;
    private System.Diagnostics.Process? _ownedDaemon;

    /// <summary>daemon 是否由本进程拉起（决定退出时能否随 UI 关闭）。</summary>
    public bool LaunchedByUs => _launchedByUs;

    public DaemonLauncher(string pipePath = @"\\.\pipe\lanchat")
    {
        _pipePath = pipePath;
    }

    /// <summary>管道已通则直接返回；否则写配置并经完全信任启动器拉起 daemon，等待管道就绪。</summary>
    public async Task EnsureRunningAsync(Settings settings, CancellationToken cancellationToken = default)
    {
        if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
        {
            return;
        }

        await WriteConfigAsync(settings).ConfigureAwait(false);

        try
        {
            await FullTrustProcessLauncher.LaunchFullTrustProcessForCurrentAppAsync(ParameterGroupId).AsTask().ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            throw new InvalidOperationException($"无法启动完全信任后台进程：{ex.Message}", ex);
        }

        _launchedByUs = true;

        // 等待管道出现（最多约 10 秒）
        for (var i = 0; i < 100; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
            {
                return;
            }
            await Task.Delay(100, cancellationToken).ConfigureAwait(false);
        }

        throw new TimeoutException("等待 lanchat-daemon 管道就绪超时");
    }

    /// <summary>把 daemon 参数写入 UI 与 Launcher 共享的 LocalState 配置文件。</summary>
    private static async Task WriteConfigAsync(Settings settings)
    {
        var config = new DaemonConfig
        {
            Socket = @"\\.\pipe\lanchat",
            Nickname = settings.Nickname ?? "",
            DownloadDir = settings.DownloadDir ?? "",
        };
        var folder = ApplicationData.Current.LocalFolder;
        var file = await folder.CreateFileAsync(ConfigFile, CreationCollisionOption.ReplaceExisting).AsTask().ConfigureAwait(false);
        await Windows.Storage.FileIO.WriteTextAsync(file, JsonSerializer.Serialize(config)).AsTask().ConfigureAwait(false);
    }

    /// <summary>探测命名管道是否可连接（不保持连接）。</summary>
    private async Task<bool> IsPipeAvailableAsync(int timeoutMs, CancellationToken cancellationToken)
    {
        var pipeName = _pipePath.Replace(@"\\.\pipe\", "");
        try
        {
            using var pipe = new NamedPipeClientStream(
                ".", pipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
            await pipe.ConnectAsync(timeoutMs, cancellationToken).ConfigureAwait(false);
            return pipe.IsConnected;
        }
        catch
        {
            return false;
        }
    }

    /// <summary>关闭由本进程拉起的 daemon（设置中选择「退出并关闭后台」时调用）。</summary>
    public void StopDaemonIfOwned()
    {
        if (!_launchedByUs)
        {
            return;
        }

        foreach (var proc in System.Diagnostics.Process.GetProcessesByName("lanchat-daemon"))
        {
            try
            {
                proc.Kill(entireProcessTree: true);
                proc.WaitForExit(3000);
            }
            catch
            {
                // 进程可能已退出
            }
            finally
            {
                proc.Dispose();
            }
        }
    }

    public void Dispose()
    {
        _ownedDaemon?.Dispose();
        _ownedDaemon = null;
    }

    private sealed class DaemonConfig
    {
        [JsonPropertyName("socket")]
        public string Socket { get; set; } = "";

        [JsonPropertyName("nickname")]
        public string Nickname { get; set; } = "";

        [JsonPropertyName("downloadDir")]
        public string DownloadDir { get; set; } = "";
    }
}
