import SwiftUI

/// 传输中心与右侧可折叠传输抽屉（Column 4 Inspector）：
/// 展示实时 P2P 传输进度条、速率、直连(绿)/⚠ 中继(橙) 徽章、Swarm 分块矩阵 (▓▓▓░) 与暂停/继续/取消控制。
struct TransfersView: View {
    @ObservedObject var store: ChatStore
    var isInspector: Bool = false
    var onClose: (() -> Void)? = nil

    @State private var filter: TransferFilter = .all

    enum TransferFilter: String, CaseIterable, Identifiable {
        case all = "全部"
        case active = "传输中"
        case pending = "待接收"
        case done = "已完成"

        var id: String { rawValue }
    }

    private var filteredTransfers: [Transfer] {
        switch filter {
        case .all:
            return store.transfers
        case .active:
            return store.transfers.filter { $0.state == "active" || $0.state == "paused" }
        case .pending:
            return store.transfers.filter { $0.state == "pending" }
        case .done:
            return store.transfers.filter { $0.state == "done" }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            headerBar
            Divider()
            filterBar
            Divider()

            if filteredTransfers.isEmpty {
                emptyState
            } else {
                ScrollView {
                    LazyVStack(spacing: 10) {
                        ForEach(filteredTransfers) { t in
                            TransferRow(
                                transfer: t,
                                scopeName: scopeName(for: t),
                                matrixSummary: store.matrixSummary[t.id] ?? fallbackMatrix(for: t),
                                onAccept: {
                                    Task { await store.respondFile(transferId: t.id, accept: true) }
                                },
                                onDecline: {
                                    Task { await store.respondFile(transferId: t.id, accept: false) }
                                },
                                onPause: {
                                    Task { await store.pauseFile(t.id) }
                                },
                                onResume: {
                                    Task { await store.resumeFile(t.id) }
                                },
                                onCancel: {
                                    Task { await store.cancelFile(t.id) }
                                }
                            )
                        }
                    }
                    .padding(12)
                }
            }
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: isInspector ? 285 : 460, maxWidth: isInspector ? 310 : .infinity, minHeight: 380)
    }

