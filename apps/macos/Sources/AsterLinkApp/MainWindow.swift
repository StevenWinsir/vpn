import AsterLinkShared
import SwiftUI

/// The content of the one main window: start-up, sign-in, or the signed-in shell.
struct MainWindowView: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Group {
            if let session = model.session {
                RootView(session: session, helper: model.helper, diagnostics: model.diagnostics, shell: model.shell)
            } else {
                ConfigurationErrorView()
            }
        }
        .frame(minWidth: 820, minHeight: 560)
        .onAppear { model.shell.openWindow = openWindow }
    }
}

struct RootView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @Bindable var shell: AppShell

    var body: some View {
        switch session.phase {
        case .starting:
            VStack(spacing: 16) {
                BrandMark(size: 56)
                ProgressView().controlSize(.small)
                Text("正在启动…").font(.system(size: 13)).foregroundStyle(Theme.textSecondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.background)
        case .signedOut, .signingIn:
            LoginView(session: session, helper: helper, diagnostics: diagnostics)
        case .signedIn:
            ShellView(session: session, helper: helper, diagnostics: diagnostics, shell: shell)
        }
    }
}

/// Signed in: sidebar (brand, three pages, status and account) plus the page.
struct ShellView: View {
    @Bindable var session: SessionController
    let helper: HelperClient
    let diagnostics: AppDiagnostics
    @Bindable var shell: AppShell

    var body: some View {
        NavigationSplitView {
            Sidebar(session: session, shell: shell)
                .navigationSplitViewColumnWidth(min: 200, ideal: 216, max: 260)
                .toolbar(removing: .sidebarToggle)
        } detail: {
            Group {
                switch shell.page {
                case .connect: ConnectPage(session: session, shell: shell)
                case .lines: LinesPage(session: session, shell: shell)
                case .account: AccountPage(session: session, helper: helper, diagnostics: diagnostics)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.background)
        }
        .toolbar(removing: .sidebarToggle)
        .toolbarBackground(.hidden, for: .windowToolbar)
        .toolbar {
            if session.busy {
                ToolbarItem(placement: .primaryAction) {
                    ProgressView().controlSize(.small).accessibilityLabel("正在处理")
                }
            }
        }
    }
}

struct Sidebar: View {
    let session: SessionController
    @Bindable var shell: AppShell

    var body: some View {
        List(selection: Binding(get: { shell.page }, set: { if let page = $0 { shell.page = page } })) {
            Label("连接", systemImage: "power").tag(AppShell.Page.connect)
            Label("线路", systemImage: "globe.asia.australia").tag(AppShell.Page.lines)
            Label("账户", systemImage: "person.crop.circle").tag(AppShell.Page.account)
        }
        .listStyle(.sidebar)
        .frame(minWidth: 200, idealWidth: 216)
        .safeAreaInset(edge: .top, spacing: 0) {
            Wordmark(size: 26)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 18).padding(.top, 6).padding(.bottom, 14)
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            VStack(alignment: .leading, spacing: 8) {
                StatusPill(status: ConnectionStatus(session.snapshot.state))
                if let email = session.snapshot.user?.email {
                    Text(email).font(.system(size: 12)).foregroundStyle(Theme.textSecondary)
                        .lineLimit(1).truncationMode(.middle).help(email)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16).padding(.vertical, 14)
        }
        .tint(Theme.brand)
    }
}

struct ConfigurationErrorView: View {
    var body: some View {
        VStack(spacing: 12) {
            BrandMark(size: 56)
            Text("应用安装不完整").font(.system(size: 18, weight: .semibold)).foregroundStyle(Theme.textPrimary)
            Text("请从官网重新下载安装 AsterLink。").font(.system(size: 13)).foregroundStyle(Theme.textSecondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.background)
    }
}
