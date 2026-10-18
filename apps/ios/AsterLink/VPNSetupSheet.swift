import SwiftUI

/// Before the system asks to add the VPN configuration (and, after the first connection, to allow
/// notifications): one sentence for each thing it will ask, so no dialog comes as a surprise.
struct VPNSetupSheet: View {
    let proceed: () -> Void
    let later: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("首次连接需要添加 VPN 配置").font(.title2.weight(.semibold)).foregroundStyle(Theme.textPrimary)
                .accessibilityIdentifier("vpn-setup-title")
            VStack(alignment: .leading, spacing: 14) {
                step("1.circle.fill", "点击“继续”后，iOS 会弹出“AsterLink 想要添加 VPN 配置”，请选择“允许”。")
                step("2.circle.fill", "系统可能要求你输入设备密码或使用面容 ID 确认。这只由系统处理，AsterLink 不会获取或保存它。")
                step("3.circle.fill", "连接成功后，我们会请求通知权限，仅用于套餐到期、流量用尽等原因导致断开时提醒你。不允许也不影响使用。")
            }
            Spacer(minLength: 0)
            Button("继续", action: proceed).buttonStyle(PrimaryButtonStyle()).accessibilityIdentifier("vpn-setup-continue")
            Button("稍后再说", action: later).buttonStyle(SecondaryButtonStyle()).accessibilityIdentifier("vpn-setup-later")
        }
        .padding(Theme.pageMargin)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("vpn-setup-sheet")
    }

    private func step(_ symbol: String, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol).font(.title3).foregroundStyle(Theme.primary).accessibilityHidden(true)
            Text(text).font(.body).foregroundStyle(Theme.textPrimary).fixedSize(horizontal: false, vertical: true)
        }
    }
}
