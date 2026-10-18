import AsterLinkKit
import SafariServices
import SwiftUI

@main
struct AsterLinkApp: App {
    @State private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .preferredColorScheme(model.appearance.scheme)
                .tint(Theme.primary)
        }
    }
}

/// Signed-out pages and the three-tab interface, switched by the Core-driven session phase.
struct RootView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        Group {
            if let session = model.session {
                switch session.state.phase {
                case .starting: SplashView()
                case .signedOut, .signingIn: LoginView(session: session)
                case .signedIn: MainTabView(session: session)
                }
            } else {
                ConfigErrorView()
            }
        }
        .animation(.snappy(duration: 0.25), value: model.session?.state.phase)
        .task { await model.launchIfNeeded() }
        .onChange(of: scenePhase) { _, phase in
            model.sceneChanged(active: phase == .active)
        }
        .sheet(item: Bindable(model).web) { page in
            SafariView(url: page.url).ignoresSafeArea().accessibilityLabel(page.title)
        }
    }
}

/// Shown for the instant the in-process Core starts; looks like the launch screen.
struct SplashView: View {
    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            VStack(spacing: 14) {
                BrandMark(size: 72)
                ProgressView().accessibilityIdentifier("splash-progress")
            }
        }
    }
}

/// The build parameters did not validate (env-v1): nothing can run, so say so.
struct ConfigErrorView: View {
    var body: some View {
        ContentUnavailableView {
            Label("应用配置无效", systemImage: "exclamationmark.triangle")
        } description: {
            Text("构建时的接口地址未通过校验，请重新安装官方版本。")
        }
        .accessibilityIdentifier("config-error")
    }
}

struct BrandMark: View {
    var size: CGFloat = 56

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.26, style: .continuous).fill(Theme.primary)
            Image(systemName: "power").font(.system(size: size * 0.5, weight: .semibold)).foregroundStyle(.white)
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

struct SafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController { SFSafariViewController(url: url) }
    func updateUIViewController(_ controller: SFSafariViewController, context: Context) {}
}
