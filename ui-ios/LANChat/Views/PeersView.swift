import SwiftUI

/// 联系人列表。
struct PeersView: View {
    @EnvironmentObject private var store: AppStore

    var body: some View {
        NavigationStack {
            List {
                if store.peers.isEmpty {
                    Text("当前没有其他在线用户")
                        .foregroundStyle(.secondary)
                }
                ForEach(store.peers) { peer in
                    NavigationLink {
                        ChatView(peer: peer)
                    } label: {
                        HStack(spacing: 10) {
                            Circle()
                                .fill(peer.status == "away" ? Color.orange : Color.green)
                                .frame(width: 10, height: 10)
                            VStack(alignment: .leading) {
                                Text(peer.nickname)
                                Text(peer.os).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .navigationTitle("在线 \(store.peers.count)")
            .refreshable { await store.refresh() }
        }
    }
}
