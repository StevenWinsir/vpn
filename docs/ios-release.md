# iOS 打包、签名与发布（第 6 轮）

本轮把“不依赖付费开发者账号”的发布链路全部做完：**构建出未签名 IPA**，附带许可材料、对应源码、依赖清单和哈希；写好**拿到证书后一条命令重新签名**的工具；写好**发布审计**；网站 `/download` 页增加 iPhone 入口。签名本身、真机安装和商店流程都需要付费账号，下文第 4 节给出买到账号后的完整步骤。

## 1. 产物

```bash
python3 scripts/package-native-ios.py --channel internal
```

> 2026-10-28（handoff_app_standardization.md 第 5 轮，D15）：公开目录只放安装包与只列安装包的 `SHA256SUMS`；许可、源码说明、源码归档、组件清单与发布信息内容不变，移入同级的 `release-private/`（内部资料，不得发布）。发布构建的 Core 不再写入内核提交号。

输出到 `artifacts/native-ios/release/`（公开目录，不提交，CI 工件 `ios-unsigned-ipa`）：

| 文件 | 说明 |
| --- | --- |
| `AsterLink-<版本>-ios-unsigned.ipa` | 真机用 arm64、Release、已剥离符号、**未签名**；内含 App 与 Packet Tunnel 扩展；Core 不含内核提交号 |
| `SHA256SUMS` | 只列 IPA |

`artifacts/native-ios/release-private/`（**内部资料，不要公开**，CI 工件 `ios-release-private-INTERNAL`，不含源码归档）：

| 文件 | 说明 |
| --- | --- |
| `THIRD_PARTY_LICENSES.txt` | 链进 Core 的 Go 模块许可 |
| `dependencies.cdx.json` | CycloneDX 风格的依赖清单 |
| `SOURCE.txt` + 两个 `.tar.gz` | 本仓库与内核的 `git archive`，附重建说明 |
| `release-info.json` | 版本、构建号、包 ID、IPA 哈希、Core 身份（含内核提交号与 `revision_stamped:false`）、`signed:false`、`trusted_release:false`、未关闭的门槛 |
| `dSYMs-<版本>.zip` | 崩溃符号化 |
| `SHA256SUMS`、`README-内部资料.txt` | 内部目录自身的哈希与“不得发布”说明 |

* 通道永远是 `internal`；`--channel release` 会被拒绝，直到 `release-info.json` 里的门槛全部关闭。
* 用 `apps/ios/.env.release.example`（`APP_ENV=release`，只允许 HTTPS）。想换后端地址：复制一份改好，用 `--env-file` 指定。
* 包 ID 前缀现在是占位的 `com.asterlink.dev`。不必重新构建：重签时用 `--bundle-prefix` 换成你注册的前缀。
* 版本与构建号：`--version 0.2.0 --build-number 3`（商店上传要求构建号递增）。

## 2. 审计

```bash
python3 scripts/test-native-ios.py --suite release
```

对**未签名包**逐项检查，一项失败即非零退出：

