import Foundation

/// Chinese text for the notices raised on this side. A Core refusal carries its own Chinese
/// message and never comes here; unknown codes get the generic failure text. Same wording as the
/// Android client (values/strings.xml) so support can quote one set of sentences.
public enum ErrorText {
    public static func text(for notice: Notice) -> String { notice.text ?? local(notice.code) }

    public static func local(_ code: String) -> String {
        switch code {
        case LocalCodes.inputMissing: "请输入邮箱和密码"
        case LocalCodes.emailTooLong: "邮箱过长"
        case LocalCodes.passwordTooLong: "密码过长（最多 72 字节）"
        case LocalCodes.coreStartFailed: "应用启动失败，请重新安装应用"
        case LocalCodes.coreLost, CoreCodes.notStarted: "连接已中断，请重新登录"
        case LocalCodes.cancelled: "已取消。本次会话已结束，请重新登录"
        case LocalCodes.sessionEnded: "登录已失效，请重新登录"
        case LocalCodes.stateChanged, CoreCodes.staleGeneration: "状态已变化，请重试"
        case "invalid_request", "payload_too_large", "unknown_command", "protocol_unsupported", "not_implemented": "操作失败，请稍后重试"
        case LocalCodes.vpnPermissionDenied: "需要允许 AsterLink 添加 VPN 配置才能使用加速"
        case LocalCodes.vpnStartFailed: "无法启动 VPN，请重试"
        case LocalCodes.vpnStopping: "上一次连接正在断开，请稍后再试"
        case LocalCodes.connectTimeout: "连接超时，请稍后重试或更换线路"
        case LocalCodes.credentialReadFailed: "无法读取保存的密码，请手动输入"
        case LocalCodes.credentialSaveFailed: "无法安全保存密码，本次未记住密码"
        case LocalCodes.credentialDeleteFailed: "无法删除本机保存的密码，请在系统设置中清除应用数据"
        case LocalCodes.noPlan: "暂无可用套餐，请前往官网购买或续费"
        case LocalCodes.noLines: "暂无可用线路"
        case LocalCodes.selectLine: "请先在“线路”页选择一条线路"
        case LocalCodes.noMatch: "没有符合条件的线路"
        case LocalCodes.syncing: "正在同步线路…"
        case LocalCodes.tunnelEnded: "连接已断开"
        case LocalCodes.vpnRevoked: "VPN 已被系统设置或其它 VPN 应用关闭，请重新连接"
        case LocalCodes.vpnSuperseded: "VPN 已被其它 VPN 应用取代，需要时请重新连接"
        case LocalCodes.vpnUserSwitch: "系统切换了用户，VPN 已断开，请重新连接"
        case LocalCodes.vpnAppUpdate: "应用已更新，VPN 已断开，请重新连接"
        case LocalCodes.vpnSystemStopped: "系统关闭了 VPN，请重新连接"
        case LocalCodes.notificationsOff: "通知已关闭：VPN 在后台因额度或套餐断开时，你不会收到提醒"
        case LocalCodes.networkUnreachable: "无法连接服务器，请检查网络"
        case LocalCodes.tunnelUnreachable: "VPN 已断开，请重新连接"
        case LocalCodes.routeProbeFailed: "连接未能生效，已自动断开，请重试"
        case "handoff_required": "请在 AsterLink App 内连接"
        case "tun_unavailable": "当前设备暂时无法建立连接，请稍后重试"
        default: "操作失败，请稍后重试"
        }
    }

    public static func status(_ status: ConnectionStatus) -> String {
        switch status {
        case .disconnected: "未连接"
        case .preparing: "正在准备"
        case .connecting: "正在连接"
        case .connected: "已连接"
        case .stopping: "正在断开"
        case .unavailable: "无法连接"
        case .reconfirming: "正在重新验证"
        case .waitingNetwork: "等待网络"
        }
    }

    public static func statusDetail(_ status: ConnectionStatus) -> String? {
        switch status {
        case .connected: "已为你建立安全连接"
        case .preparing: "正在准备连接…"
        case .connecting: "正在连接…"
        case .stopping: "正在断开…"
        case .reconfirming: "设备唤醒或网络变化，正在重新验证账户"
        case .waitingNetwork: "暂时连不上服务器，网络恢复后自动继续"
        case .disconnected, .unavailable: nil
        }
    }

    public static func lineLabel(dedicated: Bool) -> String { dedicated ? "专线" : "普通线路" }
}
