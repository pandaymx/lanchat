import SwiftUI

/// 传输中心：待应答邀请与进行中/已完成任务列表。
struct TransfersView: View {
    @ObservedObject var store: ChatStore

    var body: some View {
        Group {
            if store.transfers.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "arrow.up.arrow.down.circle")
                        .font(.largeTitle)
                        .foregroundColor(.secondary)
                    Text("暂无传输任务").foregroundColor(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(store.transfers) { t in
                        TransferRow(transfer: t) {
                            Task { await store.respondFile(transferId: t.id, accept: true) }
                        } onDecline: {
                            Task { await store.respondFile(transferId: t.id, accept: false) }
                        } onCancel: {
                            Task { await store.cancelFile(t.id) }
                        }
                    }
                }
            }
        }
        .frame(minWidth: 460, minHeight: 380)
    }
}

private struct TransferRow: View {
    let transfer: Transfer
    let onAccept: () -> Void
    let onDecline: () -> Void
    let onCancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: transfer.isIncoming ? "arrow.down" : "arrow.up")
                    .foregroundColor(.secondary)
                Text(transfer.name).font(.body).lineLimit(1)
                if transfer.viaRelay {
                    Text("⚠ 中继")
                        .font(.caption2)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(Color.orange.opacity(0.2))
                        .cornerRadius(4)
                }
                Spacer()
                Text(stateText).font(.caption).foregroundColor(.secondary)
            }

            if transfer.state == "pending" && transfer.isIncoming {
                HStack {
                    Text(Formatters.size(transfer.size))
                        .font(.caption).foregroundColor(.secondary)
                    Spacer()
                    Button("拒绝", role: .cancel, action: onDecline)
                    Button("接收", action: onAccept)
                        .keyboardShortcut(.defaultAction)
                }
            } else {
                ProgressView(value: Double(transfer.fraction))
                HStack {
                    Text("\(Formatters.size(transfer.bytesDone)) / \(Formatters.size(transfer.size))")
                        .font(.caption).foregroundColor(.secondary)
                    Spacer()
                    if transfer.state == "active" {
                        Text(Formatters.speed(transfer.speedBps))
                            .font(.caption).foregroundColor(.secondary)
                    }
                    if transfer.state == "failed", let reason = transfer.errorReason {
                        Text(reason).font(.caption).foregroundColor(.red).lineLimit(1)
                    }
                    if transfer.state == "active" || transfer.state == "paused" {
                        Button(role: .destructive, action: onCancel) {
                            Image(systemName: "xmark.circle")
                        }.buttonStyle(.borderless)
                    }
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var stateText: String {
        switch transfer.state {
        case "active": return transfer.isIncoming ? "接收中" : "发送中"
        case "pending": return "等待应答"
        case "paused": return "已暂停"
        case "done": return "已完成"
        case "failed": return "失败"
        case "canceled": return "已取消"
        default: return transfer.state
        }
    }
}
