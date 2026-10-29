# Android 发布：签名、打包、安装/升级/卸载与回退

> 第 6 轮（handoff_app_android.md 第 10 节）。入口：`scripts/package-native-android.py`（签名打包）、
> `scripts/test-native-android.py --suite release`（审计 + 安装演练，实现在 `scripts/android_release_suite.py`）。
> 分发方式：官网直链下载 APK（不上 Google Play）。当前渠道 `internal`，`release-info.json` 中
> `trusted_release=false`，直到第 7 节的外部门槛全部关闭。

## 1. 签名：免费自签的 AsterLink 发布密钥

Android 不需要（也不认）证书机构签发的证书：系统只认“同一把密钥”。因此发布密钥是免费自签的，
但**它就是这个 App 的身份**：

* 以后每个版本都必须用同一把密钥签名，手机才允许覆盖升级；密钥换了，用户只能卸载重装（丢失记住的密码与设置）。
* 丢失密钥 = 再也发不了可升级的新版本。泄露密钥 = 别人能做出可覆盖安装的“官方”包。

一次性创建（只在发布机上执行，已于本轮在开发机执行过一次）：

```bash
python3 scripts/package-native-android.py --create-keystore
```

* 文件：`~/.asterlink/android/asterlink-release.p12`（PKCS12，别名 `asterlink`，RSA 4096，有效期 30 年，目录权限 700、文件 600），**不在仓库内**。
* 密码：随机生成，存入 macOS 登录钥匙串（服务 `asterlink-android-release`，账户 `keystore`），任何时候都不出现在命令行参数、源码或日志里。
* 已存在时拒绝覆盖。
* 证书 SHA-256 指纹会打印出来，并写入每次发布的 `release-info.json` 和网站下载页（用户可据此核对是否官方包）。

**立即离线备份**（两样都要）：

```bash
cp ~/.asterlink/android/asterlink-release.p12 /Volumes/<离线U盘>/
security find-generic-password -s asterlink-android-release -a keystore -w
```

第二条命令打印密码（会弹出钥匙串授权），抄写/存入密码管理器。换机发布时：把 `.p12` 放回原路径，
再用 `security add-generic-password -s asterlink-android-release -a keystore -w` 交互输入密码；或者在
其它系统上用环境变量 `ASTERLINK_ANDROID_KEYSTORE`（路径）、`ASTERLINK_ANDROID_KEYSTORE_PASSWORD`、
`ASTERLINK_ANDROID_KEY_ALIAS`（默认 `asterlink`）提供。

签名方案：APK Signature Scheme **v2 + v3**（v1/JAR 签名关闭：minSdk 26 ≥ 24 不需要；v4 只用于 adb 增量安装，不需要）。
apksigner 通过 `--ks-pass env:` 读密码。AGP 写出的 APK 未签名，Gradle 从不接触发布密钥。

## 2. 构建与打包

前提：`apps/android/.env`（git 忽略）必须是生产参数：

```dotenv
APP_ENV=release
API_BASE_URL=https://demo.hyshentou.cn/api/v1/client
WEBSITE_BASE_URL=https://test.hyshentou.cn
```

发布正式域名时改这两个 URL 后重新打包（URL 在构建时写入 APK，装好的 App 不会跟着变）。然后：

```bash
python3 scripts/package-native-android.py --channel internal
python3 scripts/test-native-android.py --suite release --allow-network-changes
```

打包脚本做的事：

1. `build-native-android.py --target apk --configuration Release`：gomobile 绑定 Core（`-s -w`，版本号写入），
   Gradle `assembleRelease`（R8 压缩混淆 + 资源压缩，`proguard-rules.pro` 保留 gomobile 绑定；按 ABI 拆分），
   检查每个 `.so` 未压缩存放、ELF 架构正确、LOAD 段 ≥ 16 KB 对齐、`zipalign -P 16`，且 `libgojni.so` 与
   `core-build.json` 记录的可复现产物逐字节相同（`keepDebugSymbols` 禁止 AGP 再次 strip）。
