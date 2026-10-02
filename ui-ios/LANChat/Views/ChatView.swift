import SwiftUI

/// 1:1 聊天界面。
struct ChatView: View {
    @EnvironmentObject private var store: AppStore
    let peer: Peer

    @State private var draft = ""
    @State private var showPicker = false

    private var thread: [ChatMessage] {
        store.messagesByPeer[peer.id] ?? []
    }

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 8) {
                        ForEach(thread) { msg in
                            BubbleRow(message: msg)
                                .padding(.horizontal)
                        }
                    }
                    .padding(.vertical, 8)
                }
                .onChange(of: thread.count) { _ in
                    if let last = thread.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }

            Divider()
            HStack(alignment: .bottom, spacing: 8) {
                Button {
                    showPicker = true
                } label: {
                    Image(systemName: "paperclip")
                        .font(.title3)
                        .frame(width: 34, height: 34)
                }
                TextField("消息", text: $draft, axis: .vertical)
                    .lineLimit(1...5)
                    .padding(8)
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 16))
                Button {
                    store.sendText(to: peer.id, text: draft)
                    draft = ""
                } label: {
                    Text("发送").fontWeight(.semibold)
                }
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
        }
        .navigationTitle(peer.nickname)
        .navigationBarTitleDisplayMode(.inline)
        .sheet(isPresented: $showPicker) {
            FilePicker { url in
                store.offerFile(to: peer.id, path: url.path)
                showPicker = false
            }
            .ignoresSafeArea()
        }
    }
}

/// 单条消息气泡。
struct BubbleRow: View {
    let message: ChatMessage

    var body: some View {
        HStack {
            if !message.inbound { Spacer(minLength: 40) }
            Text(message.text)
                .padding(.horizontal, 12).padding(.vertical, 8)
                .background(
                    message.inbound
                        ? Color(.tertiarySystemBackground)
                        : Color.accentColor.opacity(0.22),
                    in: RoundedRectangle(cornerRadius: 16)
                )
                .foregroundStyle(.primary)
                .textSelection(.enabled)
            if message.inbound { Spacer(minLength: 40) }
        }
    }
}