| 项 | 检查内容 |
| --- | --- |
| R03.files | 公开目录只有 IPA 与 `SHA256SUMS`；`release-private/` 齐全；两个 `SHA256SUMS` 能验过、IPA 与 `release-info.json` 一致、源码包可读、声明为未签名/internal/未受信 |
| R03.structure | `Payload` 在前、扩展 ID 以 App ID 开头、最低 iOS 17.0、平台 iphoneos、仅 arm64、扩展是 packet-tunnel |
| R03.config | 两个 Info.plist：`release`、只有公网 HTTPS、**无 ATS 例外**、无后台模式、声明了出口合规键 |
| R03.entitlements | 两个 target 的 entitlements 恰好是 `packet-tunnel-provider`，二进制里没有 `get-task-allow` |
| R03.no_debug_code | 二进制里没有 UI 夹具、调试页、外观覆盖、计费线束等调试符号 |
| R03.privacy | 两个隐私清单存在、不追踪，并且二进制实际导入的“必须说明原因”的 API（文件时间戳、系统启动时间、UserDefaults、磁盘空间）全部已声明 |
| R03.secrets | 扫描整个 IPA 的数据库地址、私钥、云密钥、令牌、口令赋值；并自检（植入后必须能扫出来） |
| R03.core_identity | 两个二进制都**不含**内核提交号（发布绑定），`release-info.json` 记录了提交号与补丁 |
| R03.scrub / R03.scrub_self | 展开 IPA：无许可/源码/发布信息文件；文本与 plist 无内核标记；二进制命中不超出 `scripts/release-scrub-baseline.json`（D16）；无构建机路径；扫描器能发现植入样本 |
| R03.licenses | Mihomo GPL 文本、所有模块有许可文件、源码包与 `SOURCE.txt` 提交一致 |
| R03.profile_checks | 描述文件校验器：对的通过；包 ID 不符、没有 Network Extension、已过期的被拒；通配符可用 |
| R03.resign | 用伪造的描述文件 + 临时 ad hoc 身份走完整个重签：换包 ID 前缀、描述文件里的 entitlements、先签扩展、`codesign --verify --deep --strict`、重新打包；没有描述文件的真实身份被拒 |
| R01、R02、R04、R03.signed | **BLOCKED**：需要付费团队、已签名的包和 iPhone |

`R03.resign` 用的是 ad hoc 签名和伪造的描述文件，只证明工具链和校验逻辑正确，**不证明苹果会接受**。苹果是否接受要在你拿到真实证书与描述文件后，由第一次真机安装（R01）来证明。

## 3. 隐私清单与出口合规

* `AsterLink/PrivacyInfo.xcprivacy`、`AsterLinkTunnel/PrivacyInfo.xcprivacy`：不追踪；收集邮箱与用量数据（关联账号，仅用于 App 功能）；使用的必须说明原因的 API 来自对二进制的符号扫描，而不是凭感觉：文件时间戳（C617.1）、系统启动时间（35F9.1），App 另有 UserDefaults（CA92.1）。以后加了新 API，审计会因“导入了但没声明”而失败。
* `ITSAppUsesNonExemptEncryption = true`：VPN 使用的加密不属于豁免范围，如实声明。上架或 TestFlight 时 App Store Connect 会问出口合规问卷，需要你按所在地法规填写，必要时准备自查报告（见交接书第 14 节）。这一项属于**法务/合规门槛**，不是代码能关闭的。

## 4. 买到 Apple 开发者账号之后

**先选分发方式**（决定账号类型，以 Apple 届时条款为准）：

| 方式 | 适合 | 限制 |
| --- | --- | --- |
| **Ad Hoc + 网站链接安装**（你想要的） | 自己和少量测试者 | 每台设备要先登记 UDID，每年数量有限（个人/组织账号目前都是约 100 台）；描述文件一年到期要重签 |
| TestFlight | 更多测试者（最多约 1 万人） | 需要上传到 App Store Connect 并通过 Beta 审核；VPN 类 App 要求**组织账号** |
| App Store | 公开发布 | 组织账号、审核 5.4（VPN）、3.1.1（数字服务购买）、中国大陆区不允许未经许可的 VPN；GPL 与商店条款的兼容性需法务确认 |
| Enterprise 企业账号 | 公司内部员工 | 仅限本公司员工；不能面向公众 |

**用 Ad Hoc 签名并上线下载页的步骤：**

1. 加入 Apple Developer Program，在 Xcode → Settings → Accounts 登录，让 Xcode 创建“Apple Distribution”证书（在钥匙串里）。
2. 登记要测试的 iPhone：设置 → 通用 → 关于本机 → 可复制 UDID（或用 Xcode 的 Devices 窗口），在开发者网站 Certificates, Identifiers & Profiles → Devices 添加。
3. 在 Identifiers 里创建两个 App ID（前缀换成你自己的，例如 `cn.hyshentou`）：
   * `cn.hyshentou.asterlink`
   * `cn.hyshentou.asterlink.tunnel`
   两个都勾选 **Network Extensions**（Packet Tunnel 是其中之一）。
