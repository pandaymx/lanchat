package dev.lanchat.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ConversationsTest {

    private fun peerMessage(
        peerId: String,
        msgId: String,
        inbound: Boolean,
    ) = ChatMessage(
        peerId = peerId,
        msgId = msgId,
        text = "hi-$msgId",
        inbound = inbound,
        timestamp = msgId.hashCode().toLong(),
    )

    private fun groupMessage(
        group: String,
        senderId: String,
        msgId: String,
    ) = ChatMessage(
        peerId = senderId,
        msgId = msgId,
        text = "group-$msgId",
        inbound = true,
        timestamp = msgId.hashCode().toLong(),
        group = group,
        senderId = senderId,
    )

    @Test
    fun keys_areDistinguishable() {
        assertEquals("p1", Conversations.peerKey("p1"))
        assertEquals("group:c1", Conversations.groupKey("c1"))
        assertTrue(Conversations.isGroup(Conversations.groupKey("c1")))
        assertFalse(Conversations.isGroup(Conversations.peerKey("p1")))
        assertEquals("c1", Conversations.groupIdOf(Conversations.groupKey("c1")))
        assertNull(Conversations.groupIdOf(Conversations.peerKey("p1")))
    }

    @Test
    fun peerMessages_clusterByCounterparty() {
        var map = emptyMap<String, List<ChatMessage>>()
        // 出站：发给 p1
        map = Conversations.append(map, peerMessage("p1", "m1", inbound = false))
        // 入站：来自 p1
        map = Conversations.append(map, peerMessage("p1", "m2", inbound = true))
        // 另一个对端
        map = Conversations.append(map, peerMessage("p2", "m3", inbound = false))

        assertEquals(
            listOf("m1", "m2"),
            Conversations.messagesOf(map, Conversations.peerKey("p1")).map { it.msgId },
        )
        assertEquals(
            listOf("m3"),
            Conversations.messagesOf(map, Conversations.peerKey("p2")).map { it.msgId },
        )
    }

    @Test
    fun groupMessages_clusterByChannel_notBySender() {
        var map = emptyMap<String, List<ChatMessage>>()
        map = Conversations.append(map, groupMessage("c1", "p1", "m1"))
        map = Conversations.append(map, groupMessage("c1", "p2", "m2"))
        map = Conversations.append(map, groupMessage("c2", "p1", "m3"))

        assertEquals(
            listOf("m1", "m2"),
            Conversations.messagesOf(map, Conversations.groupKey("c1")).map { it.msgId },
        )
        assertEquals(
            listOf("m3"),
            Conversations.messagesOf(map, Conversations.groupKey("c2")).map { it.msgId },
        )
        // 同一发送者在不同频道的消息不会混在一起
        assertTrue(Conversations.messagesOf(map, Conversations.peerKey("p1")).isEmpty())
    }

    @Test
    fun peerAndGroupConversations_neverCollide() {
        var map = emptyMap<String, List<ChatMessage>>()
        map = Conversations.append(map, peerMessage("c1", "m-peer", inbound = true))
        map = Conversations.append(map, groupMessage("c1", "p1", "m-group"))

        assertEquals(1, Conversations.messagesOf(map, Conversations.peerKey("c1")).size)
        assertEquals(1, Conversations.messagesOf(map, Conversations.groupKey("c1")).size)
    }
}
