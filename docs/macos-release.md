# macOS 客户端：回归、签名安装与发布交接（阶段 5）

> 状态：**代码与自动化审计已完成；Developer ID 签名、公证、Gatekeeper 验收因没有 Apple 开发者账号而 BLOCKED。**
> 当前能产出的是 *internal-adhoc*（临时签名）安装包，仅供你自己的 Mac 做功能测试，**不可分发给用户**。
> 本文所有"通过"都指 `python3 scripts/test-native-macos.py --suite release` 的实际输出；没跑过的一律写 BLOCKED。

## 1. 两种安装包

| 项 | internal-adhoc（现在可用） | developer-id（有账号后） |
| --- | --- | --- |
| 构建 | `build-native-macos.py --configuration Release --internal` | `build-native-macos.py --configuration Release`（需 `CODESIGN_IDENTITY`、`ASTERLINK_TEAM_ID`） |
| 编译 | Release 优化，**不含** DEBUG 测试后门（自检根目录、`ASTERLINK_DEV_CORE/API`） | 同左 |
| 签名 | 临时签名（`-`），Hardened Runtime 开启，无时间戳 | Developer ID Application，Hardened Runtime + 安全时间戳 |
| 通道 | `APP_ENV=development`（XPC 只校验标识符，不校验 Team ID） | `APP_ENV=release`（XPC 校验 Apple 锚点 + 标识符 + Team ID） |
| Gatekeeper | **按设计被拒绝**（未公证） | 公证并 staple 后通过 |
| DMG 文件名 | `AsterLink-<版本>-INTERNAL-adhoc-arm64.dmg` | `AsterLink-<版本>-arm64.dmg` |

为什么 release 通道不能用临时签名：release 通道的 XPC 签名要求写死了 `anchor apple generic` 和 Team ID，临时签名没有这两项。这是安全设计，**不要**为了方便把它放宽。

## 2. 构建、打包、审计（单入口）

```bash
python3 scripts/build-native-macos.py --configuration Release --internal --run-id my-run
python3 scripts/package-native-macos.py --build-run-id my-run
python3 scripts/test-native-macos.py --suite release --build-run-id my-run
# 或一条龙（自己构建+打包+审计）：
python3 scripts/test-native-macos.py --suite release
```

产物在 `artifacts/native-macos/<run-id>/`（不入库）：`AsterLink.app`、`build-manifest.json`、`release/`（DMG、`SHA256SUMS`、`release-manifest.json`、`THIRD_PARTY_LICENSES.txt`、`SOURCE.txt`、两个源码归档）。

`--suite release` 的用例（退出码：0 全过，1 有失败，3 有 BLOCKED）：

| 用例 | 检查内容 |
| --- | --- |
| R-SIGN | `codesign --verify --deep --strict`；三个 Mach-O 标识符精确；全部开启 Hardened Runtime |
| R-ENT | 无任何 entitlement（无 `get-task-allow`、无 JIT/未签名内存例外）；白名单为空，新增须人工评审 |
| R-NOHOOKS | 发布二进制中没有 `--self-test-root`、`ASTERLINK_DEV_*`、TUN 测试工具 |
| R-SECRET / R-SECRET-SELF | 包内无 `postgres://`、`DATABASE_URL`、完整 PEM 私钥，且不含本机 `.env` 中任何密钥类取值（只比对，不打印）；扫描器自带"植入样本必须被发现"的自检 |
| R-PLIST | API/网站为 HTTPS、无 ATS 例外、最低 macOS 15、Helper 内嵌版本 = App 版本 |
| R-MANIFEST | 构建清单、`worker-manifest.json`、DMG 哈希、`SHA256SUMS`、固定的 Mihomo 提交全部与实际字节一致 |
| R-LICENSE | Mihomo GPL 文本、`SOURCE.txt`、两个源码归档存在且可读；列出无许可证文件的模块 |
| R-DMG | `hdiutil verify`；只读挂载；`Applications` 链接；DMG 内 App 签名有效且与构建产物逐字节一致；含卸载脚本 |
| R02-UNINSTALL | 卸载脚本在沙箱中：预演不改动；`--apply` 只删 AsterLink 资源，不动其他 VPN |
| R01-GATEKEEPER / R01-STAPLE | 仅 Developer ID + 已公证包才可能 PASS；临时签名包报 **BLOCKED**（Gatekeeper 拒绝是预期结果） |

