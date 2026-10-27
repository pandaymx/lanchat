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

/// <summary>契约 v1.2 实时语音通话状态快照（schema VoiceCallState）。</summary>
public sealed record VoiceCallState
{
    public string State { get; init; } = "idle";
    public string? PeerId { get; init; }
    public string? GroupId { get; init; }
    public string? LocalAddr { get; init; }
    public string? RemoteAddr { get; init; }
    public long FramesIn { get; init; }
    public long FramesOut { get; init; }
}

/// <summary>契约 v1.2 实时屏幕共享状态快照（schema ScreenShareState）。</summary>
public sealed record ScreenShareState
{
    public bool Active { get; init; }
    public string? PeerId { get; init; }
    public string? GroupId { get; init; }
    public int Fps { get; init; }
    public string? LatestFrame { get; init; }
}

/// <summary>GetState 返回的全量快照（schema State v1.2）。</summary>
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
    public VoiceCallState? VoiceCall { get; init; }
    public ScreenShareState? ScreenShare { get; init; }
}
