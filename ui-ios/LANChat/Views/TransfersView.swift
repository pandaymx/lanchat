import SwiftUI

/// 文件传输列表（手机 QQ 传输助手 / P2P 卡片风格）。
struct TransfersView: View {
    @EnvironmentObject private var store: AppStore
    @State private var searchText = ""

    private var filteredTransfers: [Transfer] {
        let q = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return store.transfers }
        return store.transfers.filter { $0.name.lowercased().contains(q) }
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                // 手机 QQ 顶部个人头像 + 搜索栏
                QQTopHeaderView(
                    searchText: $searchText,
                    trailingActionIcon: "arrow.clockwise",
                    onTrailingAction: {
                        Task { await store.refresh() }
                    }
                )

                ScrollView {
                    LazyVStack(spacing: 12) {
                        // iOS >1 GiB 诚实能力边界提示横幅
                        iosBoundaryWarningBanner

                        if filteredTransfers.isEmpty {
                            emptyTransfersCard
                        } else {
                            ForEach(filteredTransfers) { t in
                                TransferCard(t: t)
                            }
                        }
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
                }
                .background(Color(.systemGroupedBackground))
                .refreshable {
                    await store.refresh()
                }
            }
            .navigationBarHidden(true)
        }
    }

    private var iosBoundaryWarningBanner: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
                .font(.title3)

            VStack(alignment: .leading, spacing: 3) {
                Text("iOS 传输能力边界：前台控制端 + 中小文件传输（>1 GiB 提示）")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.primary)
                Text("仅在应用处于前台时保持 P2P TCP 直连 / AES-GCM 中继传输，切入后台将挂起，不支持后台做种。")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(Color.orange.opacity(0.10))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(Color.orange.opacity(0.28), lineWidth: 1)
                )
        )
    }

    private var emptyTransfersCard: some View {
        VStack(spacing: 10) {
            Image(systemName: "arrow.up.arrow.down.square.fill")
                .font(.system(size: 36))
                .foregroundStyle(QQTheme.qqBlue.opacity(0.65))
            Text("暂无文件传输任务")
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)
            Text("在聊天会话或频道中点击「📁 发送文件」即可发起 P2P 高速直连传输")
                .font(.caption2)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 36)
        .padding(.horizontal, 16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}

/// 单个 P2P 文件传输卡片：文件图标、进度条、速率、直连/⚠中继徽章、Swarm 块矩阵摘要（▓▓▓░）、>1 GiB 告警与操作按钮。
private struct TransferCard: View {
    @EnvironmentObject private var store: AppStore
    let t: Transfer

    private var isOverOneGiB: Bool {
        t.size > FileSize.largeFileThreshold
    }

    private var swarmMatrixText: String {
        if let matrix = store.matrixSummary[t.id], !matrix.isEmpty {
            return matrix
        }
        let filled = Int(round(Double(t.fraction) * 4.0))
        let clamped = max(0, min(4, filled))
        let bar = String(repeating: "▓", count: clamped) + String(repeating: "░", count: 4 - clamped)
        return "\(bar) \(Int(t.fraction * 100))%"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            // 1. 顶部：文件图标 + 文件名 + 直连/中继徽章 + 频道徽章
            HStack(spacing: 12) {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(QQTheme.qqBlue.opacity(0.14))
                    .frame(width: 44, height: 44)
                    .overlay(
                        Image(systemName: t.isIncoming ? "arrow.down.doc.fill" : "arrow.up.doc.fill")
                            .font(.system(size: 20, weight: .semibold))
                            .foregroundStyle(QQTheme.qqBlue)
                    )

                VStack(alignment: .leading, spacing: 3) {
                    Text(t.name)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)

                    HStack(spacing: 6) {
                        Text(stateText)
                            .font(.caption)
                            .foregroundStyle(.secondary)

                        if t.kind == "channel" || !t.groupId.isEmpty {
                            Text("Swarm 群发")
                                .font(.system(size: 10, weight: .semibold))
                                .foregroundStyle(QQTheme.qqBlue)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 2)
                                .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                        }
                    }
                }

                Spacer()

                // 绿色「直连」/ 橙色「⚠ 中继」徽章
                if t.viaRelay {
                    Text("⚠ 中继")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundStyle(.orange)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(Color.orange.opacity(0.14), in: Capsule())
                } else {
                    Text("直连")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundStyle(QQTheme.onlineGreen)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(QQTheme.onlineGreen.opacity(0.14), in: Capsule())
                }
            }

            // 2. 单文件 >1 GiB 边界提示
            if isOverOneGiB {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                    Text("文件超过 1 GiB，请保持 LANChat 处于前台避免传输中断")
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(.orange)
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Color.orange.opacity(0.10), in: RoundedRectangle(cornerRadius: 8))
            }

            // 3. 进度条 + 大小 + Swarm 块矩阵摘要（▓▓▓░）+ 实时速率
            VStack(alignment: .leading, spacing: 6) {
                ProgressView(value: Double(t.fraction))
                    .tint(t.viaRelay ? .orange : QQTheme.qqBlue)

                HStack(spacing: 8) {
                    Text("\(Formatters.size(t.bytesDone)) / \(Formatters.size(t.size))")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)

                    Text(swarmMatrixText)
                        .font(.caption2.monospaced())
                        .foregroundStyle(QQTheme.qqBlue)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(QQTheme.qqBlue.opacity(0.10), in: Capsule())

                    Spacer()

                    if t.isActive {
                        Text(Formatters.speed(t.speedBps))
                            .font(.caption2.weight(.semibold).monospacedDigit())
                            .foregroundStyle(QQTheme.qqBlue)
                    }
                }
            }

            if t.state == "failed" && !t.errorReason.isEmpty {
                Text("错误：\(t.errorReason)")
                    .font(.caption2)
                    .foregroundStyle(.red)
            }

            // 4. 操作按钮栏（接收/拒绝/暂停/继续/取消）
            if t.isPendingIncoming {
                HStack(spacing: 10) {
                    Button {
                        store.acceptTransfer(t)
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "checkmark.circle.fill")
                            Text("接收")
                        }
                        .font(.caption.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue, in: Capsule())
                    }

                    Button(role: .destructive) {
                        store.rejectTransfer(t)
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "xmark.circle")
                            Text("拒绝")
                        }
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.red)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                        .background(Color.red.opacity(0.12), in: Capsule())
                    }

                    Spacer()
                }
            } else if t.isActive || t.state == "paused" {
                HStack(spacing: 10) {
                    if t.isActive {
                        Button {
                            store.pauseTransfer(t)
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: "pause.circle.fill")
                                Text("暂停")
                            }
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(QQTheme.qqBlue)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 6)
                            .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                        }
                    } else if t.state == "paused" {
                        Button {
                            store.resumeTransfer(t)
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: "play.circle.fill")
                                Text("继续")
                            }
                            .font(.caption.weight(.bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 6)
                            .background(QQTheme.qqBlue, in: Capsule())
                        }
                    }

                    Button(role: .destructive) {
                        store.cancelTransfer(t)
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "stop.circle")
                            Text("取消")
                        }
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.red)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 6)
                        .background(Color.red.opacity(0.12), in: Capsule())
                    }

                    Spacer()
                }
            }
        }
        .padding(14)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
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