    private var headerBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "arrow.up.arrow.down.circle.fill")
                .font(.system(size: 16))
                .foregroundColor(QQPalette.qqBlue)

            VStack(alignment: .leading, spacing: 1) {
                Text(isInspector ? "实时传输抽屉" : "P2P 文件传输中心")
                    .font(.system(size: 14, weight: .bold))
                Text("直连优先 · AES-GCM 加密中继兜底 · 4MiB 分块校验")
                    .font(.system(size: 10.5))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            if let onClose {
                Button {
                    onClose()
                } label: {
                    Image(systemName: "sidebar.right")
                        .font(.system(size: 13))
                        .foregroundColor(.secondary)
                        .padding(5)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(Color.primary.opacity(0.05))
                        )
                }
                .buttonStyle(.plain)
                .help("收起传输抽屉")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.6))
    }

    private var filterBar: some View {
        HStack(spacing: 6) {
            ForEach(TransferFilter.allCases) { item in
                let selected = (filter == item)
                Button {
                    filter = item
                } label: {
                    Text(item.rawValue)
                        .font(.system(size: 11.5, weight: selected ? .semibold : .medium))
                        .foregroundColor(selected ? .white : .secondary)
                        .padding(.horizontal, 9)
                        .padding(.vertical, 4)
                        .background(
                            Capsule()
                                .fill(selected ? QQPalette.qqBlue : Color.primary.opacity(0.05))
                        )
                }
                .buttonStyle(.plain)
            }
            Spacer()
            Text("共 \(store.transfers.count) 项")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Spacer()
            Image(systemName: "externaldrive.badge.wifi")
                .font(.system(size: 34))
                .foregroundColor(.secondary.opacity(0.45))
            Text("暂无传输任务")
                .font(.system(size: 13.5, weight: .semibold))
                .foregroundColor(.secondary)
            Text("在聊天窗口点击「📁 发送文件」即可发起高速 P2P 直连传输")
                .font(.system(size: 11.5))
                .foregroundColor(.secondary.opacity(0.75))
                .multilineTextAlignment(.center)
                .padding(.horizontal, 20)
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    /// 频道（kind=channel）传输显示频道名；G1 全体显示“全体”；单播显示对端昵称。
    private func scopeName(for t: Transfer) -> String? {
        switch t.kind {
        case "channel":
            return store.channels.first(where: { $0.id == t.groupId })?.name
                ?? t.groupId.map { String($0.prefix(8)) }
        case "swarm":
            return "全体广播"
        default:
            if let peerId = t.peerId, !peerId.isEmpty {
                return store.peerNickname(for: peerId)
            }
            return nil
        }
    }

    /// 当未收到 group.matrix 通知时，根据字节进度生成分块矩阵视图（如 `▓▓▓░░░`）。
    private func fallbackMatrix(for t: Transfer) -> String? {
        guard t.size > 0 else { return nil }
        let blockSize: Int64 = 4 * 1024 * 1024
        let totalBlocks = max(1, Int((t.size + blockSize - 1) / blockSize))
        let doneBlocks = min(totalBlocks, Int((t.bytesDone + blockSize - 1) / blockSize))
        let cells = min(8, totalBlocks)
        let filled = min(cells, Int(Float(cells) * t.fraction))
        let bar = (0..<cells).map { $0 < filled ? "▓" : "░" }.joined()
        return "\(bar) \(doneBlocks)/\(totalBlocks) 块"
    }
}

private struct TransferRow: View {
    let transfer: Transfer
    var scopeName: String?
    var matrixSummary: String?
    let onAccept: () -> Void
    let onDecline: () -> Void
    let onPause: () -> Void
    let onResume: () -> Void
    let onCancel: () -> Void

    private var scopeBadge: String? {
        switch transfer.kind {
        case "channel": return "# " + (scopeName ?? "频道")
        case "swarm": return "📢 " + (scopeName ?? "全体")
        default: return scopeName
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            // 第一行：方向图标 + 文件名 + 直连/中继徽章
            HStack(spacing: 6) {
                Circle()
                    .fill((transfer.isIncoming ? QQPalette.qqBlue : QQPalette.onlineGreen).opacity(0.14))
                    .frame(width: 26, height: 26)
                    .overlay(
                        Image(systemName: transfer.isIncoming ? "arrow.down" : "arrow.up")
                            .font(.system(size: 11.5, weight: .bold))
                            .foregroundColor(transfer.isIncoming ? QQPalette.qqBlue : QQPalette.onlineGreen)
                    )

                VStack(alignment: .leading, spacing: 2) {
                    Text(transfer.name)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.primary)
                        .lineLimit(1)

                    HStack(spacing: 5) {
                        if transfer.isFolderBundle {
                            Text("📁 文件夹")
                                .font(.system(size: 10, weight: .semibold))
                                .foregroundColor(QQPalette.qqBlue)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 1)
                                .background(
                                    Capsule()
                                        .fill(QQPalette.qqBlue.opacity(0.14))
                                )
                        }

                        if let scope = scopeBadge {
                            Text(scope)
                                .font(.system(size: 10, weight: .medium))
                                .foregroundColor(QQPalette.qqBlue)
                                .lineLimit(1)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(
                                    Capsule()
                                        .fill(QQPalette.qqBlue.opacity(0.12))
                                )
                        }

                        // 直连 (绿) / ⚠ 中继 (橙) 徽章
                        if transfer.viaRelay {
                            Text("⚠ 中继")
                                .font(.system(size: 10, weight: .semibold))
                                .foregroundColor(QQPalette.busyOrange)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 1)
                                .background(
                                    Capsule()
                                        .fill(QQPalette.busyOrange.opacity(0.18))
                                )
                        } else {
                            Text("直连")
                                .font(.system(size: 10, weight: .semibold))
                                .foregroundColor(QQPalette.onlineGreen)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 1)
                                .background(
                                    Capsule()
                                        .fill(QQPalette.onlineGreen.opacity(0.16))
                                )
                        }

                        Spacer()

                        Text(stateText)
                            .font(.system(size: 10.5, weight: .medium))
                            .foregroundColor(stateColor)
                    }
                }
            }

            if transfer.state == "pending" && transfer.isIncoming {
                HStack {
                    Text("文件大小：\(Formatters.size(transfer.size))")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                    Spacer()
                    Button("拒绝", role: .cancel, action: onDecline)
                        .controlSize(.small)
                    Button {
                        onAccept()
                    } label: {
                        Text("接收文件")
                            .font(.system(size: 11.5, weight: .semibold))
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(
                                RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    .fill(QQPalette.qqBlue)
                            )
                    }
                    .buttonStyle(.plain)
                    .keyboardShortcut(.defaultAction)
                }
            } else {
                ProgressView(value: Double(transfer.fraction))
                    .tint(transfer.state == "done" ? QQPalette.onlineGreen : QQPalette.qqBlue)

                HStack(spacing: 6) {
                    Text("\(Formatters.size(transfer.bytesDone)) / \(Formatters.size(transfer.size))")
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundColor(.secondary)

                    if let matrixSummary, !matrixSummary.isEmpty {
                        Text(matrixSummary)
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundColor(.secondary)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1.5)
                            .background(
                                RoundedRectangle(cornerRadius: 4, style: .continuous)
                                    .fill(Color.primary.opacity(0.05))
                            )
                    }

                    Spacer()

                    if transfer.state == "active" {
                        Text(Formatters.speed(transfer.speedBps))
                            .font(.system(size: 11, weight: .semibold, design: .monospaced))
                            .foregroundColor(QQPalette.qqBlue)
                        Button(action: onPause) {
                            Image(systemName: "pause.circle.fill")
                                .font(.system(size: 15))
                                .foregroundColor(.secondary)
                        }
                        .buttonStyle(.plain)
                        .help("暂停传输")
                    } else if transfer.state == "paused" {
                        Button(action: onResume) {
                            Image(systemName: "play.circle.fill")
                                .font(.system(size: 15))
                                .foregroundColor(QQPalette.qqBlue)
                        }
                        .buttonStyle(.plain)
                        .help("断点续传")
                    }

                    if transfer.state == "active" || transfer.state == "paused" {
                        Button(role: .destructive, action: onCancel) {
                            Image(systemName: "xmark.circle.fill")
                                .font(.system(size: 15))
                                .foregroundColor(.red.opacity(0.8))
                        }
                        .buttonStyle(.plain)
                        .help("取消传输")
                    }
                }

                if transfer.state == "failed", let reason = transfer.errorReason {
                    Text("错误：\(reason)")
                        .font(.system(size: 11))
                        .foregroundColor(.red)
                        .lineLimit(1)
                }
            }
        }
        .padding(11)
        .background(
            RoundedRectangle(cornerRadius: 11, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 11, style: .continuous)
                .stroke(Color.primary.opacity(0.07), lineWidth: 1)
        )
    }

    private var stateText: String {
        switch transfer.state {
        case "active": return transfer.isIncoming ? "接收中" : "发送中"
        case "pending": return "等待应答"
        case "paused": return "已暂停"
        case "done": return "✓ 已完成"
        case "failed": return "传输失败"
        case "canceled": return "已取消"
        default: return transfer.state
        }
    }

    private var stateColor: Color {
        switch transfer.state {
        case "active": return QQPalette.qqBlue
        case "pending": return QQPalette.busyOrange
        case "done": return QQPalette.onlineGreen
        case "failed": return .red
        default: return .secondary
        }
    }
}
