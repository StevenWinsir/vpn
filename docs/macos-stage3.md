# macOS 阶段 3：完整 TUN、真实开发计量与恢复（验收说明）

> 范围：`metering_source` 仍是 `client_reported`（开发账本），不解除后端 production 保护。
> 后端 API 与数据库在本阶段**没有任何变化**，已部署的 demo 服务无需更新。

## 1. 先读：公司 VPN 会影响真机测试

Core 在连接前做 Preflight：发现任何带 IPv4 地址的 `utun` 设备，或去往公网的路由经过 `utun`，就拒绝连接并返回
`vpn_conflict`（界面提示“检测到其他 VPN 正在运行，请先断开后再连接”）。这是有意设计（首版不叠加其他 VPN，
避免两个隧道互相套娃把公司流量送到测试节点）。因此：

- **做真机 TUN 测试前必须先断开公司 VPN**，测试完再连回去。
- 不做真机 TUN 的测试（`--suite unit`、`--suite bundle`、`python3 scripts/test-native.py`）完全不受公司 VPN 影响。
- 公司 VPN 开着时 App 仍可登录、同步节点、显示套餐；只有“连接”会被 `vpn_conflict` 拦下。

## 2. 无需 root 的验证（CI 与本机都能跑）

| 命令 | 内容 |
| --- | --- |
| `python3 scripts/test-native-macos.py --suite unit` | Go（含 `-race` 之外的全部）+ Swift 38 项 + 环境契约 |
| `cd native/core && go test -race ./...` | 计量器 / 状态机 / 引擎模板（用固定版 Mihomo 解析） |
| `python3 scripts/test-native.py` | 隔离 PostgreSQL + **真实 Core 二进制（仅此测试编译 `-tags fakeengine`）** + 真实 API：<br>1×/0.5×精确整数扣量、中途换线路、额度耗尽 / 到期 / 吊销自动停连、隧道验证失败不显示已连接 |
| `python3 scripts/test-native-macos.py --suite bundle` | 构建带 Mihomo 的 Debug 包，Helper→Core 握手与篡改拒绝 |

`fakeengine` 构建标签只存在于测试脚本里；Release 与 `build-native-macos.py` 产物不含它。

## 3. 真机 TUN 验收（需要 root，改路由/DNS，明确授权才执行）

前置：断开公司 VPN；准备**一个受控测试节点**（共享供应商节点只能用于开发，见 handoff 阶段 4）。
节点参数只经环境变量传入，不进命令行、不进仓库：

```bash
# SS 节点
export ASTERLINK_POC_SS_SERVER=<host> ASTERLINK_POC_SS_PORT=<port> ASTERLINK_POC_SS_PASSWORD=<password>
# 或任意协议（如 VLESS Reality）：一个完整的单节点 JSON 对象
export ASTERLINK_TUN_PROXY_JSON='{"name":"t","type":"vless","server":"…","port":443,"uuid":"…","tls":true,"servername":"…","network":"tcp","udp":true}'

# 可选的受控目标（不给则对应项报 BLOCKED，不会假装通过）
export ASTERLINK_TUN_DOWNLOAD_URL=https://…/100MiB.bin  ASTERLINK_TUN_DOWNLOAD_BYTES=104857600   # 计数对账
export ASTERLINK_TUN_UDP_ECHO=<host:port>                                                         # UDP
export ASTERLINK_TUN_API_HOST=demo.hyshentou.cn:443                                              # 控制面
export ASTERLINK_POC_EXPECT_EXIT=<节点出口 IP>

sudo -E python3 scripts/test-native-macos.py --suite tun --allow-network-changes
```

它运行 `native/core/cmd/asterlink-tuntest`，驱动的就是正式的 `internal/engine`，逐项给出 pass / fail / blocked：

