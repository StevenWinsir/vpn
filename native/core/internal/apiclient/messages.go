package apiclient

// Chinese presentation for the stable server/Core error codes (handoff_app.md 5.1).
// Control flow branches on the code, never on this text.
var messages = map[string]string{
	"invalid_credentials":        "邮箱或密码不正确",
	"invalid_client_login":       "登录信息格式不正确",
	"native_session_required":    "请重新登录",
	"native_session_expired":     "登录已失效，请重新登录",
	"upgrade_required":           "当前账号没有有效套餐，请前往网站购买",
	"paid_vip_required":          "需要有效的付费套餐，请前往网站购买或续费",
	"subscription_expired":       "套餐已到期，请前往网站续费",
	"quota_exhausted":            "套餐流量已用完，请前往网站续费或升级",
	"profile_required":           "请先选择一个线路",
	"profile_changed":            "线路配置已更新，请重新选择线路",
	"client_config_unavailable":  "线路配置暂不可用，请稍后重试或联系管理员",
	"subscription_changed":       "套餐已变更，请重新登录",
	"device_limit":               "已达到套餐同时在线设备上限，请先在其他设备退出",
	"traffic_sequence":           "流量批次未确认，请稍后重试",
	"traffic_conflict":           "流量批次冲突，请重新登录",
	"invalid_managed_selection":  "所选线路无效",
	"node_catalog_disabled":      "服务端尚未开启线路列表",
	"auth_unavailable":           "登录服务暂时不可用，请稍后重试",
	"client_service_unavailable": "服务暂时不可用，请稍后重试",
	"rate_limited":               "请求过于频繁，请稍后再试",
	"network_unreachable":        "无法连接服务器，请检查网络",
	"tls_failure":                "安全连接失败，请检查网络或系统时间",
	"bad_response":               "服务器返回了无法识别的数据",
	"lease_expired":              "授权已过期，正在重新验证",
	"config_rejected":            "线路配置未通过安全校验，已拒绝使用",
	"stale_generation":           "状态已变化，请刷新后重试",
	"stale_catalog":              "线路列表已更新，请重新选择",
	"not_signed_in":              "请先登录",
	"node_unavailable":           "所选线路已不可用",
	"tun_unavailable":            "隧道连接将在后续版本提供",
	"invalid_api_base":           "应用的服务器地址配置无效",
	"invalid_request":            "请求格式不正确",
}

// Message returns the Chinese text for code, or a generic line for unknown codes.
func Message(code string) string {
	if text, ok := messages[code]; ok {
		return text
	}
	return "操作失败，请稍后重试"
}

// TerminatesSession reports whether code means the bearer token is no longer usable
// and every piece of temporary identity must be dropped.
func TerminatesSession(code string) bool {
	switch code {
	case "native_session_required", "native_session_expired", "subscription_changed", "invalid_credentials":
		return true
	}
	return false
}

// InvalidatesConfig reports codes after which an already-fetched node config must
// not be reused (the user must select and confirm again).
func InvalidatesConfig(code string) bool {
	switch code {
	case "profile_changed", "client_config_unavailable", "profile_required", "upgrade_required",
		"paid_vip_required", "subscription_expired", "quota_exhausted":
		return true
	}
	return false
}