2. apksigner 签名并复核：v2、v3 均通过，只有一个签名者，证书指纹等于发布密钥。
3. 输出 `artifacts/native-android/release/`（公开目录）：

> 2026-10-28（handoff_app_standardization.md 第 5 轮，D15）：公开目录只放安装包与只列安装包的 `SHA256SUMS`；许可、源码说明、源码归档、组件清单与发布信息内容不变，移入同级的 `release-private/`（内部资料，不得发布）。发布构建的 Core 不再写入内核提交号。

| 文件 | 内容 |
| --- | --- |
| `AsterLink-<版本>-android-arm64-v8a.apk` | 官网提供下载的安装包（Core 不含内核提交号） |
| `SHA256SUMS` | 只列 APK |

   以及 `artifacts/native-android/release-private/`（**内部资料，不要公开**；CI 工件 `android-release-private-INTERNAL`，不含源码归档）：

| 文件 | 内容 |
| --- | --- |
| `THIRD_PARTY_LICENSES.txt` | libgojni.so 的全部 Go 模块许可（按 ABI）+ AndroidX/Kotlin 等 Maven 依赖清单与 Apache-2.0 全文 |
| `dependencies.cdx.json` | CycloneDX 形式的依赖清单 |
| `SOURCE.txt` + `asterlink-source-*.tar.gz` + `mihomo-source-*.tar.gz` | 本仓库与内核的对应源代码 |
| `release-info.json` | 版本、versionCode、各 APK 的哈希与签名、证书指纹、Core/补丁身份（含 `revision_stamped:false`）、`trusted_release=false` 与未关闭门槛 |
| `mapping-<版本>.txt` | R8 映射，还原崩溃堆栈用，每个版本都要保存 |
| `website-env.txt` | 下载页配置，见第 4 节 |
| `SHA256SUMS`、`README-内部资料.txt` | 内部目录自身的哈希与“不得发布”说明 |

### 版本号

`apps/android/gradle.properties` 的 `asterlink.appVersion` 是唯一来源；`versionCode` 由它推导
（`主*10000 + 次*100 + 修订`，0.1.0 → 100），不能手改，次/修订号须 < 100。每次发布必须提高版本号，
否则手机拒绝覆盖安装。Core 版本与 App 版本相同，后端 `CLIENT_MIN_APP_VERSION` 与它比较。

### ABI 决策

发布只出 **arm64-v8a**：2017 年后的主流手机均为 64 位，Android 15+ 的 16 KB 页设备也只有 64 位。
`armeabi-v7a`（32 位）构建链可用（`--abi armeabi-v7a`），但本机没有能运行 32 位的 arm64 模拟器镜像，
未经运行验证，不发布。`x86_64` 只用于 CI 的 KVM 模拟器。

### 包体

| 产物 | 大小 |
| --- | --- |
| Release APK（arm64-v8a，R8） | 约 46 MiB（libgojni.so 44.4 MiB 未压缩存放） |
| Debug APK | 约 57 MiB |

`.so` 按计划不压缩：安装时不必解压，直接从 APK 映射（16 KB 页设备要求对齐）；代价是下载体积。
若以后改成压缩存放（`useLegacyPackaging = true`），下载约可减半，但安装后占用更多空间，需重新跑本套件。

## 3. 验收（`--suite release`）

