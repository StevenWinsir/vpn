# 网站“使用教程”（/guide）维护手册

> 来源：`handoff_guide.md`（2026-11-03，三轮）。本文是日常维护手册：怎样改教程文字、新增一步、在 APP 界面改版后更新截图、补拍系统画面，以及桌面端正式签名后要删掉的内容。决策与取舍在该交接书第 3 节（D1–D12）。

## 1. 它是什么

- 一个纯网站静态内容页（`handoff_seo.md` D5，第 3 轮起）：`/guide` 是总览页（开始之前、四个平台入口；按访客设备高亮推荐的平台，只在浏览器里判断，服务器输出的内容对所有访客相同）；每个平台一个独立地址 `/guide/windows`、`/guide/macos`、`/guide/android`、`/guide/ios`，各有自己的标题和描述（`frontend/src/lib/seo-copy.ts`）。平台切换是四个普通链接。旧地址 `/guide?platform=x` 永久跳转（308）到 `/guide/x`，参数无效时跳到 `/guide`；`/guide/未知值` 返回 404。**不读写后端、不新增接口、不新增数据库表、不新增 `.env` 参数**，与客户端计费、节点、租约、计量没有任何交集。
- 不要求登录（与 `/download` 一致）：未登录也能打开，侧栏照常显示；页面不得包 `RequireAuth`。
- 侧栏入口在 `frontend/src/components/site-shell.tsx`：“下载客户端”之后、“账号设置”之前，管理员同样可见，始终显示。`/guide` 和 `/guide/` 开头的地址都算工作区（有侧栏），“使用教程”在两者上都高亮；在线客服的页面判断（`support-widget.tsx` 的 `isSupportPage`）同样包含平台页。站内链接一律用 `guidePath(平台)`，不要再写 `?platform=`（`test:seo` 会检查）。
- 教程与 APP 版本强绑定，随代码发布、一起评审；不做后台在线编辑。

涉及的文件：

| 文件 | 作用 |
| --- | --- |
| `frontend/src/lib/guides.ts` | 教程内容（四个平台的章节、步骤、截图键与高亮、常见问题）与平台选择函数，纯 TS，不依赖 React |
| `frontend/src/lib/guide-images.ts` | **自动生成**的图片清单（路径、尺寸、来源场景、来源与输出的 SHA-256、字节数、替代文字），不要手改 |
| `frontend/public/guide/<平台>/<键>.<哈希前8位>.webp` | 处理后的图片，文件名带内容哈希；清单之外的文件不得留在这里 |
| `frontend/guide-shots.json` | 截图选单（来源场景、裁切、输出宽度、替代文字）与“待补拍的系统画面”清单 `pendingManual` |
| `frontend/scripts/guide-images.mjs` | `npm run guide:images` 处理脚本 |
| `frontend/src/components/guide-view.tsx`、`app/guide/[platform]/page.tsx` | 平台页与交互（面包屑、平台切换链接、目录、放大层、编号框、FAQ） |
| `frontend/src/components/guide-hub.tsx`、`app/guide/page.tsx` | 总览页与旧地址跳转 |
| `frontend/scripts/guide.test.mjs`、`tests/guide.spec.ts` | 单元测试与浏览器用例 |

## 2. 内容数据结构（guides.ts）

每个平台一个对象：`id`、`name`、`icon`、`device`（适用设备一行字）、`downloadKey`（`android` / `ios` 对应下载页的发布配置，桌面端为 `null`）、`sections`、`faq`。

