namespace LANChat.Models;

/// <summary>在线用户（schema Peer）。</summary>
public sealed record Peer
{
    public string Id { get; init; } = "";
    public string Nickname { get; init; } = "";
    public string Os { get; init; } = "";
    public string Status { get; init; } = "";
}
