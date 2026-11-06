# 客户端下载：发布与运维

> 对应 handoff_download.md（3 轮）。本文写给发布客户端的管理员和部署网站的运维。

从第 3 轮起，下载页 `/download`、首页“多平台客户端”卡片、使用教程里的“前往下载”都读取**后端数据库**里的下载条目。`frontend/.env` 不再有任何下载配置（D1），换版本不需要改文件，也不需要重启。

## 1. 发布一个版本

1. 打包。四个打包脚本都会在各自的 `release-private` 目录写出**后台录入单** `download-entry.txt`（D17），字段与后台表单一一对应：平台、架构、格式、版本号、文件名、SHA-256、文件大小（字节），Android 另有签名证书 SHA-256，iOS 网页安装另有 Bundle ID。
   - Android：`scripts/package-native-android.py`，`artifacts/native-android/release-private/`，每个 ABI 一块。
   - Windows：`scripts/package-native-windows.py --installer`，`<release-dir>-private/`。
   - macOS：`scripts/package-native-macos.py`，`release-private/`。
   - iOS：`scripts/package-native-ios.py` 产出的是**未签名** IPA，不能上架；用 `scripts/ios/resign-ipa.py` 签名后，以它在签名文件旁写出的 `download-entry.txt` 为准。网页安装需要的 `manifest.plist` 由网站生成，不用上传。
   - 录入单是内部资料，`release_scrub` 把它列为“只能在 private 目录”的文件；不要放进公开目录或随安装包分发。
2. 把安装包上传到公网 https 地址（CDN、对象存储或 Nginx 静态目录），在浏览器里确认可以下载。Android 的 `.apk` 需要 `Content-Type: application/vnd.android.package-archive`。
3. 打开 **管理后台 → 客户端下载**，在对应平台点“添加”（已有条目就点“编辑”），按录入单填写，把第 2 步的地址填进“下载地址”。新条目默认不上架。
4. 核对无误后打开“上架”。保存后约 **30 秒**内下载页、首页和教程同步（每个网站进程各自缓存 30 秒）。
5. 打开 `/download` 点一次按钮，确认下载到的文件与页面上的 SHA-256 一致。

换新版本：编辑同一条目，改版本号、地址、SHA-256 和大小后保存。每个“平台 + 架构 + 格式”只有一个当前版本（D2），旧版本的信息留在审计记录里。

## 2. 白名单

平台、架构、格式只能从固定白名单里选（D3），后台和网站各有一份，`test:downloads` 用后端快照 `backend/internal/api/testdata/client_downloads_catalog.json` 比对两份是否一致。新增一种架构或格式需要同时改后端常量、快照、网站常量（`frontend/src/lib/download-catalog.ts`）和名称表。

| 平台 | 架构 | 格式 |
| --- | --- | --- |
| Windows | x64、arm64 | exe、msi、zip |
| macOS | arm64、x64、universal | dmg、pkg |
| Android | arm64-v8a、armeabi-v7a、x86_64、universal | apk |
| iOS | arm64 | ipa（网页安装）、app_store、testflight |
| Linux | x64、arm64 | appimage、deb、rpm、tar.gz |

地址规则（D7）：只接受 https；不能带账号密码或 `#`；文件名后缀必须与格式一致（大小写不敏感，可以带查询参数）；App Store 只能填 `apps.apple.com`，TestFlight 只能填 `testflight.apple.com`。最多 60 条。

推荐架构（下载页上的实心按钮，D10）：Windows x64、macOS arm64、Android arm64-v8a、Linux x64。

## 3. 下载页不显示地址

- 公开列表 `GET /api/v1/downloads` 不含地址和 id；网站只按白名单逐字段复制，后端以后多返回字段也不会到浏览器（D4）。
- 按钮指向本站的跳转路径 `/download/get/{平台}/{架构}/{格式}`，例如 `/download/get/android/arm64-v8a/apk`。网站在服务端向后端的跳转接口取得地址，按 D7 再校验一次，然后 302 到文件；响应带 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`X-Robots-Tag: noindex`。
- 条目不存在、已下架、后端不可达时，跳回 `/download#{平台}`，不报错（D5）。这些路径可以直接发给用户，换版本后仍然有效。
- iPhone 网页安装：按钮是 `itms-services` 链接，只引用本站的 `/download/get/ios/arm64/ipa/manifest.plist`。清单由网站根据条目里的地址、Bundle ID 和版本号生成；链接里的站点地址取自浏览器当前打开的页面，不信任服务端收到的 Host 头。
- 真实地址并不保密：浏览器的下载记录里能看到最终地址。需要防盗链时在 CDN 侧做签名地址，不在本功能范围内。

## 4. 部署顺序与故障

1. **先部署后端**（第 1 轮已合并），执行迁移，用 doctor 确认 `client_downloads` 表存在。
2. 在后台录入现有的条目并上架（旧 `.env` 里的 `ANDROID_APK_*`、`IOS_*` 值，或打包产出的录入单）。
3. **再部署网站**。网站先上线而后台还没有条目时，各平台显示“暂未发布”（D1 的已知代价）。
4. 部署后可以删除 `frontend/.env` 里的 `ANDROID_APK_*`、`ANDROID_CERT_SHA256`、`ANDROID_SOURCE_URL`、`IOS_IPA_*`、`IOS_MANIFEST_URL`、`IOS_SOURCE_URL`，网站已不读取。

后端暂时不可达时：网站继续使用上一次成功读到的列表（最长 24 小时）；从未读到过时，页面照常打开，每个平台显示“下载信息暂时无法获取，请稍后刷新”，首页卡片显示“前往下载页”，不会把已发布的平台说成“即将上线”（D9、D14）。后端恢复后，下一次读取（最多约 30 秒）自动恢复。

真实来源地址：跳转请求和 `/api/v1` 代理一样，只从 `CLIENT_IP_HEADER` 指定的请求头取一个 IP，以 `X-Forwarded-For` 转发给后端，后端只信任 `TRUSTED_PROXIES` 里的来源（见 docs/deployment.md“真实来源地址”）。未配置时，后端看到的是网站服务器的地址，公开接口的限流会按网站服务器计数。

## 5. 需要同步修改的文案

- 使用教程里的设备说明仍写着“Windows 仅支持 x64”“暂不支持 Intel 芯片的 Mac”。**以后在后台上架 Windows arm64 或 Intel/通用版 Mac 时，必须同时修改 `frontend/src/lib/guides.ts` 中对应的“适用设备”文案**，否则教程与下载页矛盾。
- 下载页的系统要求（Windows 10 2004 及以上、macOS 15 及以上、Android 8.0 及以上、iOS 17 及以上）是前端固定文案（D8），客户端最低系统版本变化时一起改 `frontend/src/components/download-view.tsx`。
- Linux 没有上架条目时显示“Linux 版本即将推出，敬请期待”；上架后自动显示按钮，网站不需要改。Linux 暂时没有“安装、升级与卸载”卡片和使用教程。
