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
	"profile_changed":                   "线路配置已更新，请重新选择线路",
	"client_config_unavailable":         "线路配置暂不可用，请稍后重试或联系管理员",
	"subscription_changed":              "套餐已变更，请重新登录",
	"device_limit":                      "已达到套餐同时在线设备上限，请先在其他设备退出",
	"traffic_sequence":                  "流量批次未确认，请稍后重试",
	"traffic_conflict":                  "流量批次冲突，请重新登录",
	"invalid_managed_selection":         "所选线路无效",
	"node_catalog_disabled":             "服务端尚未开启线路列表",
	"auth_unavailable":                  "登录服务暂时不可用，请稍后重试",
	"client_service_unavailable":        "服务暂时不可用，请稍后重试",
	"rate_limited":                      "请求过于频繁，请稍后再试",
	"network_unreachable":               "无法连接服务器，请检查网络",
	"tls_failure":                       "安全连接失败，请检查网络或系统时间",
	"bad_response":                      "服务器返回了无法识别的数据",
	"lease_expired":                     "授权已过期，正在重新验证",
	"config_rejected":                   "线路配置未通过安全校验，已拒绝使用",
	"stale_generation":                  "状态已变化，请刷新后重试",
	"stale_catalog":                     "线路列表已更新，请重新选择",
	"not_signed_in":                     "请先登录",
	"node_unavailable":                  "所选线路已不可用",
	"tun_unavailable":                   "此版本不含隧道组件，无法连接",
	"vpn_conflict":                      "检测到其他 VPN 正在运行，请先断开后再连接",
	"no_network":                        "未检测到可用的物理网络，请检查网络连接",
	"tunnel_failed":                     "隧道启动失败，请稍后重试或联系管理员",
	"tunnel_unhealthy":                  "已建立隧道但无法经线路访问网络，已断开；请更换线路或稍后重试",
	"traffic_unconfirmed":               "最近一批流量尚未被服务器确认，请稍后重试",
	"invalid_core_counters":             "流量计数异常，已断开，请重新登录",
	"lifecycle_reconfirmation_required": "系统休眠或时钟变化，已断开，请重新连接",
	"network_changed":                   "网络环境发生变化，已断开，请重新连接",
	"invalid_traffic":                   "流量计数无效",
	"session_mismatch":                  "会话状态不一致，请重新登录",
	"already_connected":                 "已处于连接状态",
	"invalid_api_base":                  "应用的服务器地址配置无效",
	"invalid_request":                   "请求格式不正确",
	"grant_expired":                     "连接授权已失效，请重新连接",
	"grant_timeout":                     "线路节点未能及时就绪，请稍后重试或更换线路",
	"agent_unavailable":                 "该线路的节点暂不可用，请更换线路或稍后重试",
	"metering_source_mismatch":          "此登录会话已使用另一种计量方式，请重新登录",
	"client_upgrade_required":           "应用版本过低，请升级后再连接",
	"grant_not_found":                   "连接授权不存在，请重新连接",
	"idempotency_conflict":              "请求标识冲突，请重试",
	"handoff_invalid":                   "会话交接数据无效，请重新登录",
	"handoff_expired":                   "会话交接已过期，请重新登录",
	"handoff_stale":                     "会话已在别处接管，请重新登录",
	"handoff_busy":                      "当前已有登录会话，无法接管",
	"handoff_mismatch":                  "会话状态不一致，请重新登录",
	"handoff_conflict":                  "会话账本不一致，请重新登录",
	"handoff_unsettled":                 "最近的流量尚未结清，暂时无法切换，请稍后重试",
	"handoff_required":                  "请在 AsterLink App 内连接",
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