其他入口：`--suite unit`（Go/Swift/Python 单元，含 `U-LOGLINT` 日志脱敏静态检查）、`--suite bundle`（Debug 包 + Helper 篡改负测）、`--suite soak`（对运行中的 App 采样内存/CPU，见第 7 节）、`--suite tun`（需 root，见第 6 节）。

## 3. 在这台 Mac 上安装（临时签名）

1. 用上面的命令生成 DMG，或直接用本机构建的 `AsterLink.app`。**本机构建的文件没有"隔离"属性，不会被 Gatekeeper 拦截**，无需任何绕过。
2. 如果 DMG 是从别处下载/AirDrop 来的，首次打开会被拦：到 *系统设置 › 隐私与安全性* 点"仍要打开"。**不要**全局关闭 Gatekeeper/SIP，也**不要**用 `xattr -d com.apple.quarantine` 来冒充发布验收。
3. 把 `AsterLink.app` 拖到 `/应用程序`（SMAppService 守护进程要求 App 在固定位置）。
4. 打开 App，登录页的"首次使用"卡片上点"启用"：App 注册守护进程并直接打开 *系统设置 › 通用 › 登录项与扩展*；在"允许在后台"里打开 AsterLink 的开关，回到 App 即自动继续（每秒检测一次批准状态；有记住的密码会自动登录），无需再点"刷新"。
   - 这一步批准是 macOS 对**任何**新后台服务（`SMAppService.daemon`）的强制要求，Developer ID 签名、公证后依然存在，只能引导、不能跳过；只有 MDM 管理的设备可以预先批准。
   - 若 App 是直接在 DMG 里或被"App 转移"（`/AppTranslocation/`）运行，卡片会提示先拖到"应用程序"，因为 launchd 不能使用那里的守护进程。
5. 已实测：临时签名的 App 可以通过 `SMAppService.daemon` 注册并在批准后正常工作。
6. 钥匙串授权弹窗（"AsterLink 想要使用…com.asterlink.vpn.credentials"）：钥匙串条目记录创建它的 App 的分区 ID。临时签名的分区是 `cdhash:<哈希>`，**每装一个新构建，哈希就变**，系统把它当成另一个 App，于是每个新版本首次读取"记住的密码"和"设备 ID"时各问一次；点"始终允许"后，这个版本就不再问。Developer ID 签名的分区是 `teamid:<Team ID>`，跨版本升级不变，正式版不会出现这个弹窗。这是临时签名的特性，不要为绕过它改用明文保存或关闭钥匙串访问控制。

## 4. 有了 Apple 开发者账号之后（逐项做，每项留证据）

1. 加入 Apple Developer Program，创建 **Developer ID Application** 证书，导入钥匙串；记下 10 位 Team ID。
2. 创建 notarytool 凭据（密码类材料只进钥匙串，不进命令行历史、不进仓库）：
   `xcrun notarytool store-credentials asterlink-notary --key <AuthKey.p8> --key-id <ID> --issuer <ISSUER>`
3. 准备公开构建参数 `apps/macos/.env`：`APP_ENV=release`，API/网站必须 HTTPS（脚本会拒绝其他取值）。
4. 构建 → 打包 → 公证 → 审计：
   ```bash
   CODESIGN_IDENTITY="Developer ID Application: …" ASTERLINK_TEAM_ID=ABCDE12345 \
     python3 scripts/build-native-macos.py --configuration Release --app-version 1.0.0 --run-id rel
   CODESIGN_IDENTITY="Developer ID Application: …" ASTERLINK_NOTARY_PROFILE=asterlink-notary \
     python3 scripts/package-native-macos.py --build-run-id rel --notarize
   python3 scripts/test-native-macos.py --suite release --build-run-id rel
   ```
   此时 R01-GATEKEEPER / R01-STAPLE 必须是 PASS；仍是 BLOCKED 就不是发布版。
5. 在**干净用户/另一台 Mac** 上从下载的 DMG 安装并完成首次 Helper 授权（矩阵 R01）。
6. `.github/workflows/release-macos.yml` 是配好的手动发布流水线，**从未运行过**（见文件头注释）。第一次跑通前，以本节手动步骤为准。
7. release 通道的 Helper 授权、XPC 调用方校验（Team ID 级）、伪造调用方被拒——这些只有 Developer ID 包才能真正验收（S01 的剩余部分）。

