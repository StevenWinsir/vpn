# NodeAgent XUDP 兼容与强制计费维护记录

## 基线与问题复现

本轮基于已合并 PR #30 的 `e8dcd7a9c683af8f9b338fbba76803ba9aacb5c8`，分支 `fix/nodeagent-vless-xudp`，在独立 worktree 开发。PR #29 的发现保留于 [Android TUN 设计 §9](android-tun-design.md#9-findings-for-other-platforms-and-later-rounds)。不改 Mihomo gitlink `70f0570405c3c2c47bb113b88db95006d239b346` 或 PR #30 的生命周期补丁。

协议依据是该 pin 的 `adapter/outbound/vless.go`、`transport/vless/conn.go` 及其锁定依赖 `sing-vmess v0.2.5` 的 `xudp.go` / `mux.go`，不是猜测最新 Clash 行为：默认 `PacketEncoding` 为空也会开启 XUDP。VLESS command 3 后立即开始 mux 帧，**没有**普通 VLESS 的 port/address 字段。`v1.mux.cool` 是逻辑目的地，不是需要 DNS 解析或管理员配置的目标服务器。

先加入 `TestMihomoXUDPRoundTripAndPerGrantCounters`，在未修复的 forwarder 上运行：真实 pinned `outbound.NewVless` + 默认 `ListenPacketContext`，第一个 17 B UDP 数据报读到 EOF，测试 FAIL。实现 XUDP 后同一测试通过，两个 grant 分别观察到 up/down 各 4,813 B 和 31 B；没有计入 VLESS/mux 头。测试不使用手写 VLESS 客户端充当兼容证据。

## 实现与安全边界

`native/nodeagent/forwarder/xudp.go` 仍只依赖 Go 标准库。

- 先用外层 UUID 找到有效 grant，再注册整个 carrier，随后才读 mux 帧和解析/连接目标。每条 carrier 的所有 session 共享同一个 grant 的原子 epoch 额度；不能用 session ID 或 global ID 改变计费归属。
- 支持 UDP New/Keep/End/KeepAlive；New 可带 8 B global ID，Keep 可省略地址或明确切换目标。global ID 不跨连接恢复/共享 UDP socket。拒绝 TCP mux、未知状态/选项、重复 New、未知 session、错误地址与非法长度；错误关闭 carrier。
- IPv4、IPv6、域名目标使用相同的受保护 dialer。DNS 解析后再检查地址，每个新目标单独检查；连接式 UDP socket 固定已经检查的地址并限定返回源。默认拒绝私网、loopback、link-local 等；测试才显式允许私网。
- 每条 carrier 最多 64 个 session、合计 64 个目标；头部最多 272 B，数据帧由 uint16 长度限定。End 回收对应 socket/reader；这是每条 carrier 的边界，不是全局连接限额。
- 上下行只记负载，完整数据报接纳或整体拒绝。复用的 `writeDatagram` 在写入前发布计数，再退还实际未写出的负载；普通 command 2 同时修正了“数据报超过剩余额度时预留部分额度却未退回”的问题。短写只按实际写出的负载结算，不包含头部。
- Remove 关闭 carrier 的 TCP/UDP socket、取消正在进行的 DNS/dial、等待 parser 和全部 reader 完成，然后返回最终计数。到期/额度耗尽由已有独立 watcher 和逐包检查强制执行；不依赖客户端继续向 API 请求。

不新增 API、数据库迁移或 `.env` 配置，不放开共享策略字段，不引入 Reality、XTLS、TCP 多路复用或 packetaddr 的商业承诺。节点 TLS 仍是公网部署前提。

## 自动回归覆盖

| 层级 | 测试内容 |
| --- | --- |
| `native/nodeagent/forwarder` | 保留 TCP/plain UDP；XUDP 多 session、地址省略/切换、域名/IPv6、空数据报、End/reuse、KeepAlive、头/目标数量上限、非法状态/长度/地址、New/Keep 私网拒绝、未完成帧的 Remove、短写退款、原子整包额度、解析器 fuzz |
| `native/core/integration` | 真实 pinned Mihomo 默认 XUDP 的 New/Keep、有/无 global ID、多目标往返、精确 per-grant 计数；相同 global ID 不跨 grant 共享授权；存量/新连接 revoke、空闲到期、未知 UUID、私网拒绝、整包额度与 top-up、并发 carrier 的总额度、带负载 Remove |
| `asterlink-billtest --udp-sink` | 改用真实 pinned Mihomo，不发送 `/traffic`，不建立 TUN；报告显式标记 XUDP。正例通过；旧 command-2-only forwarder 必须 FAIL；不撤销存量流的伪节点也必须 FAIL |
| 后端 loopback acceptance | `scripts/test-native.py -run TestBilltest ./internal/api` 构建真实 NodeAgent、sink、billtest，使用独立本机 PostgreSQL、真实 API 和 mTLS 控制面验证更新后的 UDP probe |
| CI | 主 CI 全模块 race/vet；协议矩阵增加 Linux/macOS 的 forwarder + pinned-client integration；Windows CI 显式运行同一 integration（无 TUN） |

本机 macOS arm64 / Go 1.26.6：NodeAgent/Core 全模块 `go test -race -count=1 ./...` 通过。真实组件 loopback 两个 PostgreSQL 测试通过：B04-node-bills 无客户端 `/traffic`，500‰ 结算；TCP+XUDP 撤销约 0.8–1 s；空闲到期 14 s；套餐终止约 4 s；三设备竞争时最终用量为 6,291,456,000 / 6,291,456,000 units，没有超额。这些时序受 1 s agent sync、250 ms 采样影响，不是生产承诺。

补充压力与静态检查：真实 pinned-client integration 在 race 下连续 20 次通过；解析器双 worker fuzz 20 s、1,176,206 次执行无崩溃；NodeAgent/Core `go vet ./...` 和 Core `go vet -tags fakeengine ./...` 通过。

复现（先初始化子模块；Go 1.26.6，与 CI 一致）：

```sh
python3 scripts/prepare_mihomo.py
(cd native/nodeagent && go test ./... && go test -race -count=1 ./... && go vet ./...)
(cd native/core && go test ./... && go test -race -count=1 ./... && go vet ./...)
(cd native/core && go test -race -count=20 ./integration)
(cd native/nodeagent && go test -run='^$' -fuzz=FuzzReadXUDPFrame -fuzztime=20s -parallel=2 ./forwarder)
python3 scripts/test-native.py -run TestBilltest ./internal/api
```

最后一条需要本机 `initdb` / `pg_ctl` 位于 PATH。脚本仅创建临时 Unix-socket PostgreSQL，不使用项目开发服务器的数据库凭据。

## 部署与证据边界

部署新 NodeAgent 是让已安装 native 客户端恢复 UDP 的关键；既有 macOS/Windows/Android 客户端已经默认发 XUDP，不需为本修复改变 pin、节点 YAML 或 packet encoding。使用新的 billtest 二进制重新运行 [Windows grants acceptance](windows-grants-acceptance.md) 的 `--udp-sink` 流程。

自动测试证明实际 pinned 客户端到实际参考 forwarder 的 UDP 与节点强制边界；**不等于**真实公网 TLS 节点、Windows 10/11 或 Android/macOS OS TUN 的重新验收。历史 Android `artifacts/native-android/tun/tun-report.json` 不改写为本轮证据。没有部署服务、使用远端测试数据库、更改系统网络或停止已有 Android 模拟器。
