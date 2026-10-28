import Foundation

/// The runtime half of the copy guard (handoff_app_standardization.md 7.4): every screen the UI tests
/// capture has all of its visible text checked against the same word lists as the static scan.
/// scripts/test-app-copy.py compares the two lists below with its own and fails on any difference,
/// so edit the lists there first.
enum CopyGuard {
    static let forbiddenZh = [
        "内核",
        "隧道",
        "代理",
        "特权服务",
        "握手",
        "协议版本",
        "构建渠道",
        "渠道",
        "通道",
        "代次",
        "批次",
        "账本",
        "会话交接",
        "节点计量",
        "客户端上报",
        "计量",
        "幂等",
        "请求标识",
        "数据通路",
        "转发",
        "校验",
        "清单",
        "构建",
        "签名",
        "令牌",
        "进程",
        "运行时",
        "架构",
        "退出码",
        "错误代码",
        "开发版",
        "调试",
        "联调",
        "测试线路",
        "内部",
        "第.轮",
        "仓库",
        "开源",
        "许可证",
        "第三方组件",
        "源代码",
        "源码",
        "管理员(?!账号|权限)",
        "结算",
        "2³⁰",
        "1024³",
    ]
    static let forbiddenEn = [
        "Core",
        "Mihomo",
        "Clash",
        "Helper",
        "GiB",
        "MiB",
        "KiB",
        "GPL",
        "Apache",
        "sing-tun",
        "GitHub",
        "handoff",
        "channel",
        "generation",
        "handshake",
        "protocol",
        "metering",
        "settle",
        "ledger",
        "tunnel",
        "Tunnel",
        ".env",
        "localhost",
        "debug",
        "Debug",
    ]
    /// D8: a development build may show the neutral marker "开发版"; a release build never does. The
    /// "调试" section of the account page exists only in Debug builds (#if DEBUG) and is the test target.
    static let allowedPhrases = ["隧道调试（第 2 轮）", "调试", "开发版"]

    /// The forbidden words found in `texts` (empty when the copy is clean).
    static func violations(in texts: [String]) -> [String] {
        var found: [String] = []
        for original in texts {
            var text = original
            for phrase in allowedPhrases { text = text.replacingOccurrences(of: phrase, with: "") }
            var words = forbiddenZh.filter { matches($0, in: text) }
            words += forbiddenEn.filter { matches("(?<![A-Za-z])" + NSRegularExpression.escapedPattern(for: $0) + "(?![a-z])", in: text) }
            found += words.map { "\"\($0)\" in \"\(original)\"" }
        }
        return found
    }

    private static func matches(_ pattern: String, in text: String) -> Bool {
        text.range(of: pattern, options: .regularExpression) != nil
    }
}
