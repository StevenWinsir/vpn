# 阶段 4：节点权威计量、独立凭据与强制停用

> 范围：让"用户改客户端、不上报流量、拿走已授权凭据"也无法逃避节点侧的授权与计费。
> 状态：**代码与回环端到端验收完成；真实商业节点与真机 TUN 联调仍待你的节点**（见第 7 节）。
> 契约：[`shared/contracts/native-v2.json`](../shared/contracts/native-v2.json)（API、Core、Agent 三方共用，均有测试读取）。

## 1. 它解决什么

阶段 3 的账本是 `client_reported`：流量由客户端自报，共享代理密码一旦被提取就无法撤销。阶段 4 增加第二种计量来源 `node_reported`：

| | `client_reported`（开发） | `node_reported`（收费） |
| --- | --- | --- |
| 凭据 | 管理员 YAML 里的共享密码 | 每用户、每次连接独立生成（grant） |
| 谁计量 | 客户端 `POST /traffic` | 节点上的 Agent，直接读转发器计数 |
| 撤销/到期 | 只靠客户端自觉 | 节点关闭存量 TCP/UDP，且按绝对截止时间自停 |
| 额度 | 事后扣量 | 事务内分配有界预算，节点只在预算内转发 |
| 生产环境 | 代码级拒绝（`426 client_upgrade_required`） | 唯一允许的代理交付方式 |

一个登录会话只绑定其中一种来源：`node_reported` 会话调用 `/traffic`、或 `client_reported` 会话申请 grant，都返回 `409 metering_source_mismatch`，不可能对同一批字节扣两次。

## 2. 组件

```text
macOS 客户端 (Swift + Core)            控制面 (Go/Gin + PostgreSQL)              节点 (你的服务器)
  GET /capabilities ───────────────▶   capabilities
  POST /grants {node_id} ─────────────▶ 事务：校验权益/设备数/节点代理在线
                                        预留预算 → grant=pending
  GET /grants/:id (轮询)  ◀── pending                       ◀── POST /internal/v1/node-agents/sync (mTLS)
                                                              Agent 取到 install 指令 → 装入转发器
                                        Agent 确认已安装 → grant=ready
  GET /grants/:id ◀── ready + yaml(含独立凭据)
  Mihomo TUN 用该凭据连接 ─────────────────────────────────▶ 转发器按 UUID 归属、计数、可立即踢线
                                                              POST /internal/v1/node-agents/usage (mTLS)
                                        累计幂等入账（倍率按 grant 绑定值）
  POST /grants/:id/revoke ────────────▶ revoking ── 下一次 sync 下发 revoke ──▶ 关闭该用户所有连接
                                        Agent 发 final 用量 → 确认移除 → revoked，归还未用预算
```

* **Agent 身份**来自 mTLS 客户端证书（链校验 + 管理员登记的 SHA-256 指纹固定）。节点 ID 取自证书，**绝不取自请求体**。
* **只有 Agent 确认已安装，客户端才拿得到凭据。** pending 状态没有任何凭据可取。
* **管理员的共享密码从不下发**：grant 的 YAML 由服务端用该 grant 的独立凭据重新生成，版本哈希就是该文档的 SHA-256。
* **协议能力三列分开**：*解析支持*（nodepolicy）/ *TUN 实传通过*（阶段 3）/ *节点权威控制通过*（本阶段）。Agent 只上报它的转发器真正强制了的协议；服务端按这个清单决定能否发 grant。当前参考转发器仅声明 `vless`。**SS 没有声明**——共享端口的 AEAD 服务端无法区分用户，需要"每 grant 独立端口"的适配器，尚未实现，所以 SS 节点不能成为权威节点。

## 3. 服务端配置（`backend/.env`）

新增键见 [`backend/.env.example`](../backend/.env.example)。要点：

