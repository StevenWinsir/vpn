package apiclient

// Chinese presentation for the stable server/Core error codes (handoff_app.md 5.1).
// Control flow branches on the code, never on this text.
var messages = map[string]string{
	"invalid_credentials":               "邮箱或密码不正确",
	"invalid_client_login":              "登录信息格式不正确",
	"native_session_required":           "请重新登录",
	"native_session_expired":            "登录已失效，请重新登录",
	"upgrade_required":                  "当前账号没有有效套餐，请前往网站购买",
	"paid_vip_required":                 "需要有效的付费套餐，请前往网站购买或续费",
	"subscription_expired":              "套餐已到期，请前往网站续费",
	"quota_exhausted":                   "套餐流量已用完，请前往网站续费或升级",
	"profile_required":                  "请先选择一个线路",
	"profile_changed":                   "线路已更新，请重新选择线路",
	"client_config_unavailable":         "线路暂不可用，请稍后重试或联系客服",
	"subscription_changed":              "套餐已变更，请重新登录",
	"device_limit":                      "已达到同时在线设备上限，请先断开其他设备",
	"traffic_sequence":                  "正在同步用量，请稍后重试",
	"traffic_conflict":                  "用量同步异常，请重新登录",
	"invalid_managed_selection":         "所选线路无效",
	"node_catalog_disabled":             "线路列表暂不可用，请稍后重试",
	"auth_unavailable":                  "登录服务暂时不可用，请稍后重试",
	"client_service_unavailable":        "服务暂时不可用，请稍后重试",
	"rate_limited":                      "请求过于频繁，请稍后再试",
	"network_unreachable":               "无法连接服务器，请检查网络",
	"tls_failure":                       "安全连接失败，请检查网络或系统时间",
	"bad_response":                      "服务暂时异常，请稍后重试",
	"lease_expired":                     "正在重新验证账户",
	"config_rejected":                   "该线路暂不可用，请更换线路",
	"stale_generation":                  "状态已变化，请刷新后重试",
	"stale_catalog":                     "线路列表已更新，请重新选择",
	"not_signed_in":                     "请先登录",
	"node_unavailable":                  "所选线路已不可用",
	"tun_unavailable":                   "当前版本无法建立连接，请前往官网下载最新版本",
	"vpn_conflict":                      "检测到其他网络加速工具正在运行，请先关闭后再连接",
	"no_network":                        "未检测到可用的物理网络，请检查网络连接",
	"tunnel_failed":                     "连接失败，请更换线路或稍后重试，或联系客服",
	"tunnel_unhealthy":                  "该线路暂时无法访问网络，已断开，请更换线路或稍后重试",
	"traffic_unconfirmed":               "正在同步用量，请稍后重试",
	"invalid_core_counters":             "用量同步异常，已断开，请重新登录",
	"lifecycle_reconfirmation_required": "设备休眠或网络变化，已断开，请重新连接",
	"network_changed":                   "网络环境发生变化，已断开，请重新连接",
	"invalid_traffic":                   "用量同步异常，请重新登录",
	"session_mismatch":                  "登录状态已变化，请重新登录",
	"already_connected":                 "已处于连接状态",
	"invalid_api_base":                  "应用安装不完整，请从官网重新下载安装",
	"invalid_request":                   "操作失败，请稍后重试",
	"grant_expired":                     "连接已失效，请重新连接",
	"grant_timeout":                     "线路响应超时，请更换线路或稍后重试",
	"agent_unavailable":                 "该线路暂不可用，请更换线路或稍后重试",
	"metering_source_mismatch":          "登录状态已变化，请重新登录",
	"client_upgrade_required":           "应用版本过低，请升级后再连接",
	"grant_not_found":                   "连接已失效，请重新连接",
	"idempotency_conflict":              "操作冲突，请重试",
	"handoff_invalid":                   "登录状态已变化，请重新登录",
	"handoff_expired":                   "登录状态已变化，请重新登录",
	"handoff_stale":                     "登录状态已变化，请重新登录",
	"handoff_busy":                      "当前已有登录，请先退出后再试",
	"handoff_mismatch":                  "登录状态已变化，请重新登录",
	"handoff_conflict":                  "登录状态已变化，请重新登录",
	"handoff_unsettled":                 "正在同步用量，请稍后重试",
	"handoff_required":                  "请在 AsterLink App 内连接",
	"group_full":                        "该线路当前已满，请稍后再试或选择其他线路",
	"group_unavailable":                 "该线路暂无可用服务器，请选择其他线路",
	"connection_reassigned":             "线路服务器已调整，正在为你重新连接",
	"connection_not_found":              "连接已失效，请重新连接",
	"connection_expired":                "连接已失效，请重新连接",
	"grant_required":                    "当前版本不支持该线路，请升级到最新版本",
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
	case "native_session_required", "native_session_expired", "subscription_changed", "invalid_credentials", "metering_source_mismatch":
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
