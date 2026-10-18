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
        case LocalCodes.coreStartFailed: "代理内核无法启动，请重新安装应用"
        case LocalCodes.coreLost, CoreCodes.notStarted: "内核已停止，连接已断开，请重新登录"
        case LocalCodes.cancelled: "已取消。本次会话已结束，请重新登录"
        case LocalCodes.sessionEnded: "登录已失效，请重新登录"
        case LocalCodes.stateChanged, CoreCodes.staleGeneration: "状态已变化，请重试"
        case "invalid_request", "payload_too_large", "unknown_command", "protocol_unsupported", "not_implemented": "请求格式不正确"
        case LocalCodes.vpnPermissionDenied: "需要允许 AsterLink 添加 VPN 配置才能使用加速"
        case LocalCodes.vpnStartFailed: "无法启动 VPN，请重试"
        case LocalCodes.vpnStopping: "上一次连接正在断开并结算，请稍后再试"
        case LocalCodes.connectTimeout: "连接超时，请稍后重试或更换线路"
        case LocalCodes.credentialReadFailed: "无法读取保存的密码，请手动输入"
        case LocalCodes.credentialSaveFailed: "无法安全保存密码，本次未记住密码"
        case LocalCodes.credentialDeleteFailed: "无法删除本机保存的密码，请在系统设置中清除应用数据"
        case LocalCodes.noPlan: "暂无可用套餐，请前往官网购买或续费"
        case LocalCodes.noLines: "管理员尚未配置可用线路"
        case LocalCodes.selectLine: "请先在“线路”页选择一条线路"
        case LocalCodes.noMatch: "没有符合条件的线路"
        case LocalCodes.syncing: "正在同步线路…"
        case LocalCodes.tunnelEnded: "连接已断开"
        case LocalCodes.vpnRevoked: "VPN 已被系统设置或其它 VPN 应用关闭，本次流量已结算"
        case LocalCodes.networkUnreachable: "无法连接服务器，请检查网络"
        case LocalCodes.tunnelUnreachable: "VPN 已断开，请重新连接"
        case LocalCodes.routeProbeFailed: "隧道已建立，但系统流量没有经过隧道，已自动断开。请重试"
        case "handoff_required": "请在 AsterLink App 内连接"
        case "tun_unavailable": "此设备暂时无法建立系统隧道"
        default: "操作失败，请稍后重试"
        }
    }

    public static func status(_ status: ConnectionStatus) -> String {
        switch status {
        case .disconnected: "未连接"
        case .preparing: "正在准备"
        case .connecting: "正在连接"
        case .connected: "已连接"
        case .stopping: "正在断开并结算"
        case .unavailable: "无法连接"
        case .reconfirming: "正在重新验证"
        case .waitingNetwork: "等待网络"
        }
    }

    public static func statusDetail(_ status: ConnectionStatus) -> String? {
        switch status {
        case .connected: "流量正经所选线路转发"
        case .preparing: "正在校验套餐与线路授权"
        case .connecting: "正在建立隧道并验证转发"
        case .stopping: "正在关闭数据通路并结算本次流量"
        case .reconfirming: "设备唤醒或网络变化，正在在线确认授权；确认前不转发任何流量"
        case .waitingNetwork: "暂时连不上服务器：隧道保持但不转发流量，网络恢复后自动继续"
        case .disconnected, .unavailable: nil
        }
    }

    public static func lineLabel(dedicated: Bool) -> String { dedicated ? "专线" : "普通" }
}