* `NODE_AGENT_ADDR` + `NODE_AGENT_TLS_CERT_FILE` + `NODE_AGENT_TLS_KEY_FILE` + `NODE_AGENT_CLIENT_CA_FILE`：第二个监听端口（mTLS，TLS 1.3，无证书的连接到不了任何处理器）。请绑定内网/防火墙内地址。
* `APP_ENV=production` 现在的规则：`CLIENT_PROFILE_DIR` 一律拒绝；`CLIENT_NODE_CATALOG_ENABLED=true` 仅当同时配置了 `NODE_AGENT_ADDR`；`/config`、`/traffic` 由代码拒绝；`/nodes` 只列有节点代理的线路。**没有任何"默认开启"的开关取代旧保护。**
* `GRANT_BUDGET_BYTES`（默认 512 MiB）：每个 grant、每次续预算发给节点的原始字节额度，决定用户在两次上报之间最多能"超用"多少。
* `NODE_AGENT_STALE_SECONDS`（默认 30）、`NODE_AGENT_SETTLE_GRACE_SECONDS`（默认 60）、`CLIENT_MIN_APP_VERSION`（默认空）。
* 数据库：`AUTO_MIGRATE` 或 `go run ./cmd/api -migrate`，**只新增**表 `node_agents`、`subscription_periods`、`connection_grants`、`node_usage_reports` 与 `native_sessions` 两列；不删不改旧数据。迁移前请先备份（见 `scripts/prepare-dev.py` 的备份步骤）。

### 生成 mTLS 证书（示例，私有 CA）

```bash
# 控制面服务端证书（SAN 必须包含 Agent 访问的主机名或 IP）与 Agent 客户端证书，由同一私有 CA 签发。
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -x509 -new -key ca.key -sha256 -days 825 -subj "/CN=asterlink-agent-ca" -out ca.pem
# 控制面
openssl ecparam -name prime256v1 -genkey -noout -out control.key
openssl req -new -key control.key -subj "/CN=demo.hyshentou.cn" -addext "subjectAltName=DNS:demo.hyshentou.cn" -out control.csr
openssl x509 -req -in control.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 365 -sha256 \
  -extfile <(printf "subjectAltName=DNS:demo.hyshentou.cn\nextendedKeyUsage=serverAuth") -out control.pem
# 每个节点一张（在节点上生成私钥，只把 CSR 带出来）
openssl ecparam -name prime256v1 -genkey -noout -out agent.key
openssl req -new -key agent.key -subj "/CN=hk-node-1" -out agent.csr
openssl x509 -req -in agent.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 365 -sha256 \
  -extfile <(printf "extendedKeyUsage=clientAuth") -out agent.pem
```

CA 私钥只保存在你的离线环境；`ca.pem` 同时用作后端的 `NODE_AGENT_CLIENT_CA_FILE` 和 Agent 的 `NODEAGENT_SERVER_CA_FILE`。**不要把这些文件或 `.env` 提交到 Git。**

## 4. 在节点上部署 Agent

```bash
cd native/nodeagent
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=1.0.0" -o asterlink-nodeagent ./cmd/asterlink-nodeagent
cp .env.example /etc/asterlink/nodeagent.env && chmod 600 /etc/asterlink/nodeagent.env   # 按注释填写
./asterlink-nodeagent -env /etc/asterlink/nodeagent.env -print-fingerprint               # 得到证书指纹
./asterlink-nodeagent -env /etc/asterlink/nodeagent.env                                   # 由 systemd 托管
```

然后在**管理员页面（/admin/nodes）→"节点代理"面板**登记：选节点、名称、粘贴指纹。Agent 上线后面板显示"在线"和它已验证的协议。

管理员导入节点 YAML 的方式不变：`server/port/tls/servername` 填节点转发器**对外实际监听**的值；`uuid`（vless）只是占位符，客户端永远拿到的是每 grant 的独立 UUID。转发器是 Agent 内置的参考 VLESS（TCP + UDP，可选 TLS）：节点上的 YAML 端口必须就是 `NODEAGENT_VLESS_LISTEN`，且生产必须配 TLS 证书（明文需要显式 `NODEAGENT_ALLOW_PLAINTEXT=true`，仅测试用）。

### 参考转发器的边界（请如实评估）

