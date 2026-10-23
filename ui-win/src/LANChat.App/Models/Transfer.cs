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
    public string MatrixSummary { get; init; } = "";

    /// <summary>0~1 的进度，供进度条绑定。</summary>
    public double Progress => Size > 0 ? (double)BytesDone / Size : 0;

    /// <summary>是否为群组（频道 / swarm）传输，用于列表展示 groupId。</summary>
    public bool HasGroup => !string.IsNullOrEmpty(GroupId);

    /// <summary>是否有块可用性矩阵摘要。</summary>
    public bool HasMatrix => !string.IsNullOrEmpty(MatrixSummary);

    /// <summary>将 group.matrix 的 base64 haveBitmap 转为紧凑块进度摘要（如 "▓▓▓░░░ 4/8 块"）。</summary>
    public static string FormatMatrixSummary(string haveBitmapB64, long size)
    {
        if (string.IsNullOrEmpty(haveBitmapB64))
        {
            return "";
        }

        byte[] bytes;
        try
        {
            bytes = Convert.FromBase64String(haveBitmapB64);
        }
        catch
        {
            return "";
        }

        if (bytes.Length == 0)
        {
            return "";
        }

        const long chunkSize = 1024L * 1024L;
        var totalChunks = size > 0 ? (int)((size + chunkSize - 1) / chunkSize) : bytes.Length * 8;
        if (totalChunks <= 0)
        {
            return "";
        }

        var have = 0;
        for (var i = 0; i < totalChunks; i++)
        {
            var byteIdx = i / 8;
            var bitIdx = i % 8;
            var bit = byteIdx < bytes.Length && ((bytes[byteIdx] >> bitIdx) & 1) == 1;
            if (bit)
            {
                have++;
            }
        }

        var slots = Math.Min(totalChunks, 8);
        var chars = new char[slots];
        for (var s = 0; s < slots; s++)
        {
            var idx = s * totalChunks / slots;
            var byteIdx = idx / 8;
            var bitIdx = idx % 8;
            var bit = byteIdx < bytes.Length && ((bytes[byteIdx] >> bitIdx) & 1) == 1;
            chars[s] = bit ? '▓' : '░';
        }

        return $"{new string(chars)} {have}/{totalChunks} 块";
    }
}