## 5. 升级、卸载、回滚

**升级**：先在 App 中断开 → 用新版本替换 `/应用程序/AsterLink.app` → 打开。App 连接前会先向 Helper 握手并比较版本：不一致就**拒绝启动内核**，提示"重新安装特权服务"（设置页按钮，等价于 注销 + 重新注册）。连接中、正在断开时这两个按钮禁用（`HelperUpgradePolicy`，有单元测试）。

**卸载**：
- 推荐：设置 → "移除特权服务" → 把 App 拖进废纸篓。
- 或运行 DMG 内的 `卸载 AsterLink.command`（源码：`scripts/uninstall-asterlink.sh`）。默认**预演**，加 `--apply` 才执行；只处理 AsterLink 自己的 App、偏好、缓存、`/Library/Application Support/AsterLink`、两条钥匙串条目（记住的密码、设备 ID）和 `system/com.asterlink.vpn.helper` 这一个 launchd 标签；系统级项需要 sudo。**不动其他 VPN、路由、DNS。**

**回滚客户端**：重新安装旧版 DMG（先移除特权服务）。注意服务端 `CLIENT_MIN_APP_VERSION` 会让过旧客户端收到 `426 client_upgrade_required`，回滚到低于该值的版本需先下调该配置。回滚永远不会恢复"共享配置"模式。

**回滚服务端**：见 `docs/macos-stage4.md` 第 8 节。把 `NODE_AGENT_ADDR` 置空会停用节点代理接口，生产随即不再下发任何代理；**不要**用 `APP_ENV=development` 冒充来恢复交付。

## 6. 你正在连公司 VPN：对测试的影响

我在这台 Mac 上实测到路由表里有 `0/1 → utun420`（分段默认路由），说明有一个全局型 VPN 正在运行（不在系统"网络"服务列表里，应是第三方客户端）。影响如下：

| 测试 | 有影响吗 | 说明 |
| --- | --- | --- |
| 单元 / bundle / release 套件、构建、打包、DMG 审计 | **无** | 不改路由、不开 TUN |
| 登录/节点列表/管理员后台 | 基本无 | 走 HTTPS 访问 `demo.hyshentou.cn`，经公司 VPN 出去（我实测可达，HTTP 200） |
| **点"连接"、`--suite tun`、真实流量扣费验证** | **有，会被拒绝** | Core 检测到另一个 VPN 会返回 `vpn_conflict` 拒绝连接（设计如此，首版不支持叠加）。要测请先断开公司 VPN |
| 断开公司 VPN 的副作用 | 有 | 测试期间无法访问公司内网；也要注意公司可能的合规要求 |

建议：先做不需要连接的验收（登录、节点同步、管理员改节点），再临时断开公司 VPN 做连接/扣费测试，结束后恢复。TUN 套件命令（需 root、显式授权）：
`sudo -E python3 scripts/test-native-macos.py --suite tun --allow-network-changes`（环境变量见 `docs/macos-stage3.md`）。

## 7. 稳定性与资源基线（实测而非猜测）

- 20 次同进程启停：阶段 3 真机通过（`artifacts/native-macos/tun-20261012-routefix-report-tun/`）。
- 长时运行：先在 App 里连接，再 `python3 scripts/test-native-macos.py --suite soak --soak-minutes 60`。它只读 `ps`，输出 App/Helper/Core 的内存、CPU 统计和内存增长率；增长超过 `--max-rss-growth-pct`（默认 20，**这是任意取的失败线，不是业界标准**，请按首次基线调整）判失败。未连接则 BLOCKED。
- **尚未执行（需要真人在真机上）**：≥10 次睡眠/唤醒、Wi-Fi/有线切换、Core 硬杀后系统残留、多实例/快速用户切换（Helper 已有"第二个 Worker 被拒"的自检）。原生崩溃率没有数据，不填数字。

## 8. 许可证与对应源码（Mihomo 是 GPL-3.0）