- **章节** `Section`：`id`（页面锚点，同一平台内唯一，不得叫 `faq` 或 `help`）、`title`、`steps`、可选 `note`（`tip` 提示或 `warn` 注意）。每个平台必须按顺序包含 `requiredSections` 里的全部章节：`device`、`download`、`authorize`、`login`、`lines`、`connect`、`usage`、`background`、`upgrade`。
- **步骤** `Step`：`title`、`body`（纯文本）、可选 `download: true`（“下载”步骤，每个平台恰好一个；页面按下载是否可用显示“前往下载”或“安装包请联系客服获取”）、可选 `shot` 与 `highlights`。步骤序号由顺序决定，不手写。
- **正文写法**：`**加粗**`；站内链接 `[文字](/路径)`，路径只能是 `guideLinkPaths` 白名单（`/register`、`/plans`、`/download`、`/dashboard`、`/tickets`、`/forgot-password`、`/guide` 和四个平台页）；正文里的 ①②③ 指向同号的截图编号框。正文不得出现网址、邮箱。
- **截图键** `shot`：形如 `ios/vpn-setup`，必须在 `guide-images.ts` 里；只能引用本平台的图。
- **高亮** `highlights`：`{ n, rect: [左, 上, 宽, 高], shape?: 'circle', badge?: 'end' }`，坐标是占图片宽高的百分比（0–100，不得越界），`n` 必须从 1 起按 1、2、3 连续编号，并且和正文里的 ①②③ 一一对应；没有截图的步骤不能有高亮。
- **常见问题** `faq`：`{ q, a }`；四个平台共用的问题在 `commonFaq`，页面显示时与平台自己的合并。

写作规范（`handoff_guide.md` 4.7）：一步只做一件事，动词开头；按钮名称用中文引号并与 APP 完全一致（以各端 `texts.json`、`strings.xml`、视图源码为准，不凭记忆）；不写技术名词与引擎名称；桌面端不出现“VPN”“内核”“协议”“TUN”“Helper”“DNS”“端口”；任何地方不出现“开发版”、真实线路名、真实服务器地址、真实邮箱；不写无法证明的承诺（网站文案守卫的词表）。系统界面文字以中文系统原文为准——如果与守卫词冲突，把原文短语加入 `frontend/scripts/copy-guard.test.mjs` 与 `frontend/tests/copy-words.ts` 两处放行清单并注明“系统原文”，不得关闭守卫。

## 3. 怎样新增或修改一步

1. 在 `guides.ts` 对应平台、对应章节的 `steps` 里增加一项（或改文字）。不要新增章节除非同时改 `requiredSections` 与测试。
2. 需要图时：先按第 4 节把图放进选单与清单，再给步骤填 `shot` 与 `highlights`，正文里写 ①②③。
3. 运行：

```sh
cd frontend
npm run test:guide     # 结构、链接、文案、图片与高亮的全部断言
npm run test:copy      # 源码文案守卫
npm run typecheck && npm run lint
```

4. 浏览器检查（脚本自己起私有 Postgres 与 Next.js，本机需要 Chrome）：

```sh
python3 scripts/test-payment-web.py --output /tmp/guide-out --spec guide --spec ui-copy --spec theme
```

该命令在仓库根目录运行；用 `PLAYWRIGHT_CHROME_PATH` 指定本机 Chrome，`--output` 是输出目录。浏览器用例把 4 个平台 × 390 与 1280 宽 × 浅色与深色共 16 张整页截图写到输出目录下的 `screens/guide-<平台>-<宽度>-<主题>.png`（具体子目录以脚本输出为准），同时断言无横向溢出、无破图。

## 4. APP 界面改版后更新截图

截图来自四个客户端现有的“界面预览”套件（虚构数据，不连服务、不碰真实账号）。APP 改了界面或按钮名称后：

| 平台 | 重新生成素材 | 说明 |
| --- | --- | --- |
| macOS | `python3 scripts/test-native-macos.py --suite ui --run-id guide` | 产出 `artifacts/native-macos/guide-ui/`（含 `texts.json`） |
| Windows | 只能在 Windows 上跑 `python3 scripts/test-native-windows.py --suite ui`；没有 Windows 机器时，取 CI 里 Windows 工作流的 `windows-ui-preview` 产物（保留 14 天）解压使用 | 含 `texts.json` |
| Android | `python3 scripts/test-native-android.py --suite ui`，取 `artifacts/native-android/ui` 里 `guide-*.png`；或取 Android CI 的 `android-ui-screenshots` 产物 | 这些 `guide-` 图由测试类 `GuideScreenshotTest` 用发布渠道的构建信息拍摄，没有“开发版”标记 |
| iOS | `python3 scripts/test-native-ios.py --suite ui --runtime latest` | 产出 `artifacts/native-ios/ui/screenshots`；脚本已把模拟器状态栏固定为 9:41、满信号、满电。worktree 里跑需先从主检出复制被忽略的 `apps/ios/.env`、`Config/Generated.xcconfig`、`Frameworks/` |

