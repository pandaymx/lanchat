namespace LANChat.Models;

/// <summary>G2 自定义频道（schema Channel）。</summary>
public sealed record Channel
{
    public string Id { get; init; } = "";
    public string Name { get; init; } = "";
    public string OwnerId { get; init; } = "";
    public bool Private { get; init; }
    public string Topic { get; init; } = "";
    public IReadOnlyList<string> Members { get; init; } = [];
    public IReadOnlyList<string> Admins { get; init; } = [];
    public IReadOnlyList<string> Muted { get; init; } = [];
    public IReadOnlyList<string> AnnouncementAck { get; init; } = [];
}
