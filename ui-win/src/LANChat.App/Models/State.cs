namespace LANChat.Models;

/// <summary>GetState 返回的历史聊天消息（schema ChatMessage v1.1）。</summary>
public sealed record HistoryMessage
{
    public string Id { get; init; } = "";
    public string From { get; init; } = "";
    public string? To { get; init; }
    public string? Group { get; init; }
    public string Type { get; init; } = "text";
    public string Text { get; init; } = "";
    public string? ReplyTo { get; init; }
    public string? ReplyQuote { get; init; }
    public IReadOnlyList<string> Mentions { get; init; } = [];
    public IReadOnlyDictionary<string, IReadOnlyList<string>>? Reactions { get; init; }
    public long EditedAt { get; init; }
    public bool Pinned { get; init; }
    public string? MediaMime { get; init; }
    public string? MediaPath { get; init; }
    public long Ts { get; init; }
    public bool Revoked { get; init; }
    public IReadOnlyList<string> ReadBy { get; init; } = [];
}

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
    public IReadOnlyList<HistoryMessage> Messages { get; init; } = [];
}
