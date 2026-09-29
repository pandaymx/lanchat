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
        switch t.state {
        case "active": return t.isIncoming ? "接收中" : "发送中"
        case "pending": return "等待应答"
        case "paused": return "已暂停"
        case "done": return "已完成"
        case "failed": return "失败"
        case "canceled": return "已取消"
        default: return t.state
        }
    }
}