然后：

1. 需要换哪张图，就在 `frontend/guide-shots.json` 的 `shots` 里改该条的 `scene` / `source`（来源场景与文件名）、可选 `crop`（`[x, y, 宽, 高]`，源图像素）、`width`（输出宽度，不放大）与 `alt`（替代文字，要描述画面，例如“AsterLink 登录页，邮箱和密码输入框下方是……”）。新增一张图就新增一条，键为 `平台/名字`。
2. 运行处理脚本（只需给出重新生成过的平台，其余平台的图保持不变）：

```sh
cd frontend
PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  npm run guide:images -- --macos <macOS 素材目录> --windows <Windows 素材目录> --android <Android 素材目录> --ios <iOS 素材目录>
```

   脚本用本机 Chrome 解码、裁切、缩放并编码 WebP（质量 0.82，不新增 npm 依赖），写出 `public/guide/` 下带哈希的文件和 `src/lib/guide-images.ts`，并删除清单之外的旧文件。macOS 与 Windows 有 `texts.json`，脚本会检查画面里的全部文字：不得含“开发版”、不得含 `example.com` 以外的域名、不得含网站禁用词，任何一条命中则不写清单并退出。同一输入重复运行结果逐字节一致。
3. **逐张人工过目**新生成的图（Android 与 iOS 没有 `texts.json`，只能靠人看）：无“开发版”、无真实邮箱与域名、无真实线路名、无个人信息；iOS 登录页页脚若出现开发标记要裁掉。
4. 界面元素位置变了，要同步调整 `guides.ts` 里该步骤的 `highlights` 百分比矩形。页面上的编号框要落在对应按钮上：跑一遍浏览器用例，看输出目录里的 `screens/guide-marks-<平台>-<主题>.png`。
5. 体积预算：单张 ≤ 200 KB，全部 ≤ 4 MB（`test:guide` 校验）。
6. 提交：选单、清单、`public/guide/` 下的新增与删除、`guides.ts` 一起提交。CI 不重新生成图片，只校验清单与文件一致。

> 以后 APP 改界面的交接书，应把“更新教程截图”列为收尾项。

## 5. 系统画面（真机拍摄）

APP 自己的界面可以自动生成，但**系统弹窗和系统设置页只能真机拍摄**：不得用网上图片、他人截图或模拟器伪造。拿不到的画面，对应步骤保持纯文字（页面照常工作、没有破图）。

### 5.1 待拍清单

清单的权威来源是 `frontend/guide-shots.json` 的 `pendingManual`（`test:guide` 校验每一项都指向 `guides.ts` 里真实存在、且还没有图的步骤）。截至本轮共 14 张，全部尚未拍摄：

| 键 | 步骤 | 要拍的画面 |
| --- | --- | --- |
| `windows/smartscreen` | Windows › 下载安装 › 运行安装程序 | “Windows 已保护你的电脑”窗口，能看到“更多信息”，再点开后的“仍要运行” |
| `windows/uac` | Windows › 下载安装 › 允许管理员权限 | “用户账户控制”弹窗，能看到“是” |
| `macos/dmg-drag` | macOS › 下载安装 › 拖进“应用程序” | 打开 DMG 后的窗口：AsterLink 与“应用程序”文件夹 |
| `macos/open-anyway` | macOS › 下载安装 › 遇到“无法验证”提示 | 系统设置 › 隐私与安全性里被阻止的 AsterLink 与“仍要打开” |
| `macos/login-items` | macOS › 首次授权 › 在系统设置里打开开关 | 系统设置 › 通用 › 登录项与扩展，“允许在后台”里 AsterLink 的开关 |
| `macos/keychain` | macOS › 首次授权 › 遇到钥匙串提示 | “AsterLink 想要使用钥匙串中的……”，能看到“始终允许” |
| `android/download-done` | Android › 下载安装 › 用手机浏览器下载 | 浏览器下载完成的提示，能看到“打开” |
| `android/unknown-apps` | Android › 下载安装 › 允许安装 | “安装未知应用”设置页里该浏览器的开关 |
| `android/vpn-request` | Android › 首次授权 › 允许 VPN 连接 | 系统“连接请求”对话框，能看到“确定” |
| `android/notify-permission` | Android › 首次授权 › 允许通知 | Android 13 及以上的通知权限对话框 |
| `android/notification` | Android › 后台运行与退出 › 通知栏里的状态 | 下拉通知栏里 AsterLink 的常驻通知 |
| `ios/install-confirm` | iPhone › 下载安装 › 点“在 iPhone 上安装” | Safari 的“是否安装”确认，能看到“安装” |
| `ios/vpn-add` | iPhone › 首次授权 › 允许添加 VPN 配置 | “AsterLink 想要添加 VPN 配置”弹窗，能看到“允许” |
| `ios/status-vpn` | iPhone › 后台运行与退出 › 切到其他应用 | 已连接时状态栏出现的“VPN”图标（只截状态栏一角） |

