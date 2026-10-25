namespace LANChat.Models;

/// <summary>聊天消息（UI 模型；由 GetState 恢复历史消息并在本次运行内累积）。</summary>
public sealed record ChatMessage
{
    public string MsgId { get; init; } = "";
    public string FromId { get; init; } = "";
    public string FromName { get; init; } = "";
    public string Text { get; init; } = "";

    /// <summary>群聊消息所属频道 id（msg.received 的 group；单播为空）。</summary>
    public string? GroupId { get; init; }

    /// <summary>text / sticker / system。</summary>
    public string Type { get; init; } = "text";

    /// <summary>自己发送的消息（靠右气泡）。</summary>
    public bool IsSelf { get; init; }

    public DateTimeOffset Timestamp { get; init; } = DateTimeOffset.Now;

    /// <summary>M11a: 消息是否已撤回。</summary>
    public bool Revoked { get; init; }

    /// <summary>M11a: 已读该消息的对端 ID 列表。</summary>
    public IReadOnlyList<string> ReadBy { get; init; } = [];

    /// <summary>贴纸类型时，Text 为贴纸文件路径。</summary>
    public bool IsSticker => Type == "sticker";

    /// <summary>系统提示消息。</summary>
    public bool IsSystem => Type == "system";

    /// <summary>对端发来的消息（左侧气泡）。</summary>
    public bool IsInbound => !IsSelf && !IsSystem;

    /// <summary>自己发出的消息（右侧 QQ 蓝气泡）。</summary>
    public bool IsOutbound => IsSelf && !IsSystem;

    /// <summary>圆形头像首字。</summary>
    public string SenderInitial =>
        IsSelf ? "我" : (string.IsNullOrWhiteSpace(FromName) ? "U" : FromName.Trim()[..1].ToUpperInvariant());

    /// <summary>格式化时间戳（HH:mm）。</summary>
    public string FormattedTime => Timestamp.ToLocalTime().ToString("HH:mm");

    /// <summary>消息气泡顶部发送者与时间栏。</summary>
    public string HeaderText => $"{FromName}  {FormattedTime}";

    /// <summary>是否已被对端读取。</summary>
    public bool IsRead => IsSelf && !Revoked && ReadBy.Count > 0;

    /// <summary>已读回执文案。</summary>
    public string ReadReceiptText => Revoked ? "已撤回" : (IsRead ? "✓ 已读" : "✓ 已送达");

    /// <summary>是否允许撤回（自己发送的非系统未撤回消息）。</summary>
    public bool CanRevoke => IsSelf && !Revoked && !IsSystem && !string.IsNullOrEmpty(MsgId);

    /// <summary>是否允许引用回复。</summary>
    public bool CanReply => !Revoked && !IsSystem && !string.IsNullOrEmpty(MsgId);

    /// <summary>M17: 是否携带 data:image/...;base64,... 内联缩略图。</summary>
    public bool HasInlineImage =>
        !Revoked &&
        Text.TrimStart().StartsWith("data:image/", StringComparison.OrdinalIgnoreCase) &&
        Text.Contains(";base64,", StringComparison.Ordinal);

    /// <summary>气泡展示文本（若为内联 base64 图片则显示紧凑摘要，避免渲染长串 base64）。</summary>
    public string DisplayText => HasInlineImage ? "🖼️ [图片内联预览]" : Text;
}
