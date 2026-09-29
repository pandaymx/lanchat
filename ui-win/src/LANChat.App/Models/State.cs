namespace LANChat.Models;

/// <summary>GetState 返回的全量快照（schema State）。</summary>
public sealed record State
{
    public ConnState Conn { get; init; }
    public string? Server { get; init; }
    public string? SelfId { get; init; }
    public string Nickname { get; init; } = "";
    public IReadOnlyList<Peer> Peers { get; init; } = [];
    public IReadOnlyList<Transfer> Transfers { get; init; } = [];
    public IReadOnlyList<Channel> Channels { get; init; } = [];
}