| 用例 | 证明 |
| --- | --- |
| R03-checksums / -not-trusted / -version | SHA256SUMS 全部匹配；渠道 internal、`trusted_release=false` 且列出门槛；versionCode 符合推导 |
| R03-release-key | 用 AsterLink 发布密钥签名（临时密钥记 BLOCKED：流程正确但不可发布） |
| R03-&lt;abi&gt;-signature | apksigner：v2+v3、一个签名者、证书与 release-info 一致、不是 Android Debug 证书 |
| R03-&lt;abi&gt;-manifest | 包名、版本、minSdk 26、targetSdk 36、非 debuggable/testOnly、无明文、备份关闭、权限在白名单内、无测试 Activity |
| R03-&lt;abi&gt;-alignment | zipalign -P 16；只含本 ABI 的 .so，全部未压缩、ELF 架构正确、LOAD ≥ 16 KB |
| R03-&lt;abi&gt;-no-test-code | dex 与 libgojni.so 中没有测试框架、验收测试、TUN 钩子、调试工具类 |
| R03-&lt;abi&gt;-no-secrets / -secret-self | 所有条目中无私钥、连接串、本地 .env 秘密值、发布密钥密码；扫描器能发现植入的秘密 |
| R03-&lt;abi&gt;-endpoint | BuildConfig 为 HTTPS 生产 API，`APP_ENV=release` |
| R03-public | 公开目录只有 APK 与只列 APK 的 `SHA256SUMS` |
| R03-licenses / -source-archives / -private-kept-out | `release-private/` 中许可、清单、SOURCE.txt、两个源码包齐全；mapping 与密钥不在公开目录 |
| R03-&lt;abi&gt;-scrub / R03-scrub-self | APK 内无许可/源码/发布信息文件，文本条目无内核标记，二进制命中不超出 `scripts/release-scrub-baseline.json`（D16），Core 不含内核提交号；扫描器能发现植入样本 |
| R01-api36 / -api36-16k / -api26 | 发布 APK 在各镜像上干净安装、启动、登录页显示 Core 与 Mihomo 版本（R8 下 JNI 正常）、无崩溃；缺镜像记 BLOCKED |
| R01-rehearsal-* | 同一发布流水线（R8、拆分、apksigner）以本机回环测试后端构建两个版本、同一把临时密钥签名，经真实界面：全新安装 → 登录（记住密码）→ 选线 → **系统 VPN 授权弹窗** → 已连接（真实 TUN）→ 断开 |
| R02-upgrade / -upgrade-consent | 覆盖升级到下一版本：自动登录（记住的密码保留）、再次连接不再弹授权 |
| R02-downgrade / -foreign-key | 降级安装被系统拒绝（`INSTALL_FAILED_VERSION_DOWNGRADE`）；其它密钥签名的同版本被拒绝（`INSTALL_FAILED_UPDATE_INCOMPATIBLE`） |
| R02-uninstall | 连接中卸载：VPN 立即消失、包与 VPN 授权记录消失；重装后是空白登录页（数据已清除） |
| R04-device | `--device <序列号>` 在真机上执行 R01 冒烟；没有真机记 BLOCKED |

负向对照（本轮实测）：同一“无测试代码”扫描器对 Debug APK 报出 `Landroidx/compose/ui/test/` 与 `PreviewActivity`，证明它能失败；秘密扫描每次运行都做植入自检（R03-secret-self）。

**公司 VPN 的影响：** 审计与安装、登录、断开都只走模拟器与本机回环，不受影响；演练中“已连接”需要
Core 的 Verify 经测试节点访问公网探测地址，这一步经过本机网络（即公司 VPN），历史各轮在公司 VPN 下均通过。
对真实线路/demo 服务器的检查需断开公司 VPN（见第 6 节）。

## 4. 网站下载页

网站新增 `/download`（公开页面，顶部导航“下载客户端”）。它在每次请求时读取 `frontend/.env` 的服务端变量，
**改完只需重启 Next.js，不需要重新构建**；任一必填值缺失或格式错误时页面显示“暂未发布”而不是错误链接。

1. 把 APK 上传到网站服务器，例如 `https://test.hyshentou.cn/downloads/AsterLink-0.1.0-android-arm64-v8a.apk`
   （Nginx 静态目录或 `frontend/public/downloads/`；需为 `.apk` 设置 `Content-Type: application/vnd.android.package-archive`）。
2. 把 `artifacts/native-android/release-private/website-env.txt` 的几行写入服务器的 `frontend/.env`：

