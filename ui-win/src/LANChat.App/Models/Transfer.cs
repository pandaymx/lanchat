namespace LANChat.Models;

/// <summary>文件传输任务快照（schema Transfer）。</summary>
public sealed record Transfer
{
    public string Id { get; init; } = "";
    public TransferDirection Direction { get; init; }
    public TransferState State { get; init; }
    public PathKind Kind { get; init; }
    public string? PeerId { get; init; }
    public string? GroupId { get; init; }
    public string Name { get; init; } = "";
    public long Size { get; init; }
    public long BytesDone { get; init; }
    public long SpeedBps { get; init; }
    public bool ViaRelay { get; init; }
    public string? ErrorReason { get; init; }

    /// <summary>0~1 的进度，供进度条绑定。</summary>
    public double Progress => Size > 0 ? (double)BytesDone / Size : 0;
}
