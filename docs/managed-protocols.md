# 托管节点协议支持与安全边界

## 支持依据与部署

本项目按 `native/third_party/Clash.Meta` gitlink **70f0570405c3c2c47bb113b88db95006d239b346** 的实际适配器实现验证，不以最新网上文档或任意 Clash 分支作为兼容依据。升级子模块时，CI 会要求同步审查协议策略及样例，并直接检查已允许的顶层字段是否存在于 core 的对应 Option 结构体。运行共享模块的兼容测试前需要初始化该子模块。

协议规则的唯一维护源是 `shared/nodepolicy`，不依赖 Mihomo；API 不启动代理、不解析 DNS、不探测管理员提交的服务器。客户端继续直接引用它，保留原生构建输入追踪。后端改用随目录交付的 `backend/nodepolicy` 精确快照：`scripts/sync-backend-shared.py --check` 在 CI 逐字节检查它与源文件一致，禁止独立修改后端规则。现在单独复制完整 `backend/` 即可编译/测试，不需要同级 FlClash 目录或 Mihomo 子模块。部署操作见 [backend/DEPLOYMENT.md](../backend/DEPLOYMENT.md)。

更新后需要重新编译后端和 macOS 客户端。旧客户端的托管白名单并不包含所有新增类型；应先更新测试客户端，再导入新协议节点。管理员 `/admin/nodes` 从 API 返回的 `supported_protocols` 显示支持列表；这不是客户端版本协商机制。不需要数据库迁移、修改 `.env` 或重新录入已有的合法 SS/HTTP/SOCKS5 节点。

## 协议矩阵

下表是**管理员可导入、客户端可解析的配置子集**，不代表 NodeAgent 已能为所有这些协议做可信商业计费。节点侧能力见后文；不得把配置解析成功当作 `node_reported` 验收通过。

| YAML `type` | 托管支持范围 |
| --- | --- |
| `ss` | AES-128/256-GCM、ChaCha20-IETF-Poly1305；SS2022 的三种 Blake3 AEAD；obfs、v2ray-plugin WebSocket、ShadowTLS 插件 |
| `ssr` | 策略内列举的常用 cipher、protocol 和 obfs；仅兼容已有线路，不表示推荐旧密码套件 |
| `http` / `socks5` | 内联服务器、认证字段及受信任 TLS；SOCKS5 可启用 UDP |
| `vmess` | UUID、cipher、alterId，TCP / WS / HTTP / H2 / gRPC |
| `vless` | UUID，TCP / WS / HTTP / H2 / gRPC；TLS、Reality、XTLS Vision；Vision 要求 TCP + TLS |
| `trojan` | TCP / WS / gRPC、TLS、Reality |
| `hysteria` / `hysteria2` | 内联认证、TLS、带宽参数及已允许的混淆；Hysteria2 支持 salamander |
| `tuic` | v4 token 或 v5 UUID + password（不能混用） |
| `wireguard` | 单 peer 或内联 `peers` 列表、IPv4/IPv6 地址、密钥、预共享密钥、reserved、MTU、allowed-ips |
| `snell` | core 支持的 1–5 版本及常用混淆 |
| `anytls` | 内联密码、TLS 及受限的会话选项 |
| `ssh` | 密码或内联 PEM 私钥；必须提供 `host-key` 固定服务器公钥 |
| `mieru` | TCP/UDP、用户名密码、multiplexing |

这是 **15 种协议的托管子集，不是放开 Mihomo 的所有选项**。精确字段及类型以 `nodepolicy/policy.go` 为准。未审查字段会被拒绝，不会被静默丢弃。`direct`、`dns`、`reject`、`rematch` 不是可售代理节点；Tailscale、ZeroTier、OpenVPN、ShadowQUIC、MASQUE 等此补丁没有开放。存在后台控制面、文件依赖或多出口行为的其他协议/高级选项需要单独审查及端到端测试。

WireGuard 的 `private-key` 是严格验证的 32 字节 Base64 内容，不是文件路径。`ip`、`ipv6` 填写地址而非 CIDR，`allowed-ips` 填写 CIDR。`peers` 不能与根级 peer 服务器/公钥混用；每个 peer 必须明确服务器、端口、公钥和 allowed-ips。同一节点内多个 peers 共用该节点的一个倍率。当前禁止非零 `persistent-keepalive` 和 `remote-dns-resolve: true`，避免增加脱离受控连接的后台流量来源。常规握手/重传等协议开销不属于客户端应用数据计量承诺。

所有 TLS 节点均拒绝 `skip-cert-verify: true`。证书文件路径、外部插件可执行文件、未知嵌套参数、外部 providers、任意 rules/listeners、`dialer-proxy`、`interface-name` 和 `routing-mark` 不对管理员导入开放。WS 的 `path` 是受限的 HTTP 路径字符串，不代表允许读取本地文件。Reality 的 `public-key` 为 32 字节无 padding Base64URL，`short-id` 为至多 16 位的偶数位十六进制**字符串**；数字形式请加引号。Hysteria 带宽按字符串填写，例如 `'100 Mbps'`。

## 导入和计费

编辑器仍接受唯一的 `proxies:` 列表，不接受整份客户端运行配置。最多 128 个节点、64 KiB 输入，并限制递归深度、树大小、字符串与数组长度；禁止 YAML 别名、锚点、重复键、多文档和合并键。验证失败只返回节点序号和固定错误，不回显密码、UUID、私钥或未知字段原文。批量中任何一项失败，整批不写入数据库。

