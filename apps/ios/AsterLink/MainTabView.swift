import AsterLinkKit
import SwiftUI

/// The three tabs after login: connect, lines, account.
struct MainTabView: View {
    @Environment(AppModel.self) private var model
    let session: SessionController

    var body: some View {
        TabView(selection: Bindable(model).tab) {
            NavigationStack { ConnectView(session: session) }
                .tabItem { Label("连接", systemImage: "power") }
                .tag(AppTab.connect)
            NavigationStack { LinesView(session: session) }
                .tabItem { Label("线路", systemImage: "list.bullet") }
                .tag(AppTab.lines)
            NavigationStack { AccountView(session: session) }
                .tabItem { Label("我的", systemImage: "person.crop.circle") }
                .tag(AppTab.account)
        }
    }
}
