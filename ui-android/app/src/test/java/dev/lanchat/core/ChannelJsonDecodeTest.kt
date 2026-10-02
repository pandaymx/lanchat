package dev.lanchat.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 校验 gomobile JSON 快照 → 本地模型的字段映射，
 * 重点覆盖新增的 topic / private 字段及其缺省可解析性。
 */
class ChannelJsonDecodeTest {

    @Test
    fun decodeChannels_mapsTopicAndPrivate() {
        val payload = """
            [
              {
                "id": "c1",
                "name": "项目组",
                "ownerId": "owner-1",
                "private": true,
                "topic": "每周同步",
                "members": ["owner-1", "p2"]
              }
            ]
        """.trimIndent().toByteArray()

        val channels = decodeChannels(payload)

        assertEquals(1, channels.size)
        val channel = channels.single()
        assertEquals("c1", channel.id)
        assertEquals("项目组", channel.name)
        assertEquals("owner-1", channel.ownerId)
        assertTrue(channel.private)
        assertEquals("每周同步", channel.topic)
        assertEquals(listOf("owner-1", "p2"), channel.members)
    }

    @Test
    fun decodeChannels_defaultsTopicAndPrivateWhenAbsent() {
        // Go 侧 private/topic 使用 omitempty，旧快照可能不含这两个字段。
        val payload = """
            [
              {"id": "c2", "name": "公开", "ownerId": "o", "members": []}
            ]
        """.trimIndent().toByteArray()

        val channel = decodeChannels(payload).single()

        assertFalse(channel.private)
        assertEquals("", channel.topic)
        assertTrue(channel.members.isEmpty())
    }

    @Test
    fun decodeChannels_emptyAndNullPayload_safe() {
        assertEquals(0, decodeChannels("[]".toByteArray()).size)
        assertEquals(0, decodeChannels(null).size)
    }

    @Test
    fun decodeState_mapsChannelsInsideSnapshot() {
        val payload = """
            {
              "conn": "connected",
              "selfId": "me",
              "nickname": "本机",
              "peers": [],
              "transfers": [],
              "channels": [
                {"id": "c9", "name": "C9", "ownerId": "me", "private": true,
                 "topic": "M9", "members": ["me"]}
              ]
            }
        """.trimIndent().toByteArray()

        val state = decodeState(payload)

        assertEquals("connected", state.conn)
        assertEquals("me", state.selfId)
        val channel = state.channels.single()
        assertTrue(channel.private)
        assertEquals("M9", channel.topic)
    }
}