一步只能挂一张图；Windows“找回图标”一步已有托盘菜单图，不再补拍“^”展开图。

### 5.2 拍摄要求

- 系统语言简体中文，浅色模式，常规字号。
- 状态栏没有个人通知；画面里没有真实邮箱、手机号、姓名、Apple ID、Wi-Fi 名称或其他个人信息（有就换账号重拍，或在手机上先关掉再拍，不要在图上涂抹）。
- 只截与步骤相关的区域；用设备自带的截图功能，PNG 原图。
- 每张图记下：拍摄设备与系统版本（例如“iPhone 15，iOS 17.5”“MacBook Air M2，macOS 15.1”）。原图不入库，只入库处理后的结果。

### 5.3 入库流程

1. 把原图放进一个新的空目录（例如 `/tmp/guide-manual/`），文件名自定。
2. 在 `guide-shots.json` 的 `shots` 里新增一条，并把该键从 `pendingManual` 里删掉：

```json
{
  "key": "ios/vpn-add",
  "platform": "ios",
  "scene": "manual:vpn-add",
  "source": "vpn-add.png",
  "device": "phone",
  "width": 600,
  "manual": { "device": "iPhone 15", "system": "iOS 17.5" },
  "alt": "系统弹窗“AsterLink 想要添加 VPN 配置”，下方有“不允许”和“允许”两个按钮"
}
```

   `scene` 必须以 `manual:` 开头，`manual.device` 与 `manual.system` 必填（脚本与测试都会检查）。桌面端的 `device` 用 `desktop`（`width` 不超过 1440），手机用 `phone`（约 600）。桌面端的 `alt` 同样不得出现“VPN”等词。
3. 运行 `npm run guide:images -- --manual /tmp/guide-manual`。手动图与套件图各自独立：跑套件不会动手动图，跑 `--manual` 也不会动套件图。清单里这些图会多一个 `capture`（设备与系统）字段。
4. 逐张人工过目（隐私、文案），在步骤上填 `shot` 与 `highlights`，正文写 ①②③，跑第 3 节的测试。
5. 如果该步骤是第 6 节列出的“临时步骤”，图随步骤一起删除。

## 6. 桌面端正式签名后要删掉的内容

目前桌面端只有临时签名 / 未受信签名的内部通道（`docs/macos-release.md`、`docs/windows-release.md`），下载页没有桌面安装包，教程里“获取”一步写的是“没有列出时请联系客服”。以下内容**只在未签名、未公证的版本上出现**，正式签名与公证并公开发布后必须删除，否则会误导用户：

| 位置（`guides.ts`） | 处理 |
| --- | --- |
| Windows › 下载安装 › “运行安装程序”正文里的“Windows 已保护你的电脑 … 仍要运行”一句 | 删除这句（已签名安装包不会出现 SmartScreen 拦截；刚发布、信誉不足时仍可能出现，以实测为准） |
| macOS › 下载安装 › “遇到‘无法验证’提示”整步 | 删除整步 |
| macOS › 首次授权 › “遇到钥匙串提示”整步 | 删除整步（正式签名版升级不再反复询问） |
| `pendingManual` / `shots` 中的 `windows/smartscreen`、`macos/open-anyway`、`macos/keychain` | 一并删除（已入库的则从选单删除后重跑脚本，会清除旧文件） |
| 同时，桌面端 `download` 步骤的“联系客服获取” | 桌面安装包上架下载页后，`platforms.windows.downloadKey` / `platforms.macos.downloadKey` 改为对应键，并在 `downloads.ts` 与 `downloadAvailable` 里加入桌面端的可用性判断；`test:guide` 里“只有 Android 与 iPhone 有下载键”的断言要一起改 |

