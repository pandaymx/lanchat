using System.Text.Json;

namespace LANChat.Services;

/// <summary>本地配置：昵称、默认下载目录等（JSON 存于 %APPDATA%\LANChat）。</summary>
public sealed class Settings
{
    public string Nickname { get; set; } = "";
    public string DownloadDir { get; set; } = "";

    private static string ConfigDir =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "LANChat");

    private static string ConfigPath => Path.Combine(ConfigDir, "settings.json");

    public static Settings Load()
    {
        try
        {
            if (File.Exists(ConfigPath))
            {
                return JsonSerializer.Deserialize<Settings>(File.ReadAllText(ConfigPath)) ?? new Settings();
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