- App 包内 `Resources/THIRD_PARTY_LICENSES.txt`（由 `go list -deps` 生成，Mihomo 文本置顶）和 `SOURCE.txt`（仓库、提交、Mihomo 提交、重建命令）。
- 发布目录随 DMG 提供 `asterlink-source-<提交>.tar.gz` 与 `mihomo-source-<提交>.tar.gz`（`git archive` 的已提交内容）。**发布时两者必须一起提供**。
- 当前 100 个链接模块中有 2 个在模块目录里找不到许可证文件：`github.com/metacubex/chacha`、`github.com/rasky/go-lzo`。**未核对其上游许可证**，正式收费发布前必须人工确认（清单在 `build-manifest.json` 的 `licenses_without_file`）。
- 把闭源/收费 App 与 GPL 内核放在同一安装包里是否合规，**取决于进程拆分之外的法律判断**，本文不给结论，发布前请让法务/律师确认。

## 9. 运维：密钥轮换、告警、备份

**密钥轮换（上线前必做）**
- 聊天里出现过的数据库密码、共享供应商节点凭据：全部视为已泄露，轮换。数据库：改 `DATABASE_PASSWORD`（`backend/.env`）后重启后端；生产使用 `DATABASE_SSLMODE=verify-full` + `DATABASE_SSLROOTCERT`。
- `JWT_SECRET`（轮换会使网页会话失效）、`NODE_ENCRYPTION_KEY`（**不能直接换**：已加密的节点凭据需先用旧钥解密、新钥重加密——当前没有现成的轮换工具，换钥前必须先写迁移并备份）。
- NodeAgent mTLS：Agent 证书指纹在管理员页"节点代理"面板登记；换证书 = 在节点生成新证书 → 面板更新指纹 → 重启 Agent。
- App 本身不含任何可轮换的密钥（API 地址在构建时写入、公开）。

**节点计量告警**：现状只有管理员页"节点代理"面板的被动展示（在线/离线、在用 grant、待对账数）。**没有主动告警**（邮件/IM/监控）——这是未实现项，上线前需要接入你们的监控（建议对"待对账 > 0 持续 N 分钟"、"Agent 离线"、"sync 失败率"告警）。

**备份与回滚演练**：迁移或发版前 `pg_dump -Fc` 备份，并在**隔离库**演练恢复；`python3 scripts/test-native.py` 使用隔离的临时 PostgreSQL，不会碰你的远程测试库。**恢复演练本阶段未在你的环境执行。**

## 10. 已知限制与外部阻塞（不得写成"商业可上线"）

阻塞（需要你）：
1. Apple Developer ID 与公证 → R01/R02 的签名、Gatekeeper、staple、干净机安装、Team ID 级 XPC 校验。
2. 自有可管理节点（部署 NodeAgent）→ 阶段 4 的真机链路与存量连接强制断开；共享供应商节点只能作开发节点。
3. 网站商业条件：真实支付回调验签/对账/退款、邮箱验证与找回密码、管理员 MFA、HTTPS/数据库 TLS/限流/备份告警、用户协议与隐私政策。
4. 许可证人工确认（第 8 节）。

产品限制：无系统级 Kill Switch；无自动选路；无无缝切换节点；一次只连一个节点；管理员改任一线路会使所有会话触发 `profile_changed`；SS 暂无权威节点适配（仅参考 VLESS）；VLESS UDP 未与 Mihomo 对接验证。

## 11. 测试矩阵状态（阶段 5 结束时）

| ID | 状态 | 证据 |
| --- | --- | --- |
| R03 回归 | 本机通过 | `--suite unit`、`scripts/test-native.py`（隔离 PostgreSQL + 真实 Core/Agent）、`swift test`；CI 同步执行 |
| S02 日志/产物无秘密 | 部分通过 | `U-LOGLINT`、`R-SECRET`；崩溃报告未检查（尚无崩溃样本） |
| S01 XPC/Worker | 部分通过 | bundle 套件 6 项负测；真实 SMAppService 注册与 Team ID 级校验 BLOCKED |
| R01 干净机安装 | **BLOCKED** | 需 Developer ID + 公证 |
| R02 升级/卸载 | 部分通过 | 版本不一致拦截单测、卸载脚本沙箱测试；真实"重新安装特权服务"未手动验证 |
| T06/T07 睡眠、硬杀 | **未执行** | 需真机人工 |
