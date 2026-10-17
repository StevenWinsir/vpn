import AsterLinkKit
import SwiftUI

/// Round-1 page: proves the app links the real Core and shows its identity. The formal
/// three-tab interface replaces it in round 3.
struct FoundationView: View {
    @State private var hello: Result<CoreHello, Error>?

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "power.circle.fill")
                .font(.system(size: 72))
                .foregroundStyle(Theme.primary)
                .accessibilityHidden(true)
            Text("AsterLink")
                .font(.largeTitle.weight(.semibold))
                .foregroundStyle(Theme.textPrimary)
            identity
            #if DEBUG
            NavigationLink("隧道调试（第 2 轮）") { TunnelDebugView() }
                .font(.footnote)
                .accessibilityIdentifier("open-tunnel-debug")
            #endif
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.background)
        .task { hello = CoreBridge.hello() }
    }

    @ViewBuilder private var identity: some View {
        switch hello {
        case .success(let value):
            VStack(spacing: 4) {
                Text("Core \(value.coreVersion)")
                Text("Mihomo \(String(value.mihomoRevision.prefix(12)))")
            }
            .font(.footnote.monospacedDigit())
            .foregroundStyle(Theme.textSecondary)
            .accessibilityIdentifier("core-identity")
        case .failure:
            Text("内核未响应")
                .font(.footnote)
                .foregroundStyle(Theme.error)
                .accessibilityIdentifier("core-identity-error")
        case nil:
            ProgressView()
        }
    }
}
