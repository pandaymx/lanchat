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

    /// <summary>撤回前保存的原始可编辑正文（用于点击「重新编辑」恢复到输入框）。</summary>
    public string OriginalText { get; init; } = "";

    /// <summary>M11a: 已读该消息的对端 ID 列表。</summary>
    public IReadOnlyList<string> ReadBy { get; init; } = [];

    /// <summary>v1.1: 结构化被引用消息快照摘要。</summary>
    public string? ReplyQuote { get; init; }

    /// <summary>v1.1: 结构化表情回应映射（emoji -> 点赞用户 ID 列表）。</summary>
    public IReadOnlyDictionary<string, IReadOnlyList<string>>? StructuredReactions { get; init; }

    /// <summary>v1.1: 消息二次编辑时间戳（Unix 毫秒）。</summary>
    public long EditedAt { get; init; }

    /// <summary>v1.1: 消息是否置顶。</summary>
    public bool Pinned { get; init; }

    /// <summary>v1.1: 关联媒体 MIME 类型。</summary>
    public string? MediaMime { get; init; }

    /// <summary>v1.1: 关联本地媒体路径。</summary>
    public string? MediaPath { get; init; }

    /// <summary>贴纸类型时，Text 为贴纸文件路径。</summary>
    public bool IsSticker => Type == "sticker";

    /// <summary>系统提示消息。</summary>
    public bool IsSystem => Type == "system";

    /// <summary>QQ 风格居中撤回提示条（已撤回消息不显示左右头像与聊天气泡）。</summary>
    public bool IsRevokedNotice => Revoked && !IsSystem;

    /// <summary>居中撤回提示文案：自己撤回显示「你撤回了一条消息」，对方撤回显示「"昵称" 撤回了一条消息」。</summary>
    public string RevokeNoticeText =>
        IsSelf
            ? "你撤回了一条消息"
            : $"\"{(string.IsNullOrWhiteSpace(FromName) ? "对方" : FromName)}\" 撤回了一条消息";

    /// <summary>是否显示「重新编辑」蓝色按钮（仅自己撤回且存在原始文本时显示）。</summary>
    public bool CanReEdit => Revoked && IsSelf && !IsSticker && !string.IsNullOrWhiteSpace(OriginalText);

    /// <summary>对端发来的消息（左侧气泡，不含已撤回消息）。</summary>
    public bool IsInbound => !IsSelf && !IsSystem && !Revoked;

    /// <summary>自己发出的消息（右侧 QQ 蓝气泡，不含已撤回消息）。</summary>
    public bool IsOutbound => IsSelf && !IsSystem && !Revoked;

    /// <summary>圆形头像首字。</summary>
    public string SenderInitial =>
        IsSelf ? "我" : (string.IsNullOrWhiteSpace(FromName) ? "U" : FromName.Trim()[..1].ToUpperInvariant());

    /// <summary>格式化时间戳（HH:mm）。</summary>
    public string FormattedTime => Timestamp.ToLocalTime().ToString("HH:mm");

    /// <summary>消息气泡顶部发送者与时间栏（自己发送的消息仅展示时间与已读状态，避免重复显示“我”）。</summary>
    public string HeaderText
    {
        get
        {
            var pinPrefix = Pinned || Text.StartsWith("📌 ", StringComparison.Ordinal) ? "📌 [置顶] · " : "";
            var editSuffix = EditedAt > 0 || Text.EndsWith(" (已编辑)", StringComparison.Ordinal) ? " · (已编辑)" : "";
            return IsSelf
                ? $"{pinPrefix}{(IsRead ? $"{FormattedTime} · ✓ 已读" : FormattedTime)}{editSuffix}"
                : $"{pinPrefix}{FromName}  {FormattedTime}{editSuffix}";
        }
    }

    /// <summary>是否包含三反引号（```）代码块卡片。</summary>
    public bool HasCodeBlock =>
        !Revoked &&
        !HasInlineImage &&
        BodyText.Contains("```", StringComparison.Ordinal);

    /// <summary>提取代码块正文用于 NTQQ 暗色等宽代码卡片展示。</summary>
    public string CodeBlockText
    {
        get
        {
            if (!HasCodeBlock)
            {
                return "";
            }
            var first = BodyText.IndexOf("```", StringComparison.Ordinal);
            var last = BodyText.LastIndexOf("```", StringComparison.Ordinal);
            if (first >= 0 && last > first + 3)
            {
                var inner = BodyText[(first + 3)..last];
                var nl = inner.IndexOf('\n');
                return (nl >= 0 ? inner[(nl + 1)..] : inner).Trim();
            }
            return BodyText;
        }
    }

    /// <summary>是否为内联 P2P 文件传输卡片消息（Type == "file"）。</summary>
    public bool IsFileCard => !Revoked && Type == "file";

    /// <summary>是否已被对端读取。</summary>
    public bool IsRead => IsSelf && !Revoked && ReadBy.Count > 0;

    /// <summary>已读回执文案。</summary>
    public string ReadReceiptText => Revoked ? "已撤回" : (IsRead ? "✓ 已读" : "");

    /// <summary>是否允许撤回（自己发送的非系统未撤回消息）。</summary>
    public bool CanRevoke => IsSelf && !Revoked && !IsSystem && !string.IsNullOrEmpty(MsgId);

    /// <summary>是否允许引用回复。</summary>
    public bool CanReply => !Revoked && !IsSystem && !string.IsNullOrEmpty(MsgId);

    /// <summary>M17: 是否携带 data:image/...;base64,... 内联缩略图。</summary>
    public bool HasInlineImage =>
        !Revoked &&
        CleanText.TrimStart().StartsWith("data:image/", StringComparison.OrdinalIgnoreCase) &&
        CleanText.Contains(";base64,", StringComparison.Ordinal);

    /// <summary>剥离尾部点赞/表情回应徽章后的消息正文。</summary>
    public string CleanText => SplitReactions(Text).Body;

    /// <summary>是否包含 QQ 引用回复块（优先使用 v1.1 ReplyQuote，向后兼容 「...」\n）。</summary>
    public bool HasQuote => !Revoked && !HasInlineImage &&
        (!string.IsNullOrWhiteSpace(ReplyQuote) || SplitQuote(CleanText).HasQuote);

    /// <summary>被引用回复的原消息内容。</summary>
    public string QuotedText => !string.IsNullOrWhiteSpace(ReplyQuote)
        ? ReplyQuote!
        : SplitQuote(CleanText).Quoted;

    /// <summary>剥离引用回复块与尾部表情回应后的实际消息正文。</summary>
    public string BodyText => HasInlineImage ? "🖼️ [图片]" : SplitQuote(CleanText).Body;

    /// <summary>M20/v1.1: 解析出的尾部表情回应列表（优先读取结构化映射，展示在气泡下方）。</summary>
    public IReadOnlyList<string> Reactions => Revoked
        ? []
        : (StructuredReactions is { Count: > 0 }
            ? StructuredReactions.Keys.ToList()
            : SplitReactions(Text).Reactions);

    /// <summary>是否存在应在气泡下方展示的点赞/表情回应徽章。</summary>
    public bool HasReactions => !Revoked && Reactions.Count > 0;

    /// <summary>气泡下方点赞/表情回应胶囊展示文案（如 "👍 1"）。</summary>
    public string ReactionsText => StructuredReactions is { Count: > 0 }
        ? string.Join("  ", StructuredReactions.Select(kv => $"{kv.Key} {Math.Max(1, kv.Value.Count)}"))
        : string.Join("  ", Reactions.Select(r => $"{r} 1"));

    /// <summary>气泡展示文本（若包含引用块则仅返回正文部分，引用块由独立卡片渲染）。</summary>
    public string DisplayText => BodyText;

    private static (bool HasQuote, string Quoted, string Body) SplitQuote(string clean)
    {
        if (clean.StartsWith('「'))
        {
            var closeIdx = clean.IndexOf("」\n", StringComparison.Ordinal);
            if (closeIdx > 1)
            {
                return (true, clean[1..closeIdx], clean[(closeIdx + 2)..]);
            }
        }

        return (false, "", clean);
    }

    private static bool IsReactionEmojiToken(string token)
    {
        if (string.IsNullOrEmpty(token) || token.Length > 24)
        {
            return false;
        }

        foreach (var ch in token)
        {
            if (char.IsAsciiLetterOrDigit(ch) || (ch >= '\u4E00' && ch <= '\u9FFF') || ch is ':' or '"' or ' ')
            {
                return false;
            }
        }

        return true;
    }

    public static bool IsSystemDiagnosticText(string? raw)
    {
        var t = (raw ?? "").Trim();
        return t.StartsWith("[安全码指纹]", StringComparison.Ordinal) ||
               t.StartsWith("🛡️ [安全码指纹", StringComparison.Ordinal) ||
               t.StartsWith("📊 [诊断状态]", StringComparison.Ordinal) ||
               t.StartsWith("🚀 [版本更新]", StringComparison.Ordinal) ||
               t.StartsWith("⚡ [带宽限速", StringComparison.Ordinal);
    }

    private static (string Body, IReadOnlyList<string> Reactions) StripTrailingReactions(string raw)
    {
        var body = raw ?? "";
        var reversed = new List<string>();
        while (body.EndsWith(']'))
        {
            var idx = body.LastIndexOf(" [", StringComparison.Ordinal);
            if (idx < 0)
            {
                break;
            }

            var token = body[(idx + 2)..^1];
            if (!IsReactionEmojiToken(token))
            {
                break;
            }

            reversed.Add(token);
            body = body[..idx];
        }

        reversed.Reverse();
        return (body, reversed);
    }

    public static (string Body, IReadOnlyList<string> Reactions) SplitReactions(string raw)
    {
        var (body, reactions) = StripTrailingReactions(raw ?? "");
        if (body.StartsWith("[表情] ", StringComparison.Ordinal))
        {
            body = body[5..];
        }

        if (body.StartsWith('「'))
        {
            var closeIdx = body.IndexOf("」\n", StringComparison.Ordinal);
            if (closeIdx > 1)
            {
                var quoted = body[1..closeIdx];
                var rest = body[(closeIdx + 2)..];
                var (cleanQuoted, _) = StripTrailingReactions(quoted);
                if (cleanQuoted.StartsWith("[表情] ", StringComparison.Ordinal))
                {
                    cleanQuoted = cleanQuoted[5..];
                }

                body = IsSystemDiagnosticText(cleanQuoted)
                    ? rest
                    : $"「{cleanQuoted}」\n{rest}";
            }
        }

        return (body, reactions);
    }
}