| 编号 | 检查 |
| --- | --- |
| T01 / T01a / T02 | utun 与路由出现；不设代理的请求出口改变；经节点真实转发（不是仅解析 YAML） |
| B-COUNT | 受控下载的正文字节 vs. Mihomo 代理计数（记录倍数与协议开销） |
| T03 | 系统解析返回 fake-ip；对任意解析器 :53 的查询被劫持；UDP 回显 |
| T04 | IPv6：要么经隧道，要么被阻断，不能直连（出口未变 = fail） |
| T05 | 隧道期间 API 仍经物理网卡可达；空闲 6 秒计数不增长（无自循环） |
| T07 | 停止后没有遗留 utun / 路由 / DNS 变化 |
| T-CYCLES | 连续 20 次连接/断开，goroutine、文件描述符、utun 不累积 |

T05-loop 对没有活动流的主机使用 64 KiB/6 秒严格预算；macOS 系统范围 TUN 若有其他应用保持流量，则使用 256 KiB/6 秒的有限背景流量预算，并在报告中记录活动流数量。超过预算仍失败，不能用“有背景流量”掩盖持续自循环。

异常中断后的手动恢复见 `docs/macos-tun-poc.md`（`sudo pkill -f asterlink-tuntest`，进程退出即拆除 utun 与路由）。

## 4. 用 App 端到端试：管理员改节点 → 客户端登录 → 连接 → 看扣量

1. 网站管理员后台（`test.hyshentou.cn`）编辑/新增节点 YAML，设置线路类型与倍率（专线 1000‰、普通 500‰ 等）。
2. 断开公司 VPN。构建并启动 Debug 包：

   ```bash
   python3 scripts/build-native-macos.py --configuration Debug
   open artifacts/native-macos/<run-id>/AsterLink.app      # 路径以脚本输出为准
   ```

3. 设置 → 特权服务 → “启用”，在 系统设置 → 通用 → 登录项与扩展 里批准 Helper（需要你的操作，也需要 Helper 的 ad-hoc 签名被系统接受；
   若被拒，需用 Developer ID 重签，见 handoff 阶段 1 遗留项 2）。
4. 登录 `test@test.com` 之类的**有有效套餐**的账号（管理员身份本身不等于有代理权益）。选节点 → 连接。
5. 界面显示：实时上/下行速率、短时曲线、本次经线路传输的真实字节、**预估剩余**与**服务器已确认剩余**（分开显示）、线路倍率。
6. 在网站后台/数据库核对：`subscriptions.used_units` 增量 = (上行+下行字节) × 倍率‰（整数，1 单位 = 1/1000 字节）；
   `client_traffic_reports` 每分钟一条，`source = client_reported`。

> Helper 注册被系统拒绝时的**仅限 Debug、仅限本机开发**后备：以 root 直接运行 Debug 版 App 并指定 Core，
> `sudo -E ASTERLINK_DEV_API=<…/api/v1/client> ASTERLINK_DEV_SITE=<…> ASTERLINK_DEV_CORE=<core 路径> <App>/Contents/MacOS/AsterLink`。
> 该分支用 `#if DEBUG` 编译，Release 不包含。

## 5. 连接生命周期与自动停连（实现要点）

连接顺序固定：授权闸门 → Preflight（其他 VPN / 物理出口）→ 唯一计量器 `Begin` → **首个计量 ACK（零字节批次合法）**
→ 绑定物理出口并开 TUN → 经隧道的真实探测 → 才显示“已连接”。任何一步失败都会关闭数据通路。

已连接时每秒的看门狗：

- 睡眠 / 时钟回拨（墙钟与单调钟背离 > 5 秒）→ 停连，重新验证前不复用旧授权（`lifecycle_reconfirmation_required`）。
- 物理网络变化（默认接口或其地址变）→ 停连（`network_changed`）。
- 租约过期 / 套餐到期 / 账号禁用 / 会话吊销 / 线路或倍率被管理员修改 / 本地估算额度耗尽 → 停连并结账。
- 计数回退或超限 → 停连并强制重新登录（`invalid_core_counters`），不偷偷加偏移。

结账：先关数据通路，再把最后一批累计原样上报直到服务器确认；未确认的批次保持 pending，下次连接前必须先确认
（`traffic_unconfirmed`），空闲保活会自动重放。切换线路、登出、退出 App 同样先结清再进行。

观察到的延迟（隔离环境，单次）：额度耗尽后约 1.0 秒停连；订阅到期 / 管理员改动约 0.4–1.0 秒反映到客户端。
