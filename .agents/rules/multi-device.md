# 多端一致性铁律

> 与核心卖点（多端同时在线不互踢）直接相关，违反会被打回。
> 基础模型见 AGENTS.md §7（ADR-008），本文件是它的可执行约束。

## 铁律清单

1. **新端必须复用 `pkg/core` + `pkg/client`**。TUI / Web / 桌面 / 移动四端共享同一份业务内核，任何新端不得另起炉灶（ADR-002 / ADR-012 的验收标准）。
2. **per-device 游标是硬要求**。`read_cursors(device_id, conv_id, last_seq)`，禁止任何 user 级游标（`conversations.last_seq` 这类会让第二台设备漏消息或重复拉全量）。
3. **新功能验收必须含「同一 User 双设备同时在线」用例**。单设备跑通不等于多端正确，验收清单里显式写双设备并发场景。
4. **身份分两层**：`User`（人）与 `Device`（设备）。一个 User 可有多个 Device 同时在线，消息投递到该 User 的所有在线 Device（不是互踢）。
5. **一期 UI 不暴露一对多**，但结构上必须支持。二期再做配对码绑定、设备列表管理、全设备同步。
6. **消息排序与同步一律以服务端 `server_seq` 为准**，禁止依赖客户端时钟。
7. **Web 端多 Tab 是「多设备」的特例**：同一浏览器多 Tab 共享一个 device_id（localStorage + cookie 双层），Session 需支持 fanout 多订阅者（M4.4）。

## 验收用例模板（新功能必须附）

```
场景：user=alice 双设备 alice-laptop / alice-phone 同时在线
- [ ] 消息发给 alice → 两台设备都收到
- [ ] laptop 读游标前进 → phone 未读不受影响
- [ ] phone 离线重连 → 只补自己漏掉的（ResumeFrom 按 per-device cursor）
```
