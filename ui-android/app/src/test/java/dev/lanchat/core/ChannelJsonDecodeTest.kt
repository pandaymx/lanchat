package dev.lanchat.core

import dev.lanchat.model.Formatters
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
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

    @Test
    fun decodeState_mapsVoiceCallAndScreenShareAndDualStackBadge() {
        val payload = """
            {
              "conn": "connected",
              "server": "100.105.48.87:9090",
              "selfId": "me",
              "nickname": "本机",
              "peers": [],
              "transfers": [],
              "channels": [],
              "voiceCall": {
                "state": "ringing",
                "peerId": "p1",
                "groupId": "",
                "localAddr": "127.0.0.1:50001",
                "remoteAddr": "100.105.48.87:50002",
                "framesIn": 12,
                "framesOut": 8
              },
              "screenShare": {
                "active": true,
                "peerId": "p1",
                "groupId": "",
                "fps": 15,
                "latestFrame": "data:image/jpeg;base64,AA=="
              }
            }
        """.trimIndent().toByteArray()

        val state = decodeState(payload)

        val vc = state.voiceCall
        assertNotNull(vc)
        assertEquals("ringing", vc!!.state)
        assertEquals("p1", vc.peerId)
        assertEquals("100.105.48.87:50002", vc.remoteAddr)
        assertEquals(12L, vc.framesIn)

        val ss = state.screenShare
        assertNotNull(ss)
        assertTrue(ss!!.active)
        assertEquals("p1", ss.peerId)
        assertEquals(15, ss.fps)
        assertEquals("data:image/jpeg;base64,AA==", ss.latestFrame)

        assertTrue(Formatters.isTailscaleAddress(state.server))
        assertEquals("🌐 Tailscale 直连", Formatters.linkBadge(state.server))
        assertEquals("⚡ 局域网直连", Formatters.linkBadge("192.168.1.10:9090"))
    }
}
