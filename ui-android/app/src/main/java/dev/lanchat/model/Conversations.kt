package dev.lanchat.model

/**
 * 会话归类纯逻辑：单播会话以对端 peer ID 为 key，
 * 群（频道）会话以带前缀的频道 ID 为 key，二者不会冲突。
 */
object Conversations {

    private const val GROUP_PREFIX = "group:"

    /** 单播会话 key。 */
    fun peerKey(peerId: String): String = peerId

    /** 群（频道）会话 key。 */
    fun groupKey(groupId: String): String = GROUP_PREFIX + groupId

    /** 判断会话 key 是否为群会话。 */
    fun isGroup(key: String): Boolean = key.startsWith(GROUP_PREFIX)

    /** 从群会话 key 取出频道 ID；非群会话返回 null。 */
    fun groupIdOf(key: String): String? =
        if (isGroup(key)) key.removePrefix(GROUP_PREFIX) else null

    /**
     * 计算一条消息所属的会话 key：
     * 群消息按频道归类，出站单播按接收方、入站单播按发送方归类。
     */
    fun keyOf(message: ChatMessage): String =
        if (message.group.isNotEmpty()) {
            groupKey(message.group)
        } else {
            peerKey(message.peerId)
        }

    /** 取某会话下的消息，保持时间顺序。 */
    fun messagesOf(
        messagesByConversation: Map<String, List<ChatMessage>>,
        conversationKey: String,
    ): List<ChatMessage> = messagesByConversation[conversationKey] ?: emptyList()

    /** 移除某群（频道）会话；退出频道时调用，单播会话不受影响。 */
    fun removeGroup(
        messagesByConversation: Map<String, List<ChatMessage>>,
        groupId: String,
    ): Map<String, List<ChatMessage>> = messagesByConversation - groupKey(groupId)

    /** 向会话表追加一条消息（按 msgId 去重或更新状态），返回新的不可变 Map。 */
    fun append(
        messagesByConversation: Map<String, List<ChatMessage>>,
        message: ChatMessage,
    ): Map<String, List<ChatMessage>> {
        if (isSystemDiagnosticMessage(message.msgId, message.text)) {
            return messagesByConversation
        }
        val key = keyOf(message)
        val existing = messagesByConversation[key] ?: emptyList()
        if (message.msgId.isNotEmpty() && existing.any { it.msgId == message.msgId }) {
            val updated = existing.map { if (it.msgId == message.msgId) message else it }
            return messagesByConversation + (key to updated)
        }
        val list = existing + message
        return messagesByConversation + (key to list)
    }

    /** 将指定 msgId 的消息标记为已撤回。 */
    fun revokeMessage(
        messagesByConversation: Map<String, List<ChatMessage>>,
        msgId: String,
    ): Map<String, List<ChatMessage>> {
        if (msgId.isEmpty()) return messagesByConversation
        return messagesByConversation.mapValues { (_, list) ->
            list.map { m ->
                if (m.msgId == msgId) m.copy(revoked = true, text = "消息已撤回") else m
            }
        }
    }

    /** M20: 更新指定 msgId 的消息文本（用于表情回应、二次编辑与置顶标记）。 */
    fun updateMessageText(
        messagesByConversation: Map<String, List<ChatMessage>>,
        msgId: String,
        newText: String,
    ): Map<String, List<ChatMessage>> {
        if (msgId.isEmpty()) return messagesByConversation
        return messagesByConversation.mapValues { (_, list) ->
            list.map { m ->
                if (m.msgId == msgId && !m.revoked) m.copy(text = newText) else m
            }
        }
    }

    /** 记录对端已读指定 msgId 的消息。 */
    fun markRead(
        messagesByConversation: Map<String, List<ChatMessage>>,
        msgId: String,
        reader: String,
    ): Map<String, List<ChatMessage>> {
        if (msgId.isEmpty() || reader.isEmpty()) return messagesByConversation
        return messagesByConversation.mapValues { (_, list) ->
            list.map { m ->
                if (m.msgId == msgId && !m.readBy.contains(reader)) {
                    m.copy(readBy = m.readBy + reader)
                } else {
                    m
                }
            }
        }
    }

