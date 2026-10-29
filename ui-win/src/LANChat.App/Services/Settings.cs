using System.Text.Json;

namespace LANChat.Services;

/// <summary>本地配置：昵称、默认下载目录等（JSON 存于 %APPDATA%\LANChat）。</summary>
public sealed class Settings
{
    public static string DefaultDownloadDir =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "Downloads", "LANChat");

    public string Nickname { get; set; } = "";
    public string DownloadDir { get; set; } = DefaultDownloadDir;

    private static string ConfigDir =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "LANChat");

    private static string ConfigPath => Path.Combine(ConfigDir, "settings.json");

    public static Settings Load()
    {
        try
        {
            if (File.Exists(ConfigPath))
            {
                var loaded = JsonSerializer.Deserialize<Settings>(File.ReadAllText(ConfigPath)) ?? new Settings();
                if (string.IsNullOrWhiteSpace(loaded.DownloadDir))
                {
                    loaded.DownloadDir = DefaultDownloadDir;
                }
                return loaded;
            }
        }
        catch (JsonException)
        {
            // 配置损坏时回退默认值
        }
        catch (IOException)
        {
            // 读取失败时回退默认值
        }
        return new Settings();
    }

    public void Save()
    {
        Directory.CreateDirectory(ConfigDir);
        File.WriteAllText(ConfigPath, JsonSerializer.Serialize(this, new JsonSerializerOptions
        {
            WriteIndented = true,
        }));
    }
}