4. 在 Profiles 里为这两个 App ID 各建一个 **Ad Hoc** 描述文件，选刚才的证书和设备，下载两个 `.mobileprovision`。
5. 重签：

   ```bash
   python3 scripts/ios/resign-ipa.py artifacts/native-ios/release/AsterLink-0.1.0-ios-unsigned.ipa \
     --identity "Apple Distribution: 你的名字 (TEAMID1234)" \
     --app-profile ~/Downloads/AsterLink_AdHoc.mobileprovision \
     --tunnel-profile ~/Downloads/AsterLinkTunnel_AdHoc.mobileprovision \
     --bundle-prefix cn.hyshentou \
     --ipa-url https://test.hyshentou.cn/downloads/AsterLink-0.1.0-ios.ipa
   ```

   工具会先检查：描述文件是否对应该包 ID、是否带 Network Extension 能力、是否过期、两个是否同一团队；然后先签扩展再签 App，用 `codesign` 验证，输出 `*-signed.ipa`、`manifest.plist` 和一段 `IOS_*` 配置。
6. 把签好的 IPA 和 `manifest.plist` **用 HTTPS** 放到 `--ipa-url` 所在的目录（文件名要和 URL 一致），把打印出的 `IOS_*` 行写进服务器的 `frontend/.env`，重启 Next.js（不需要重新构建）。
7. 用已登记的 iPhone 的 Safari 打开 `https://test.hyshentou.cn/download`，点“在 iPhone 上安装”。

**首次真机验收**：按 `docs/ios-tun-design.md` 第 9 节、`docs/ios-billing-acceptance.md` 第 3 节、`docs/ios-lifecycle.md` 第 5 节的顺序跑 D 项，再做 R01/R02。把结果写进 `handoff_app_ios.md` 第 15 节。

**如果苹果不接受**：最常见原因是 App ID 没开 Network Extensions、描述文件没重新生成、描述文件里没有这台设备、证书和描述文件不匹配。`resign-ipa.py` 已经检查了前几种；安装失败时把设备的 Console 日志（`installd`、`mobile_installation_proxy`）里的原因记下来。

## 5. 升级、回退与卸载

* **升级**：同一前缀、同一团队、`CFBundleVersion`（构建号）更大的包覆盖安装即可，记住的密码（钥匙串）与 VPN 配置会保留。包 ID 或团队变了，系统会当成另一个 App。
* **回退**：iOS 不允许直接装更低构建号的同一 App，需要先卸载。记住的密码随之清除；设备标识可能留在钥匙串里（见 R02）。
* **卸载**：删除 App 后 VPN 配置一并移除；`device_id` 在钥匙串里“通常保留”，这是预期行为，需要写进隐私说明。
* **描述文件到期**：Ad Hoc 描述文件一年有效，到期后 App 无法启动，需要用新的描述文件重新签名再发新链接。

## 6. 已知限制与未关闭的门槛（来自 `release-info.json`）

1. IPA 未签名；没有付费团队、App ID、描述文件。
2. 第 2、4、5 轮的全部真机项（TUN、计费、生命周期、内存）都是 BLOCKED。
3. R01/R02/R04 需要真机。
4. 受管公网节点的权威计量（B04-live）未做。
5. 法务：Mihomo GPL-3.0 与所选分发渠道的义务、go-lzo（GPL-2.0）组合。
6. 出口合规问卷待填。
7. 图标与启动图是开发期矢量占位，等你提供正式 Logo。
8. 网站没有 `/privacy`、`/terms` 页面（上任何商店之前必须有）。
9. 包 ID 前缀是占位，重签时替换。
10. 本轮没有做 UI 打磨（VoiceOver 全屏走查、iPhone SE 小屏、英文本地化）：这些需要设备/模拟器镜像和你确认文案，未做就如实标为未做。