* 实现：VLESS v0，TCP 与 UDP（命令 2，长度前缀数据报）。**不含** Reality、XTLS flow、Mux.Cool、SS、Trojan。需要这些的节点必须换用经同样验证（用户归属、可信计数、存量踢线）的转发器，而不是把它们标成可用。
* 计数口径：只数转发的应用层负载（客户端→目标为上行，目标→客户端为下行），不含 VLESS/TLS 封装。因此与客户端 Mihomo 的计数略有差异，**以节点为准**。
* 目的地址过滤：默认拒绝回环/私网/链路本地/组播/CGNAT 目标（在解析后的连接阶段检查，防 DNS 重绑定），避免节点被用作进入其内网的跳板。
* 超限边界：字节预算在每次写入前原子预留，**不会超过预算转发**；DB 里的 `ChargedBytes` 可能略大于 `BudgetBytes` 仅当上报晚于转发时的并发写入，属于已计入账单的真实字节。
* UDP 已做协议级测试（自写客户端），**尚未与 Mihomo 的 VLESS UDP 路径对接验证**。

## 5. 时间参数（均可在 `.env` 调整）

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| grant 租约 | 90 s（`CLIENT_LEASE_SECONDS`） | 节点的绝对停止时间：`min(now+租约, 会话到期, 套餐到期, 最后保活+空闲超时)`；每次 sync 续期 |
| Agent sync | 2 s（`NODEAGENT_SYNC_SECONDS`） | 撤销在线时的下发延迟 ≈ 一个 sync 周期 |
| Agent 用量上报 | 5 s（`NODEAGENT_USAGE_SECONDS`） | 仅在计数变化时上报；检查点每 1 秒落盘 |
| 对账宽限 | 60 s（`NODE_AGENT_SETTLE_GRACE_SECONDS`） | 节点失联时，到期 + 宽限后才归还预算，并标记"待对账" |

## 6. 验收证据（本机回环，macOS 26 / arm64，PostgreSQL 隔离实例；真实 Agent 二进制 + 真实 mTLS + 真实 Core 二进制）

运行 `python3 scripts/test-native.py`（它会构建 Core 与 Agent 二进制并设置环境变量）。节点端到端的实测数字：

| 用例 | 结果 |
| --- | --- |
| B04 零客户端上报仍计费 | 发送 200 KiB 往返，**约 1.0 s 内**库中 `used_units=204,800,000`（409,600 字节 × 0.5），与手算精确一致；`ClientTrafficReport` 行数 = 0 |
| B05 撤销 | `POST /grants/:id/revoke` 后 **约 1.0 s** 内存量 TCP 全部被节点关闭；同凭据新连接被拒；尾部字节通过 `final` 报告入账，预算归还 |
| B05 共享凭据 | 管理员 YAML 里的共享 UUID 在节点上被拒（未知用户不返回任何字节） |
| B07 控制面消失 | 停掉 API 后，节点在租约（测试用 4 s）**约 3.9 s** 时自行关闭存量连接并拒绝新连接；恢复后同一凭据继续，累计计数不回零 |
| B07 Agent `kill -9` | 重启后以**旧代次**的检查点发送 `final` 报告，已落盘的字节精确入账（131,072 字节），不标记待对账；新代次独立计数 |
| B06 预算竞争 | 8 个设备并发争抢 5 MiB 额度（块大小 2 MiB）：恰好 3 个 grant 成功，预留总和 = 5 MiB，`已用 + 预留 ≤ 额度` |
| B03 周期归属 | 套餐续期换周期后，旧周期的迟到用量只记入旧周期，新周期余额不受影响 |
| 全链路 | 真实 Core：登录 → 选线路 → 连接（grant 被节点确认，约 1.3 s）→ 节点计费后 Core 显示的服务器余额精确等于 `额度 − 实测×倍率` → 订阅到期后 Core 与节点**同时约 1.4 s 内**停止 |

后端单元/集成：`go test -race ./...` 全绿（含契约夹具与活跃响应的字段集逐项比对）；`native/core`、`native/nodeagent`、Swift（39 项）全绿。

## 7. 未验证 / BLOCKED（不得写成已完成）