    /** 在指定会话 key 中按关键字检索未撤回消息（M12）。 */
    fun search(
        messagesByConversation: Map<String, List<ChatMessage>>,
        conversationKey: String,
        query: String,
    ): List<ChatMessage> {
        val q = query.trim().lowercase()
        if (q.isEmpty()) return emptyList()
        return (messagesByConversation[conversationKey] ?: emptyList()).filter {
            !it.revoked && it.text.lowercase().contains(q)
        }
    }

    /** 将 `/reply <msgID> <body>` 展开为带引用前缀的展示文本（M12）。 */
    fun formatReplyText(
        messagesByConversation: Map<String, List<ChatMessage>>,
        raw: String,
    ): String {
        val trimmed = raw.trim()
        if (trimmed == "/voice" || trimmed.startsWith("/voice ")) {
            val sec = trimmed.removePrefix("/voice").trim().ifEmpty { "3" }
            return "🎤 [语音消息 $sec\"]"
        }
        if (!trimmed.startsWith("/reply ")) return raw
        val rest = trimmed.removePrefix("/reply ").trim()
        val parts = rest.split(" ", limit = 2)
        if (parts.size < 2 || parts[1].isBlank()) return raw
        val targetId = parts[0].trim()
        val body = parts[1].trim()
        for (list in messagesByConversation.values) {
            val orig = list.firstOrNull { it.msgId == targetId && !it.revoked }
            if (orig != null) {
                val (cleanOrig, _) = splitReactions(orig.text)
                return "「$cleanOrig」\n$body"
            }
        }
        return "[re:$targetId] $body"
    }

    private fun isReactionEmojiToken(token: String): Boolean {
        if (token.isEmpty() || token.length > 24) return false
        for (ch in token) {
            if (ch in 'a'..'z' || ch in 'A'..'Z' || ch in '0'..'9' ||
                ch in '\u4E00'..'\u9FFF' || ch == ':' || ch == '"' || ch == ' '
            ) {
                return false
            }
        }
        return true
    }

    fun isSystemDiagnosticText(raw: String): Boolean {
        val t = raw.trim()
        return t.startsWith("[安全码指纹]") ||
            t.startsWith("🛡️ [安全码指纹") ||
            t.startsWith("📊 [诊断状态]") ||
            t.startsWith("🚀 [版本更新]") ||
            t.startsWith("⚡ [带宽限速")
    }

    fun isSystemDiagnosticMessage(msgId: String, text: String): Boolean {
        val prefixes = listOf("fp-", "status-", "update-", "search-", "e2ee-", "bwlimit-")
        if (prefixes.any { msgId.startsWith(it) }) return true
        return isSystemDiagnosticText(text)
    }

    private fun stripTrailingReactions(raw: String): Pair<String, List<String>> {
        var body = raw
        val reversed = mutableListOf<String>()
        while (body.endsWith("]")) {
            val idx = body.lastIndexOf(" [")
            if (idx < 0) break
            val token = body.substring(idx + 2, body.length - 1)
            if (!isReactionEmojiToken(token)) break
            reversed.add(token)
            body = body.substring(0, idx)
        }
        return body to reversed.asReversed()
    }

    /**
     * 拆分消息正文与尾部表情回应徽章（如 `"你好 [👍] [❤️]"` -> `"你好"` 与 `listOf("👍", "❤️")`），
     * 同时清理引用块 `「...」\n` 内的 `[👍]` 或 `[安全码指纹]`，供 UI 将点赞渲染在聊天气泡下方。
     */
    fun splitReactions(raw: String): Pair<String, List<String>> {
        val (rawBody, reactions) = stripTrailingReactions(raw)
        var body = rawBody.removePrefix("[表情] ")
        if (body.startsWith("「")) {
            val closeIdx = body.indexOf("」\n")
            if (closeIdx > 1) {
                val quoted = body.substring(1, closeIdx)
                val rest = body.substring(closeIdx + 2)
                val (cleanQuotedRaw, _) = stripTrailingReactions(quoted)
                val cleanQuoted = cleanQuotedRaw.removePrefix("[表情] ")
                body = if (isSystemDiagnosticText(cleanQuoted)) {
                    rest
                } else {
                    "「$cleanQuoted」\n$rest"
                }
            }
        }
        return body to reactions
    }
}