嵌套选项经 YAML 规范化后整体 AES-GCM 加密存储，不拆散丢弃 Reality/WS/gRPC/WireGuard 配置。管理员列表和变更响应仍只有元数据。客户端登录、套餐/线路授权后仅下发所选节点凭据，其余可选节点只有元数据。Core 先重复验证共享策略，再执行真正的 Mihomo 语义解析。退出和替换配置时主动关闭旧适配器，避免依赖 GC 才释放 WireGuard 的用户态网络栈。

倍率和计费不按协议更改：`charged_units = (upload_delta + download_delta) * rate_permille`，1000 单位为 1 字节；500‰ = 0.5×，1000‰ = 1×。费率、节点和套餐绑定取自后端会话，不能由流量上报请求覆盖。累计计数、顺序、防重放、旧倍率结清、禁用节点、过期套餐与配额控制沿用现有逻辑。

这里必须区分两种计量来源。`client_reported` 仍只适合内部开发：TLS、幂等序列、隐藏 YAML 或内嵌签名密钥都不能证明设备所有者没有少报。`node_reported` 则使用节点侧 per-grant UUID、可信累计负载计数、epoch 字节上限、绝对到期和存量连接撤销；客户端 `/traffic` 不能再给这些 grant 记账。后者是后续商业计费轮引入的能力，不是上面的协议导入白名单自动提供的保证。

### NodeAgent 参考转发器的商业能力

| 传输 | 节点侧能力及边界 |
| --- | --- |
| VLESS v0 TCP（command 1）+ TLS | per-grant 认证、负载计数、额度、到期、Remove 关闭存量连接 |
| VLESS plain UDP（command 2）+ TLS | 保留原有兼容路径；数据报完整接纳或拒绝，不截断，不计封装开销 |
| VLESS XUDP（command 3 / mux.cool）+ TLS | 支持固定 Mihomo 的默认 UDP；New/Keep/End/KeepAlive、IPv4/IPv6/域名、帧内目标切换；沿用相同 grant、额度、到期与撤销边界 |
| 通用 TCP Mux、Reality、XTLS flow、packetaddr、其他协议 | 参考 NodeAgent **未提供**相应商业能力；不能据客户端可解析就加入商业支持矩阵 |

XUDP 的外层 command 3 后没有目标地址；`v1.mux.cool` 是逻辑 mux 目的地，不应写成 YAML 的节点 `server`。真实 UDP 目标来自帧内地址，每个目标都经过解析后的私网检查，只有测试显式设置 `AllowPrivateTargets` 才允许私网。每条 carrier 至多 64 个 UDP session、合计 64 个目标；global ID 只按格式读取，不跨 carrier/grant 复用身份或 socket。TCP/mux/TLS 封装不计费。详情和复现命令见 [NodeAgent XUDP 维护记录](nodeagent-xudp.md)。

已有 native 客户端默认就在发送 XUDP，本轮修复需要重新构建并部署 **NodeAgent**；无需为此升级 Mihomo pin、重新录入节点 YAML、添加 `packet-encoding` 或迁移数据库。验收工具需重建，`--udp-sink` 现使用真实 pinned Mihomo XUDP，不允许静默回退到 command 2。公网必须启用节点 TLS；本机 loopback 和 CI 通过不等于已完成真实公网 TLS 节点、各平台 TUN、支付或签名公证发布验收。

## 可复现检查与证据范围

```sh
python3 scripts/prepare_mihomo.py
(cd native/nodeagent && go test -race -count=1 ./...)
(cd native/core && go test -race -count=1 -v ./integration)
python3 scripts/sync-backend-shared.py --check
python3 scripts/test-backend-shared.py
python3 scripts/test-backend-standalone.py
(cd shared/nodepolicy && go test -race -count=1 ./... && go vet ./...)
(cd backend && go test -count=1 ./internal/nodes)
(cd backend && go test -run='^$' -fuzz=FuzzParseNodeYAML -fuzztime=20s -parallel=2 ./internal/nodes)
python3 scripts/test-native.py
(cd native/legacy-core && CGO_ENABLED=0 go test -count=1 ./...)
(cd native/legacy-core && CGO_ENABLED=0 go vet ./... && go test -race -count=1 ./managed)
bash scripts/node.sh npm --prefix frontend run lint
bash scripts/node.sh npm --prefix frontend run build -- --webpack
bash scripts/node.sh npm --prefix frontend run typecheck
bash scripts/node.sh python3 scripts/test-node-catalog.py --skip-build --output artifacts/protocol-catalog
```

PostgreSQL 工具和 Go 须位于 PATH。测试创建隔离的本机 Unix-socket PostgreSQL，不访问开发服务器。目录验收输出请使用新的目录名。

30 组脱敏配置覆盖 15 种协议和常见传输组合，验证 API 解析、规范化往返、数据库加密、单节点下发、绑定费率及实际锁定 core 的 Prepare/Apply。新增 VLESS 测试使用仅绑定回环的真实入站/出站，验证正确与错误 UUID 和真实 TCP 数据往返；其明文 TCP 是隔离认证变量的测试条件，不是推荐的公网部署方式。Reality 和 WireGuard 的样例测试是**解析/配置应用级**，并不验证商业公网节点的握手、带宽、UDP、DNS 或 TUN。浏览器验收导入/重新编辑 VLESS Reality 和 WireGuard（禁用的解析样例），原有真实转发与 0.5×/1× 数据库结算回归继续执行。

新增 `.github/workflows/node-protocols.yml` 运行策略/race/模糊测试和 Linux/macOS core 兼容测试；现有主 CI 继续负责数据库竞态、浏览器、完整 core、Flutter 和原生 macOS 构建。CI 是否通过应以该 PR 的实际 Checks 为准。
