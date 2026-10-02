package dev.lanchat.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 校验退出频道（leaveChannel）对会话归类表的影响：
 * 仅移除对应群会话，单播会话与其他频道保留。
 */
class ConversationsLeaveTest {

    private fun groupMessage(group: String, msgId: String) = ChatMessage(
        peerId = "p1",
        msgId = msgId,
        text = "g-$msgId",
        inbound = true,
        timestamp = msgId.hashCode().toLong(),
        group = group,
        senderId = "p1",
    )

    private fun peerMessage(peerId: String, msgId: String) = ChatMessage(
        peerId = peerId,
        msgId = msgId,
        text = "p-$msgId",
        inbound = true,
        timestamp = msgId.hashCode().toLong(),
    )

    @Test
    fun removeGroup_dropsOnlyThatChannel() {
        var map = emptyMap<String, List<ChatMessage>>()
        map = Conversations.append(map, groupMessage("c1", "m1"))
        map = Conversations.append(map, groupMessage("c2", "m2"))
        map = Conversations.append(map, peerMessage("p1", "m3"))

        val updated = Conversations.removeGroup(map, "c1")

        assertTrue(Conversations.messagesOf(updated, Conversations.groupKey("c1")).isEmpty())
        assertEquals(
            listOf("m2"),
            Conversations.messagesOf(updated, Conversations.groupKey("c2")).map { it.msgId },
        )
        // 单播会话不受影响。
        assertEquals(
            listOf("m3"),
            Conversations.messagesOf(updated, Conversations.peerKey("p1")).map { it.msgId },
        )
    }

    @Test
    fun removeGroup_isIdempotentAndSafeForUnknown() {
        var map = emptyMap<String, List<ChatMessage>>()
        map = Conversations.append(map, groupMessage("c1", "m1"))

        val once = Conversations.removeGroup(map, "c1")
        val twice = Conversations.removeGroup(once, "c1")
        val unknown = Conversations.removeGroup(twice, "nope")

        assertEquals(once, unknown)
        assertTrue(Conversations.messagesOf(unknown, Conversations.groupKey("c1")).isEmpty())
    }
}
