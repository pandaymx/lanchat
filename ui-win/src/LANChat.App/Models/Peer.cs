namespace LANChat.Models;

/// <summary>在线用户（schema Peer）。</summary>
public sealed record Peer
{
    public string Id { get; init; } = "";
    public string Nickname { get; init; } = "";
    public string Os { get; init; } = "";
    public string Status { get; init; } = "";
    public int UnreadCount { get; init; }

    /// <summary>圆形头像首字。</summary>
    public string AvatarInitial =>
        string.IsNullOrWhiteSpace(Nickname) ? "U" : Nickname.Trim()[..1].ToUpperInvariant();

    /// <summary>QQ 风格在线状态与平台副标题。</summary>
    public string Subtitle =>
        string.IsNullOrWhiteSpace(Status)
            ? $"[在线] {(string.IsNullOrWhiteSpace(Os) ? "LAN" : Os)}"
            : $"[在线] {Os} · {Status}";

    public bool HasUnread => UnreadCount > 0;

    public string UnreadBadgeText => UnreadCount > 99 ? "99+" : UnreadCount.ToString();
}
