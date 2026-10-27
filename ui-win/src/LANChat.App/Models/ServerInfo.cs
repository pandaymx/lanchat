namespace LANChat.Models;

/// <summary>mDNS / UDP / Tailscale 浏览到的中心节点候选（schema Server）。</summary>
public sealed record ServerInfo
{
    public string Name { get; init; } = "";
    public string Id { get; init; } = "";
    public string Addr { get; init; } = "";
    public string Version { get; init; } = "";
    public string AuthMode { get; init; } = "";

    public string VersionDisplay => string.IsNullOrWhiteSpace(Version) ? "v1.0" : Version;

    public bool IsTailscale => IsTailscaleAddress(Addr);

    public string LinkBadgeText => IsTailscale ? "🌐 Tailscale 直连" : "⚡ 局域网直连";

    public string AddrWithLinkDisplay => $"{LinkBadgeText} · {Addr}";

    public string AuthBadgeText =>
        string.Equals(AuthMode, "psk", StringComparison.OrdinalIgnoreCase)
            ? "🔒 PSK 鉴权"
            : "🔓 开放节点";

    public static bool IsTailscaleAddress(string? raw)
    {
        if (string.IsNullOrWhiteSpace(raw))
        {
            return false;
        }
        var s = raw.Trim();
        foreach (var prefix in new[] { "ws://", "wss://", "http://", "https://" })
        {
            if (s.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            {
                s = s[prefix.Length..];
                break;
            }
        }
        var slashIdx = s.IndexOf('/');
        if (slashIdx >= 0)
        {
            s = s[..slashIdx];
        }
        var colonIdx = s.IndexOf(':');
        if (colonIdx >= 0)
        {
            s = s[..colonIdx];
        }
        var parts = s.Trim().Split('.');
        return parts.Length == 4
            && byte.TryParse(parts[0], out var a)
            && byte.TryParse(parts[1], out var b)
            && a == 100
            && b >= 64
            && b <= 127;
    }
}
