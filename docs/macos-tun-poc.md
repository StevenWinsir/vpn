# macOS 原生 TUN 概念验证（阶段 1，仅限受控测试机）

`native/tunpoc` 是**独立 Go 模块**，不进入 App 包，也不被 `build-native-macos.py` 构建。
它启动固定提交的 Mihomo 原生 TUN（`stack: system`），只连一个由你提供的测试 Shadowsocks 节点，
然后证明：

1. 不设置任何 HTTP/SOCKS/系统代理，一个 `Proxy: nil` 的 HTTP 请求出口地址发生变化；
2. 关闭后没有遗留 `utun`、IPv4/IPv6 路由或 `scutil --dns` 变化；
3. 结果写入 `artifacts/native-macos/tun-<时间>/tun-poc-report.json`（不含密码，节点主机只记哈希前缀）。

## 运行（会临时修改本机路由和 DNS）

```bash
export ASTERLINK_POC_SS_SERVER=<测试节点主机>
export ASTERLINK_POC_SS_PORT=<端口>
export ASTERLINK_POC_SS_PASSWORD=<密码>          # 只经环境变量，不进 argv/日志
export ASTERLINK_POC_EXPECT_EXIT=<节点出口IP>      # 可选
sudo -E python3 scripts/test-native-macos.py --suite tun --allow-network-changes
```

安全措施：缺少 `--allow-network-changes`、非 root 或缺少环境变量一律报 `BLOCKED`（退出码 3），
不会用 mock 冒充通过；整个运行有 `--max-seconds`（默认 90）硬上限，并响应 SIGINT/SIGTERM。

## 异常后手动恢复

```bash
sudo pkill -f tunpoc            # 进程退出时 Mihomo 会拆除 utun 与路由
ifconfig | grep -A2 '^utun'      # 确认多出来的 utun 已消失
scutil --dns | head -20          # 确认 DNS 回到原值
sudo networksetup -setdnsservers Wi-Fi Empty   # 仅当 DNS 被残留修改时
```

## 已核实的上游事实

锁定的 FlClash 分支 Mihomo（`70f0570`）中 `executor.ApplyConfig` **故意不启动监听器**
（`updateListeners` / `updateTun` 被注释），所以 TUN 必须在 `ApplyConfig` 之后显式调用
`listener.ReCreateTun(cfg.General.Tun, tunnel.Tunnel)`，关闭时传入零值 `LC.Tun{}`。
阶段 2/3 的 EngineAdapter 必须沿用这一点。
