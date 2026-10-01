using System.Diagnostics;
using System.Text.Json;
using System.Text.Json.Serialization;
using Windows.Storage;

namespace LANChat.Launcher;

/// <summary>
/// MSIX 完全信任入口。FullTrustProcessLauncher 不接受命令行参数，
/// 故 UI 先把 daemon 参数写入 LocalState\daemon-config.json，
/// 本程序读取后以命令行方式拉起包内 lanchat-daemon.exe，随后自身退出。
/// 这样 Go daemon（A1）无需任何改动。
/// </summary>
internal static class Program
{
    private const string DaemonExe = "lanchat-daemon.exe";
    private const string ConfigFile = "daemon-config.json";

    private static void Main()
    {
        try
        {
            var local = ApplicationData.Current.LocalFolder.Path;
            var configPath = Path.Combine(local, ConfigFile);
            var config = LoadConfig(configPath);

            var daemonPath = Path.Combine(AppContext.BaseDirectory, DaemonExe);
            if (!File.Exists(daemonPath))
            {
                Log(local, $"未找到 {DaemonExe}：{daemonPath}");
                return;
            }

            var startInfo = new ProcessStartInfo
            {
                FileName = daemonPath,
                UseShellExecute = false,
                CreateNoWindow = true,
                WorkingDirectory = Path.GetDirectoryName(daemonPath),
            };
            startInfo.ArgumentList.Add("--socket");
            startInfo.ArgumentList.Add(string.IsNullOrWhiteSpace(config.Socket) ? @"\\.\pipe\lanchat" : config.Socket);
            if (!string.IsNullOrWhiteSpace(config.Nickname))
            {
                startInfo.ArgumentList.Add("--nickname");
                startInfo.ArgumentList.Add(config.Nickname);
            }
            if (!string.IsNullOrWhiteSpace(config.DownloadDir))
            {
                startInfo.ArgumentList.Add("--download-dir");
                startInfo.ArgumentList.Add(config.DownloadDir);
            }

            Process.Start(startInfo);
        }
        catch (Exception ex)
        {
            try
            {
                Log(ApplicationData.Current.LocalFolder.Path, ex.ToString());
            }
            catch
            {
                // 已无法记录日志，静默退出
            }
        }
    }

    private static DaemonConfig LoadConfig(string path)
    {
        if (File.Exists(path))
        {
            try
            {
                return JsonSerializer.Deserialize<DaemonConfig>(File.ReadAllText(path)) ?? new DaemonConfig();
            }
            catch (JsonException)
            {
                // 配置损坏时回退默认值
            }
        }
        return new DaemonConfig();
    }

    private static void Log(string dir, string message)
    {
        Directory.CreateDirectory(dir);
        File.AppendAllText(
            Path.Combine(dir, "launcher.log"),
            $"{DateTimeOffset.Now:O} {message}{Environment.NewLine}");
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