1. **真实商业节点**：你当前的节点是供应商提供的共享凭据，没有服务器控制权，**不能**成为权威节点。要真正收费，需要在一台你能管理的服务器上部署 Agent 并让 `YAML` 指向它。
2. **真机 TUN + 真实 Agent 的整条链**：本阶段在回环上用测试进程扮演"TUN 发出的流量"（用同一份凭据直连转发器）；Core 的 TUN 阶段-3 验收已单独通过，但未用 Mihomo 客户端经 TUN 连接本阶段的参考转发器。需要在你断开公司 VPN 后，用真实节点按 `docs/macos-stage3.md` 的流程联调。
3. **SS / Trojan / Reality / XTLS / Mux**：参考转发器未实现，节点代理不会声明，服务端因此不会对这些节点发 grant。
4. **VLESS UDP 与 Mihomo 对接**、**QUIC/WebRTC**：未验证。
5. **多实例 API**：sync/usage 逻辑依赖行锁与唯一约束，设计上可多实例；`/nodes` 长轮询的限流仍按 IP（阶段 2 遗留）。多实例与压测未做。
6. **每用户一次 usage 请求**：N 个 grant 每个周期 N 次请求，未做批量接口；规模大时应加批量端点（向后兼容地新增）。
7. **支付、MFA、备份演练、签名公证**：不属于本阶段，见 `handoff_app.md` 第 10 节。

## 8. 回滚

* 回退客户端：旧客户端在生产环境会收到 `426 client_upgrade_required`（不会静默降级到共享凭据）。
* 回退服务端：保持新表不动即可（只增不删）；把 `NODE_AGENT_ADDR` 置空会停用 Agent 接口，生产随即不再下发任何代理。开发环境仍可用 `client_reported`。
* 不要用 `APP_ENV=development` 冒充来恢复生产代理交付。

## 9. 在线状态与名额释放（节点组第 6 轮）

规划 `handoff_node_use_history_and_load_balancing_c.md` 第 6 轮。Core 只在连接时取得在线租约，网站“连接与客户端”、同时在线上限（默认 2 台）与服务器负载都按租约统计。macOS 的 Core 是 Helper 管理的 `asterlink-core` 工作进程：

| 事件 | 路径 | 名额 |
| --- | --- | --- |
| 点击断开 | `disconnect` → `stopTunnel` | 立即释放 |
| 退出 App（⌘Q / 菜单“退出”） | `SessionController.shutdown` → Helper `stopWorker` → IPC `shutdown` → `Service.Close` → `Shutdown` | 立即释放 |
| Helper 宽限后 `terminate()`、注销、系统关机（SIGTERM） | 第 6 轮修复：Core 的信号处理先恢复 DNS，再在 4 秒内执行 `Shutdown`（原先直接退出，网站要等 90 秒租约到期才显示离线） | 立即释放 |
| 合盖睡眠 | 网络与进程挂起，无法上报 | 约 90 秒后网站显示离线；唤醒后桌面端按第 5 轮约定提示“在线状态已过期，请重新连接”，不自动重连 |
| 工作进程被强杀、断电 | 无代码可运行 | 90 秒后过期 |
| 管理员停用所在服务器 | Core 停止 → 结账 → 自动改连同线路另一台；期间快照保持 `preparing`，界面显示 Core 文案“线路服务器已调整，正在为你重新连接” | 换到新服务器 |

证据：`TestCoreExitPathsReleaseTheSlot`（真实 `asterlink-core` 二进制对真实 API）分别走 IPC `shutdown`、命令管道关闭与 SIGTERM，均在约 10 ms 内以 `released` 结束租约（SIGTERM 用例在旧 `main.go` 上失败）；`Stage2Tests` 的 `testDeviceLimitAtConnectIsShownInTheCoresWordsAndStaysSignedIn`、`testServerSideMoveIsFollowedWithoutACommand`。

本机实测（需要安装本轮构建并批准 Helper、断开其他 VPN，会改变系统路由，须本人在场）：

1. 网站 `/dashboard` 登录同一账号，App 连接任一线路，确认“在线设备 1 / 2”。
2. 点击断开，记录网站变为 0 的秒数（预期一次刷新内，默认 15 秒）。
3. 再次连接后 ⌘Q 退出 App，记录秒数（预期同上）。
4. 再次连接后合盖 3 分钟，期间在另一设备看网站：约 90 秒后显示离线；开盖后 App 显示需要重新连接。
5. 两台其他设备在线时连接，App 应显示“已达到同时在线设备上限，请先断开其他设备”。
