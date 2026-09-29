namespace LANChat.Models;

/// <summary>mDNS 浏览到的中心节点候选（schema Server）。</summary>
public sealed record ServerInfo
{
    public string Name { get; init; } = "";
    public string Id { get; init; } = "";
    public string Addr { get; init; } = "";
    public string Version { get; init; } = "";
    public string AuthMode { get; init; } = "";
}
