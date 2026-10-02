import SwiftUI
import UniformTypeIdentifiers

/// 文件选择使用场景：决定超大文件时的能力边界提示文案。
enum FilePickScenario {
    /// 1:1 文件发送。
    case unicast
    /// 频道群文件（不鼓励发起超大 swarm）。
    case channel

    var largeFileMessage: String {
        switch self {
        case .unicast:
            return "「%@」超过 1 GiB。\niOS 仅在应用处于前台时传输，切到后台可能中断，确定继续吗？"
        case .channel:
            return "「%@」超过 1 GiB。\niOS 仅适合中小文件，不支持后台做种，在频道群（多成员）中发起超大文件 swarm 很可能中断，不建议继续。确定仍要发起吗？"
        }
    }
}

/// 文件选择器：包装 UIDocumentPickerViewController。
///
/// 选中后：
///   1. 取得 security-scoped 访问权；
///   2. 若文件 >1 GiB，先弹窗提示 iOS 前台传输限制（诚实边界），
///      用户确认后才继续；
///   3. 通过回调把文件路径交给调用方。
/// 注意：Go 侧按路径读取期间需要保持 startAccessingSecurityScopedResource，
/// 这里在整个 App 生命周期内不释放（与前台传输定位一致）。
struct FilePicker: UIViewControllerRepresentable {
    var scenario: FilePickScenario = .unicast
    var onPicked: (URL) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIViewController(context: Context) -> UIDocumentPickerViewController {
        let picker = UIDocumentPickerViewController(forOpeningContentTypes: [UTType.data])
        picker.allowsMultipleSelection = false
        picker.shouldShowFileExtensions = true
        picker.delegate = context.coordinator
        return picker
    }

    func updateUIViewController(_ controller: UIDocumentPickerViewController, context: Context) {}

    final class Coordinator: NSObject, UIDocumentPickerDelegate {
        private let parent: FilePicker
        init(_ parent: FilePicker) { self.parent = parent }

        func documentPicker(_ controller: UIDocumentPickerViewController,
                            didPickDocumentsAt urls: [URL]) {
            guard let url = urls.first else { return }
            let needsScope = url.startAccessingSecurityScopedResource()
            _ = needsScope // 传输期间保持访问，不 stop。

            let size = (try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
            if Int64(size) > FileSize.largeFileThreshold {
                // 先取出宿主再进闭包：捕获列表里写 [parent] 会被要求显式 self。
                let host = parent
                let name = url.lastPathComponent
                let message = String(format: host.scenario.largeFileMessage, name)
                DispatchQueue.main.async {
                    Self.confirmLargeFile(message: message) {
                        host.onPicked(url)
                    }
                }
            } else {
                parent.onPicked(url)
            }
        }

        private static func confirmLargeFile(message: String,
                                             confirm: @escaping () -> Void) {
            guard let scene = UIApplication.shared.connectedScenes
                .compactMap({ $0 as? UIWindowScene }).first,
                let root = scene.windows.first(where: { $0.isKeyWindow })?.rootViewController else {
                confirm()
                return
            }
            let alert = UIAlertController(
                title: "文件较大",
                message: message,
                preferredStyle: .alert
            )
            alert.addAction(UIAlertAction(title: "取消", style: .cancel))
            alert.addAction(UIAlertAction(title: "继续发送", style: .default) { _ in confirm() })
            root.present(alert, animated: true)
        }
    }
}

enum FileSize {
    /// 1 GiB（iOS 诚实边界阈值）。
    static let largeFileThreshold: Int64 = 1_073_741_824
}
