import AsterLinkShared
import SwiftUI

/// The menu bar panel (handoff 5.4): status, a connect switch with the same conditions as the main
/// window's button, the current line, the remaining traffic, open and quit. Opening it calls nothing:
/// it only reads the snapshot the session already holds.
struct MenuBarPanel: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let session = model.session {
                SignedPanel(session: session, open: open)
            } else {
                header(title: "应用安装不完整", tone: .bad)
                    .padding(14)
            }
            Divider().overlay(Theme.stroke)
            HStack(spacing: 8) {
                Button { open(nil) } label: {
                    Text(model.session?.phase == .signedIn ? "打开 AsterLink" : "打开 AsterLink 登录").frame(maxWidth: .infinity)
                }
                .buttonStyle(SecondaryButtonStyle(height: 28, fullWidth: true))
                Button { NSApp.terminate(nil) } label: { Text("退出") }
                    .buttonStyle(SecondaryButtonStyle(height: 28))
                    .keyboardShortcut("q")
            }
            .padding(12)
        }
        .frame(width: 300)
        .background(Theme.background)
        .onAppear { model.shell.openWindow = openWindow }
    }

    private func open(_ page: AppShell.Page?) {
        dismiss()
        model.shell.showMainWindow(page)
    }
}

private func header(title: String, tone: Tone) -> some View {
    HStack(spacing: 10) {
        BrandMark(size: 30)
        VStack(alignment: .leading, spacing: 2) {
            Text("AsterLink").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.textPrimary)
            HStack(spacing: 5) {
                Circle().fill(Theme.color(for: tone)).frame(width: 7, height: 7)
                Text(title).font(.system(size: 12)).foregroundStyle(tone == .idle ? Theme.textSecondary : Theme.color(for: tone))
            }
        }
        Spacer(minLength: 0)
    }
    .accessibilityElement(children: .combine)
}

private struct SignedPanel: View {
    let session: SessionController
    let open: (AppShell.Page?) -> Void

    private var snap: CoreSnapshot { session.snapshot }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            switch session.phase {
            case .signedIn:
                let status = ConnectionStatus(snap.state)
                header(title: status.title, tone: status.tone)
                connectSwitch
                VStack(spacing: 2) {
                    PanelRow(symbol: "globe.asia.australia", title: "当前线路", value: snap.selectedNode?.name ?? "未选择") { open(.lines) }
                    PanelRow(symbol: "chart.pie", title: "剩余流量", value: remaining) { open(.connect) }
                }
                if let blocker = Blocker.of(session) {
                    Text(blocker.text).font(.system(size: 12)).foregroundStyle(blocker.severe ? Theme.dangerText : Theme.warnText)
                        .fixedSize(horizontal: false, vertical: true)
                } else if snap.state != .connected, let failure = snap.lastError {
                    Text(failure.message).font(.system(size: 12)).foregroundStyle(Theme.dangerText)
                        .fixedSize(horizontal: false, vertical: true)
                }
            case .starting:
                header(title: "正在启动…", tone: .busy)
            case .signedOut, .signingIn:
                header(title: "未登录", tone: .idle)
                Text("登录后即可在这里快速连接").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
            }
        }
        .padding(14)
    }

    private var connectSwitch: some View {
        let action = PrimaryAction(session)
        return Button {
            switch action {
            case .connect(true): Task { await session.connect() }
            case .disconnect(true): Task { await session.disconnect() }
            default: break
            }
        } label: {
            HStack(spacing: 8) {
                if case .progress = action {
                    ProgressView().controlSize(.small)
                    Text(ConnectionStatus(snap.state).title)
                } else {
                    Image(systemName: "power")
                    Text(action.title)
                }
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(PanelSwitchStyle(connected: snap.state == .connected))
        .disabled(!action.enabled)
        .accessibilityValue(ConnectionStatus(snap.state).title)
    }

    private var remaining: String {
        guard let entitlement = snap.entitlement, entitlement.accountAuthorized else { return "暂无有效套餐" }
        guard entitlement.totalBytes > 0 else { return entitlement.planName.isEmpty ? "—" : entitlement.planName }
        return "\(Formatting.bytes(entitlement.remainingBytes)) / \(Formatting.bytes(entitlement.totalBytes))"
    }
}

/// Connect is the brand fill; Disconnect is a quiet bordered button, so the two never look alike.
private struct PanelSwitchStyle: ButtonStyle {
    let connected: Bool

    func makeBody(configuration: Configuration) -> some View {
        if connected {
            SecondaryButtonStyle(height: 34, fullWidth: true).makeBody(configuration: configuration)
        } else {
            PrimaryButtonStyle(height: 34, fullWidth: true).makeBody(configuration: configuration)
        }
    }
}

private struct PanelRow: View {
    let symbol: String
    let title: String
    let value: String
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: symbol).frame(width: 16).foregroundStyle(Theme.brand).accessibilityHidden(true)
                Text(title).font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                Spacer(minLength: 8)
                Text(value).font(.system(size: 12, weight: .medium).monospacedDigit()).foregroundStyle(Theme.textPrimary).lineLimit(1)
                Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.textTertiary)
                    .accessibilityHidden(true)
            }
            .padding(.horizontal, 8).padding(.vertical, 7)
            .contentShape(Rectangle())
            .background(hovering ? Theme.surfaceMuted : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityLabel("\(title)：\(value)")
    }
}