```dotenv
ANDROID_APK_URL=https://test.hyshentou.cn/downloads/AsterLink-0.1.0-android-arm64-v8a.apk
ANDROID_APK_VERSION=0.1.0
ANDROID_APK_SHA256=<64 位十六进制>
ANDROID_APK_SIZE_BYTES=<字节数>
ANDROID_CERT_SHA256=<发布证书指纹>
ANDROID_SOURCE_URL=   # 可选：公开 THIRD_PARTY_LICENSES.txt / SOURCE.txt / 源码包的目录地址
```

3. 重启 Next.js。App 中“需要更新”（服务端返回 `client_upgrade_required`）与“我的 → 开源许可与源代码”都跳到这个页面。

GPL 要求：随 APK 一起公开 `THIRD_PARTY_LICENSES.txt`、`SOURCE.txt` 与两个源码包（填 `ANDROID_SOURCE_URL`），
或至少能按要求提供；最终方式由法务确认。

## 5. 安装、升级、卸载、回退（给用户与运维）

* **安装**：手机浏览器下载 APK → 打开 → 系统提示“未知来源”时允许该浏览器安装。首次连接时系统询问是否允许建立 VPN，选“允许”。
  Play 保护机制可能提示“未知开发者”，这是自签发布的正常现象（选择“仍然安装”）。
* **升级**：下载新版本直接覆盖安装。记住的密码、设置、VPN 授权都保留（R02 实测）。正在连接时覆盖安装会结束连接，安装后重新连接即可。
* **卸载**：系统设置中卸载。VPN 配置、记住的密码（Keystore 密钥随应用删除）、设备标识全部消失（R02 实测）。
  服务端的登录会话按租约自动过期；如设备名额紧张，可在后台清理。
* **回退**：Android 不允许安装比当前更低的版本（R02 实测）。要回退只能“卸载 → 安装旧版”（会丢失本机设置，需重新登录）。
  更好的做法是发布一个更高版本号的修复版。若服务端设置了 `CLIENT_MIN_APP_VERSION`，低于它的旧版连接时会收到“需要更新”。
* **强制升级**：后端 `backend/.env` 设置 `CLIENT_MIN_APP_VERSION=<新版本>`；旧客户端连接时 App 显示“下载新版本”按钮并跳到下载页。

## 6. 发布前人工检查（需要用户执行）

1. **断开公司 VPN、退出 macOS 版 AsterLink**（否则测的是公司网络）。
2. 用 Android Studio 模拟器（或真机）安装 `artifacts/native-android/release/` 中的 APK；用你的真实账号登录 demo 服务器，选择专线与普通线路各连接一次，
   打开 Chrome 浏览网页、播放一段视频；在网站“总览”核对流量按倍率扣减。
3. 真机抽检（R04）：用 USB 调试连接手机，执行
   `python3 scripts/test-native-android.py --suite release --device <adb devices 中的序列号>`；
   再手工在真机上连接、锁屏 10 分钟、解锁后确认仍连接，切换 Wi-Fi/移动数据各一次。国产 ROM（小米/华为/OPPO/vivo）请在“后台运行与省电”中设置为不限制。
4. 全部通过且第 7 节门槛关闭后，才可以把 `trusted_release` 视为 true 并对外收费。

## 7. 已知限制与未关闭门槛（`release-info.json` 的 `open_gates`）

* R04 真机/云真机抽检、厂商 ROM 后台清理、真实射频切换与耗电：本机无真机，BLOCKED。
* B04-live：公网受管节点的权威计量（`node_reported`）不存在；`client_reported` 只是开发计量。
* 法务：Mihomo GPL-3.0 义务与 go-lzo（GPL-2.0）组合问题。
* 网站运营：支付回调、退款、找回密码、隐私政策、用户协议页面（App 目前没有放隐私政策/用户协议链接，网站上线后在 `WebPage` 枚举加一项即可）。
* 分发：自签 APK 从网站安装会有“未知来源”提示，Play 保护机制可能警告；上 Google Play 需另外的 VPN 政策声明与 Play 应用签名。
* 32 位（armeabi-v7a）未发布；API 29、API 37 镜像未在本机安装（API 26/36/36-16 KB 已测）。
