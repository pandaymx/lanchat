namespace LANChat.Models;

/// <summary>G2 自定义频道（schema Channel）。</summary>
public sealed record Channel
{
    public string Id { get; init; } = "";
    public string Name { get; init; } = "";
    public string OwnerId { get; init; } = "";
    public IReadOnlyList<string> Members { get; init; } = [];
}
