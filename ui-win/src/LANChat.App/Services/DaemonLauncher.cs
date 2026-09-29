using System.Diagnostics;
using System.ComponentModel;
using System.IO.Pipes;

namespace LANChat.Services;

/// <summary>
/// 确保本机 lanchat-daemon.exe 在运行：管道不可达时拉起进程。
/// daemon 可常驻；仅本进程拉起的实例可在退出时一并关闭。
/// </summary>
public sealed class DaemonLauncher : IDisposable
{
    private const string DaemonExe = "lanchat-daemon.exe";

    private readonly string _pipePath;
    private Process? _process;

    /// <summary>daemon 是否由本进程拉起（决定退出时能否随 UI 关闭）。</summary>
    public bool LaunchedByUs => _process is not null;

    public DaemonLauncher(string pipePath = @"\\.\pipe\lanchat")
    {
        _pipePath = pipePath;
    }

    /// <summary>管道已通则直接返回；否则拉起 daemon 并等待管道就绪。</summary>
    public async Task EnsureRunningAsync(Settings settings, CancellationToken cancellationToken = default)
    {
        if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
        {
            return;
        }

        var exePath = ResolveDaemonPath();
        var startInfo = new ProcessStartInfo
        {
            FileName = exePath,
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(exePath),
        };
        startInfo.ArgumentList.Add("--socket");
        startInfo.ArgumentList.Add(_pipePath);
        if (!string.IsNullOrEmpty(settings.Nickname))
        {
            startInfo.ArgumentList.Add("--nickname");
            startInfo.ArgumentList.Add(settings.Nickname);
        }
        if (!string.IsNullOrEmpty(settings.DownloadDir))
        {
            startInfo.ArgumentList.Add("--download-dir");
            startInfo.ArgumentList.Add(settings.DownloadDir);
        }

        try
        {
            _process = Process.Start(startInfo);
        }
        catch (Win32Exception ex)
        {
            throw new InvalidOperationException($"无法启动 {DaemonExe}：{ex.Message}", ex);
        }

        if (_process is null)
        {
            throw new InvalidOperationException($"无法启动 {DaemonExe}");
        }

        _process.EnableRaisingEvents = true;

        // 等待管道出现（最多约 10 秒）
        for (var i = 0; i < 100; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_process.HasExited)
            {
                throw new InvalidOperationException($"{DaemonExe} 启动后立即退出，退出代码 {_process.ExitCode}");
            }
            if (await IsPipeAvailableAsync(500, cancellationToken).ConfigureAwait(false))
            {
                return;
            }
            await Task.Delay(100, cancellationToken).ConfigureAwait(false);
        }

        throw new TimeoutException($"等待 {DaemonExe} 管道就绪超时");
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

    /// <summary>解析 daemon 路径：环境变量 → EXE 同目录 → 工作目录。</summary>
    private static string ResolveDaemonPath()
    {
        var envPath = Environment.GetEnvironmentVariable("LANCHAT_DAEMON_PATH");
        string[] candidates =
        [
            envPath ?? "",
            Path.Combine(AppContext.BaseDirectory, DaemonExe),
            Path.Combine(Environment.CurrentDirectory, DaemonExe),
        ];

        foreach (var candidate in candidates)
        {
            if (!string.IsNullOrEmpty(candidate) && File.Exists(candidate))
            {
                return candidate;
            }
        }

        throw new FileNotFoundException(
            $"未找到 {DaemonExe}，可设置环境变量 LANCHAT_DAEMON_PATH 指定其位置");
    }

    /// <summary>关闭由本进程拉起的 daemon（设置中选择「退出并关闭后台」时调用）。</summary>
    public void StopDaemonIfOwned()
    {
        if (_process is null || _process.HasExited)
        {
            return;
        }
        _process.Kill(entireProcessTree: true);
        _process.WaitForExit(3000);
    }

    public void Dispose()
    {
        _process?.Dispose();
        _process = null;
    }
}
