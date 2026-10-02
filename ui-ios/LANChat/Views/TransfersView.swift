import SwiftUI

/// 文件传输列表。
struct TransfersView: View {
    @EnvironmentObject private var store: AppStore

    var body: some View {
        NavigationStack {
            List {
                if store.transfers.isEmpty {
                    Text("暂无传输任务")
                        .foregroundStyle(.secondary)
                }
                ForEach(store.transfers) { t in
                    TransferRow(t: t)
                }
            }
            .navigationTitle("传输")
            .refreshable { await store.refresh() }
        }
    }
}

private struct TransferRow: View {
    @EnvironmentObject private var store: AppStore
    let t: Transfer

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: t.isIncoming ? "arrow.down.circle" : "arrow.up.circle")
                Text(t.name).lineLimit(1)
                Spacer()
                if t.kind == "channel" {
                    Text("频道")
                        .font(.caption2)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(.thinMaterial, in: Capsule())
                        .foregroundStyle(.secondary)
                }
                if t.viaRelay {
                    Text("⚠ 中继")
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
            }

            Text(stateText).font(.caption).foregroundStyle(.secondary)

            if t.isPendingIncoming {
                HStack {
                    Button("拒绝", role: .destructive) { store.rejectTransfer(t) }
                    Button("接收") { store.acceptTransfer(t) }
                }
                .buttonStyle(.bordered)
            } else {
                ProgressView(value: Double(t.fraction))
                    .tint(t.viaRelay ? .orange : .accentColor)
                HStack {
                    Text("\(Formatters.size(t.bytesDone)) / \(Formatters.size(t.size))")
                        .font(.caption2).foregroundStyle(.secondary)
                    Spacer()
                    if t.isActive {
                        Text(Formatters.speed(t.speedBps))
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                }
                if t.state == "failed" && !t.errorReason.isEmpty {
                    Text(t.errorReason).font(.caption2).foregroundStyle(.red)
                }
                if t.isActive || t.state == "paused" {
                    Button("取消", role: .destructive) { store.cancelTransfer(t) }
                        .buttonStyle(.bordered)
                        .font(.caption)
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var stateText: String {
        let action: String
        switch t.state {
        case "active": action = t.isIncoming ? "接收中" : "发送中"
        case "pending": action = "等待应答"
        case "paused": action = "已暂停"
        case "done": action = "已完成"
        case "failed": action = "失败"
        case "canceled": action = "已取消"
        default: action = t.state
        }
        guard t.kind == "channel", let channel = store.channels.first(where: { $0.id == t.groupId }) else {
            return action
        }
        return "\(action) · \(channel.name)"
    }
}