## 7. 测试与 CI

- `npm run test:guide`（CI 的 Web validation 里运行）：四个平台齐全、必备章节、锚点唯一、每平台恰好一个下载步骤；链接都在白名单内；教程与图片替代文字过禁用词（复用 `copy-words.ts`，桌面端另禁“VPN”等）；没有网址与邮箱；平台选择函数；选单与清单一一对应、来源场景可追溯、文件字节与哈希与尺寸和清单一致、文件名带哈希、单张与总量预算、`public/guide` 下没有清单外的文件；高亮不越界、编号与正文一致；手动图必须带拍摄设备与系统；`pendingManual` 指向真实步骤。
- `npm run test:copy`：源码文案守卫，覆盖 `app/guide/page.tsx`、`components/guide-view.tsx`、`lib/guides.ts`、`lib/guide-images.ts`。
- 浏览器用例 `tests/guide.spec.ts`（随 `playwright.payment.config.ts`，由 `scripts/test-payment-web.py` 驱动）：侧栏位置与高亮（用户与管理员）、未登录可访问、深链与刷新保持、四种设备 UA 自动识别、下载可用性提示、下载页互链、四平台 × 两种宽度 × 两种主题无横向溢出并存档整页截图、无破图、只请求当前平台图片、放大层、减少动态效果、编号框落点证据。`ui-copy.spec` 与 `theme.spec` 的页面清单也包含 `/guide`。
- 本功能不涉及后端、数据库与 APP 产品代码，不需要 Go 测试与 APP 套件的全量回归；改了 APP 的截图测试代码时只需跑对应的 `--suite ui`。

## 8. 发布、回滚与缓存

- 发布：`frontend` 目录 `npm ci && npm run build`，重启 Next.js 进程；不需要新增 `.env` 参数，后端与 APP 都不用升级。回滚：回到上一版前端，侧栏入口与 `/guide` 一并消失，无数据残留。
- 图片都在同源 `/guide/<平台>/` 下，文件名带内容哈希，换图后文件名就变，所以可以长缓存。Next.js 对 `public/` 下文件默认不带长缓存；在 Nginx 前端站点里加一段（可选，不加不影响功能，写法见 `docs/deployment.md`“使用教程图片的长缓存”）。

## 9. 用户部署后的实测（U-47 … U-51）

前提：生产环境已部署；准备一个有有效套餐的普通账户。与开发方的本地结果分开记录。

| 编号 | 步骤 | 预期 |
| --- | --- | --- |
| U-47 | 在电脑浏览器登录网站，看左侧栏 | “下载客户端”下方有“使用教程”，点进去默认平台与这台电脑的系统一致 |
| U-48 | 用 iPhone 的 Safari 和 Android 手机浏览器分别打开网站的“使用教程” | 各自默认显示本平台教程；图片清晰、无横向滚动；已配置下载时“前往下载”可用 |
| U-49 | 请一位不熟悉本产品的人，只看教程在自己的手机上完成安装与连接 | 不需要额外讲解即可连上；卡住的步骤记录下来回来修订 |
| U-50 | 在 Windows 与 Mac 上各照教程完成一次安装、首次授权、连接、关闭窗口后从托盘 / 菜单栏找回 | 每一步的按钮名称、画面与教程一致 |
| U-51 | 复制一条带 `platform` 参数的教程链接，通过工单回复或客服对话发给用户 | 对方打开直接是该平台的教程 |

任何一步与预期不符，截图并记录设备型号、系统版本、APP 版本。

## 10. 已知限制与观察项

- 系统画面尚未拍摄（第 5.1 节 14 张），相关步骤目前是纯文字。
- iPhone 仅限已登记设备安装（Ad Hoc 分发），教程无法替代设备登记流程，点安装失败时只能联系客服。
- 各品牌安卓手机的省电设置入口差异大，教程只给通用说法。
- 下载页“开源许可与源代码”段落仍提到代理内核名称，与 APP 去敏口径（`handoff_app_standardization.md` D10）不一致，是否调整由需求方另行决定，不在本功能范围内。
