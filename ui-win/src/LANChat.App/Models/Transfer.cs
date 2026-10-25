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

    /// <summary>是否为 P2P 直连路径（非中继）。</summary>
    public bool IsDirect => !ViaRelay;

    /// <summary>传输方向徽章。</summary>
    public string DirectionBadgeText => Direction == TransferDirection.Inbound ? "⬇ 接收" : "⬆ 发送";

    /// <summary>传输类型徽章。</summary>
    public string KindBadgeText => Kind switch
    {
        PathKind.Swarm => "G1 广播",
        PathKind.Channel => "G2 频道",
        _ => "P2P",
    };

    /// <summary>已传字节与总大小文案。</summary>
    public string FormattedSizeProgress =>
        $"{FormatBytes(BytesDone)} / {FormatBytes(Size)} ({Progress * 100:0.#}%)";

    /// <summary>实时速率或状态文案。</summary>
    public string FormattedSpeed => SpeedBps > 0
        ? $"{FormatBytes(SpeedBps)}/s"
        : State switch
        {
            TransferState.Done => "✓ 传输完成",
            TransferState.Paused => "⏸ 已暂停",
            TransferState.Failed => string.IsNullOrEmpty(ErrorReason) ? "✕ 传输失败" : $"✕ {ErrorReason}",
            TransferState.Canceled => "✕ 已取消",
            TransferState.Pending => "⏳ 等待确认",
            _ => "传输中…",
        };

    public static string FormatBytes(long bytes)
    {
        string[] units = ["B", "KB", "MB", "GB", "TB"];
        double value = Math.Max(0, bytes);
        var unit = 0;
        while (value >= 1024 && unit < units.Length - 1)
        {
            value /= 1024;
            unit++;
        }
        return $"{value:0.##} {units[unit]}";
    }

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
