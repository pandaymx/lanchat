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

    /** 向会话表追加一条消息，返回新的不可变 Map。 */
    fun append(
        messagesByConversation: Map<String, List<ChatMessage>>,
        message: ChatMessage,
    ): Map<String, List<ChatMessage>> {
        val key = keyOf(message)
        val list = (messagesByConversation[key] ?: emptyList()) + message
        return messagesByConversation + (key to list)
    }
}
