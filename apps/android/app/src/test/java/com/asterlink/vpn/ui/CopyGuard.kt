package com.asterlink.vpn.ui

/**
 * The runtime half of the copy guard (handoff_app_standardization.md 7.4): the screenshot tests check all
 * the text of every screen they render against the same word lists as the static scan.
 * scripts/test-app-copy.py compares the two lists below with its own and fails on any difference,
 * so edit the lists there first.
 */
object CopyGuard {
    val forbiddenZh = listOf(
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
    )
    val forbiddenEn = listOf(
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
    )

    /** D8: a development build may show the neutral marker "开发版"; a release build never does. */
    private val allowedPhrases = listOf("开发版")

    /** The forbidden words found in [texts] (empty when the copy is clean). */
    fun violations(texts: Collection<String>): List<String> = texts.flatMap { original ->
        val text = allowedPhrases.fold(original) { acc, phrase -> acc.replace(phrase, "") }
        val words = forbiddenZh.filter { Regex(it).containsMatchIn(text) } +
            forbiddenEn.filter { Regex("(?<![A-Za-z])" + Regex.escape(it) + "(?![a-z])").containsMatchIn(text) }
        words.map { "\"$it\" in \"$original\"" }
    }
}
