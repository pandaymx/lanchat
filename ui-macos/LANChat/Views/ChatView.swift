import SwiftUI

/// 聊天主区：消息气泡 + 输入框 + 发送文件。
struct ChatView: View {
    @ObservedObject var store: ChatStore
    let peer: Peer

    @State private var draft = ""

    private var thread: [ChatMessage] { store.messages[peer.id] ?? [] }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(peer.nickname).font(.headline)
                    Text(peer.status.isEmpty ? "在线" : peer.status)
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                Spacer()
            }
            .padding()

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 8) {
                        ForEach(thread) { msg in
                            BubbleRow(message: msg, isSelf: !msg.inbound)
                                .id(msg.id)
                        }
                    }
                    .padding()
                }
                .onChange(of: thread.count) { _ in
                    if let last = thread.last { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }

            Divider()
            HStack(alignment: .bottom, spacing: 8) {
                Button {
                    pickAndOffer()
                } label: {
                    Image(systemName: "paperclip")
                }
                .help("发送文件")

                TextEditor(text: $draft)
                    .frame(minHeight: 36, maxHeight: 120)
                    .padding(6)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .cornerRadius(8)

                Button("发送") { send() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(draft.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding()
        }
        .frame(minWidth: 480, minHeight: 420)
    }

    private func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        draft = ""
        Task { await store.sendText(to: peer.id, text: text) }
    }

    private func pickAndOffer() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await store.offerFile(to: peer.id, path: url.path) }
    }
}

struct BubbleRow: View {
    let message: ChatMessage
    let isSelf: Bool

    var body: some View {
        VStack(alignment: isSelf ? .trailing : .leading, spacing: 2) {
            if !isSelf, let name = message.senderName {
                Text(name)
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            HStack {
                if isSelf { Spacer(minLength: 60) }
                Text(message.text)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(isSelf ? Color.accentColor.opacity(0.18) : Color(nsColor: .windowBackgroundColor))
                    .cornerRadius(12)
                    .overlay(
                        RoundedRectangle(cornerRadius: 12)
                            .stroke(Color.gray.opacity(0.2), lineWidth: 0.5))
                    .textSelection(.enabled)
                if !isSelf { Spacer(minLength: 60) }
            }
        }
    }
}
