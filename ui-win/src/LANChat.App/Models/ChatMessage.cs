namespace LANChat.Models;

/// <summary>聊天消息（UI 模型；由 GetState 无历史消息，仅本次运行内累积）。</summary>
public sealed record ChatMessage
{
    public string MsgId { get; init; } = "";
    public string FromId { get; init; } = "";
    public string FromName { get; init; } = "";
    public string Text { get; init; } = "";

    /// <summary>text / sticker。</summary>
    public string Type { get; init; } = "text";

    /// <summary>自己发送的消息（靠右气泡）。</summary>
    public bool IsSelf { get; init; }

    public DateTimeOffset Timestamp { get; init; } = DateTimeOffset.Now;

    /// <summary>贴纸类型时，Text 为贴纸文件路径。</summary>
    public bool IsSticker => Type == "sticker";
}
