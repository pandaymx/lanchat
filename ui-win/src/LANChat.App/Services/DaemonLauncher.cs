using System.IO.Pipes;
using System.Text.Json;
using System.Text.Json.Serialization;
using Windows.ApplicationModel;
using Windows.Storage;

namespace LANChat.Services;

/// <summary>
/// 确保本机 lanchat-daemon.exe 在运行。按安装形态分两条路径：
/// <para>
/// 【MSIX / 有包标识】打包后无法用 Process.Start 直接拉起包内 exe，故：
/// 1) 把 daemon 参数写入共享的 LocalState\daemon-config.json；
/// 2) 用 FullTrustProcessLauncher 拉起包内 LANChat.Launcher.exe（完全信任）；
/// 3) Launcher 读取配置再以命令行方式启动 daemon（Go 侧零改动）。
/// </para>
/// <para>
/// 【MSI / 便携 zip / unpackaged】进程本身就有完全信任权限，
/// 直接 Process.Start 同目录下的 lanchat-daemon.exe，参数走命令行，
/// 不碰 ApplicationData / FullTrustProcessLauncher 这类要求包标识的 WinRT API。
/// </para>
/// </summary>
public sealed class DaemonLauncher : IDisposable
{
    private const string ConfigFile = "daemon-config.json";
    private const string ParameterGroupId = "lanchat";
    private const string DaemonExeName = "lanchat-daemon.exe";

    /// <summary>等待管道就绪的轮询次数（约 10 秒）。</summary>
    private const int PipeReadyAttempts = 100;

    private readonly string _pipePath;
    private bool _launchedByUs;
    private System.Diagnostics.Process? _ownedDaemon;

    /// <summary>daemon 是否由本进程拉起（决定退出时能否随 UI 关闭）。</summary>
    public bool LaunchedByUs => _launchedByUs;

    public DaemonLauncher(string pipePath = @"\\.\pipe\lanchat")
    {
        _pipePath = pipePath;
    }

    /// <summary>管道已通则直接返回；否则拉起 daemon（MSIX 走完全信任启动器，MSI 直接起进程），等待管道就绪。</summary>
    public async Task EnsureRunningAsync(Settings settings, CancellationToken cancellationToken = default)
    {
        if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
        {
            return;
        }

        if (PackageIdentity.IsPackaged)
        {
            await LaunchViaFullTrustProcessAsync(settings).ConfigureAwait(false);
        }
        else
        {
            LaunchDirectly(settings);
        }

        _launchedByUs = true;

        if (await WaitForPipeAsync(cancellationToken).ConfigureAwait(false))
        {
            return;
        }

        throw new TimeoutException(DaemonNotReadyMessage());
    }

    /// <summary>MSIX 路径：写共享配置，再用完全信任启动器拉起 daemon。</summary>
    private static async Task LaunchViaFullTrustProcessAsync(Settings settings)
    {
        await WriteConfigAsync(settings).ConfigureAwait(false);

        try
        {
            await FullTrustProcessLauncher
                .LaunchFullTrustProcessForCurrentAppAsync(ParameterGroupId)
                .AsTask()
                .ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            throw new InvalidOperationException($"无法启动完全信任后台进程：{ex.Message}", ex);
        }
    }

    /// <summary>unpackaged 路径：直接以命令行方式启动安装目录内的 daemon。</summary>
    private void LaunchDirectly(Settings settings)
    {
        var daemonPath = Path.Combine(AppContext.BaseDirectory, DaemonExeName);
        if (!File.Exists(daemonPath))
        {
            throw new InvalidOperationException(
                $"未找到后台进程 {DaemonExeName}（预期位置：{daemonPath}）。安装包内容不完整，请重新安装。");
        }

        var startInfo = new System.Diagnostics.ProcessStartInfo
        {
            FileName = daemonPath,
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(daemonPath),
        };
        ApplyDaemonArguments(startInfo, settings);

        try
        {
            _ownedDaemon = System.Diagnostics.Process.Start(startInfo)
                ?? throw new InvalidOperationException($"无法启动 {DaemonExeName}。");
        }
        catch (Exception ex)
        {
            throw new InvalidOperationException($"无法启动后台进程 {DaemonExeName}：{ex.Message}", ex);
        }
    }

    /// <summary>把 UI 侧配置翻译成 daemon 的命令行参数（两条路径共用同一套参数）。</summary>
    private void ApplyDaemonArguments(System.Diagnostics.ProcessStartInfo startInfo, Settings settings)
    {
        startInfo.ArgumentList.Add("--socket");
        startInfo.ArgumentList.Add(string.IsNullOrWhiteSpace(_pipePath) ? @"\\.\pipe\lanchat" : _pipePath);

        if (!string.IsNullOrWhiteSpace(settings.Nickname))
        {
            startInfo.ArgumentList.Add("--nickname");
            startInfo.ArgumentList.Add(settings.Nickname);
        }
        if (!string.IsNullOrWhiteSpace(settings.DownloadDir))
        {
            startInfo.ArgumentList.Add("--download-dir");
            startInfo.ArgumentList.Add(settings.DownloadDir);
        }
    }

    /// <summary>等待管道出现（最多约 10 秒）。</summary>
    private async Task<bool> WaitForPipeAsync(CancellationToken cancellationToken)
    {
        for (var i = 0; i < PipeReadyAttempts; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
            {
                return true;
            }
            await Task.Delay(100, cancellationToken).ConfigureAwait(false);
        }
        return false;
    }

    /// <summary>超时后给出尽可能具体的原因：daemon 若已崩溃，把退出码一并报出来。</summary>
    private string DaemonNotReadyMessage()
    {
        try
        {
            if (_ownedDaemon is { HasExited: true } proc)
            {
                return $"lanchat-daemon 启动后立即退出（退出码 {proc.ExitCode}），请检查安装是否完整或防火墙设置。";
            }
        }
        catch (InvalidOperationException)
        {
            // 进程信息不可用，回退到通用提示
        }
        return "等待 lanchat-daemon 管道就绪超时";
    }

    /// <summary>把 daemon 参数写入 UI 与 Launcher 共享的 LocalState 配置文件（仅 MSIX 需要）。</summary>
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