/// D6: the first time the window is closed, a small note under the menu bar icon says where the app
/// went. It never takes focus and disappears after six seconds; it is shown once per Mac user.
@MainActor
enum CloseHint {
    static let key = "closeHintShown"
    private static var panel: NSPanel?

    static func showOnce() {
        guard !UserDefaults.standard.bool(forKey: key) else { return }
        UserDefaults.standard.set(true, forKey: key)
        show()
    }

    static func show() {
        panel?.orderOut(nil)
        let anchor = statusItemFrame()
        let screen = NSScreen.screens.first { anchor.map($0.frame.intersects) ?? false } ?? NSScreen.main
        let host = NSHostingView(rootView: CloseHintBubble(arrowOffset: nil))
        let size = host.fittingSize
        let visible = screen?.visibleFrame ?? .zero
        let wanted = (anchor?.midX ?? visible.maxX - 120) - size.width / 2
        let x = min(max(wanted, visible.minX + 8), visible.maxX - size.width - 8)
        let y = (anchor?.minY ?? visible.maxY) - size.height - 4
        host.rootView = CloseHintBubble(arrowOffset: anchor.map { $0.midX - x })

        let created = NSPanel(contentRect: NSRect(x: x, y: y, width: size.width, height: size.height),
                              styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
        created.isOpaque = false
        created.backgroundColor = .clear
        created.hasShadow = true
        created.level = .statusBar
        created.collectionBehavior = [.canJoinAllSpaces, .transient, .ignoresCycle]
        created.isReleasedWhenClosed = false
        created.contentView = host
        created.orderFrontRegardless()
        panel = created
        DispatchQueue.main.asyncAfter(deadline: .now() + 6) {
            created.orderOut(nil)
            if panel === created { panel = nil }
        }
    }

    /// The menu bar icon's on-screen frame (its status bar window), when the system shows it.
    private static func statusItemFrame() -> NSRect? {
        NSApp.windows.first { String(describing: type(of: $0)).contains("StatusBarWindow") && $0.isVisible }?.frame
    }
}

struct CloseHintBubble: View {
    let arrowOffset: CGFloat?

    var body: some View {
        VStack(spacing: 0) {
            Triangle().fill(Theme.surface).frame(width: 14, height: 7)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.leading, max(12, (arrowOffset ?? 150) - 7))
            HStack(alignment: .top, spacing: 10) {
                BrandMark(size: 26)
                VStack(alignment: .leading, spacing: 3) {
                    Text("AsterLink 仍在菜单栏运行").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.textPrimary)
                    Text("点击此图标即可重新打开。").font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                }
            }
            .padding(12)
            .frame(width: 300, alignment: .leading)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Theme.stroke, lineWidth: 1))
        }
        .frame(width: 300)
        .padding(6)
    }
}

private struct Triangle: Shape {
    func path(in rect: CGRect) -> Path {
        var path = Path()
        path.move(to: CGPoint(x: rect.midX, y: rect.minY))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.maxY))
        path.addLine(to: CGPoint(x: rect.minX, y: rect.maxY))
        path.closeSubpath()
        return path
    }
}
