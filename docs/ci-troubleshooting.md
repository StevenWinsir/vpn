# CI 启动失败与本机验收

## PR #1 本轮故障

检查对象为 `StevenWinsir/vpn` 的 PR #1，提交 `0c32de84b1c1b6fe96290e11eae1e4921748474d`，Actions run `37261648035`（第 2 次尝试）。后端、Web/Core、Flutter 和 macOS 四个作业均没有开始执行步骤；GitHub 的 Check annotation 是：

> The job was not started because recent account payments have failed or your spending limit needs to be increased. Please check the 'Billing & plans' section in your settings

这不是编译器或测试失败。仓库所有者需要在 GitHub 账户的 Billing & plans 检查未支付账单、支付方式和 Actions 消费预算/额度，处理后再重新运行作业。本轮没有更改账户支付设置、购买额度、降低测试门槛或换到不受信任的自托管 Runner。macOS arm64 的容量排队 notice 也存在，但不是上述付款错误的替代解释。

原始入口：<https://github.com/StevenWinsir/vpn/actions/runs/37261648035/job/111610613099>

已登录 GitHub CLI 的本机可只读复核：

```sh
gh api repos/StevenWinsir/vpn/actions/runs/37261648035/jobs \
  --jq '.jobs[] | {id,name,status,conclusion,steps}'
gh api repos/StevenWinsir/vpn/check-runs/111610613099/annotations \
  --jq '.[] | {annotation_level,message}'
```

## 不依赖 GitHub 托管 Runner 的回归

### Runner 已启动但 macOS 编译失败

修复提交 `0185eeef6d383c25783dd282463e2246cc5825f0` 的 [run 37302973875](https://github.com/StevenWinsir/vpn/actions/runs/37302973875) 已实际获得四个托管 Runner，不再是上述账户级启动失败。本轮未修改账户付款设置，因此不把恢复运行归因于代码修复或预算调整。

该次 macOS 作业在 Xcode 16.4 / macOS 15.5 SDK 下编译锁定的 `connectivity_plus 7.2.0` 时失败：`value of type 'NWPath' has no member 'isUltraConstrained'`。`#available(macOS 26.0, *)` 是运行期保护，不能让旧 SDK 识别不存在的编译期声明。

工作流在 `macos-15` 上显式选择 `/Applications/Xcode_26.3.app/Contents/Developer`，并在构建前输出 Xcode/SDK 版本、编译一个最小 Network API 探针；工具链缺失或不兼容会立即失败，不静默回退到默认 Xcode。该版本由 [官方 macOS 15 ARM64 镜像清单](https://github.com/actions/runner-images/blob/main/images/macos/macos-15-arm64-Readme.md#xcode) 列出。未降级插件、删除 API 使用或放宽验签。编译使用新 SDK 不等于将应用最低运行系统版本改成 macOS 26。

### 本机辅助回归

从仓库根目录执行，Go/PostgreSQL、已锁定的前端依赖和浏览器须预先安装；脚本不会为此读取或改写真实用户套餐。输出目录应使用新的名字。

```sh
export PATH="/opt/homebrew/bin:$PATH"
python3 scripts/test-native.py
bash scripts/node.sh npm --prefix frontend run lint
bash scripts/node.sh npm --prefix frontend run typecheck
bash scripts/node.sh python3 scripts/test-node-catalog.py \
  --output artifacts/local-catalog-check
```

目录验收包含前端 production build、真实浏览器管理员导入/编辑、普通用户越权拒绝、慢编辑响应不能覆盖新建草稿、受控 Mihomo 转发、0.5/1 倍结算及到期拒绝。使用一次性 PostgreSQL 和随机测试账户，不使用用户粘贴的公网节点密码。

`test:api-proxy` 使用 Playwright；缺少其浏览器可安装 CI 指定的 Chromium，或在 macOS 显式使用已安装的 Chrome（不隐式下载/升级浏览器）：

```sh
PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  bash scripts/node.sh npm --prefix frontend run test:api-proxy
(cd native/legacy-core && go test -race -count=1 ./managed)
```

macOS 原生验收使用 `scripts/test-macos-managed.py`，要求独立构建的 `managed_acceptance` Core，并隔离系统代理命令。它会构建测试入口；验收后必须重新执行正常 `flutter build macos --debug --no-pub`，不能将测试入口或 acceptance Core 当作交付包。

## 本轮复现与边界

macOS 增量构建还复现了独立问题：`flutter build` 返回成功，但切换测试入口与 `lib/main.dart` 后，嵌套的 `App.framework` / `objective_c.framework` 已变化，外层应用仍保留旧签名封装，`codesign --verify --deep --strict` 报 `nested code is modified or invalid`。修复在 `FlClash/macos/Runner.xcodeproj/project.pbxproj` 为 Flutter embed 阶段声明 `App.framework` 输出，让 Xcode 跟踪嵌套产物变更并重签外层包；没有手工覆盖签名、关闭验签或改写 Release 签名身份。

macOS CI 新增“正常入口 → 测试入口 → 正常入口”的增量构建及严格签名回归。这里只编译测试入口，不启动、不连接测试数据库。最终保留 `lib/main.dart`。验证命令如下，开发 ad-hoc 签名通过不代表 Developer ID 签名或 Apple 公证完成：

```sh
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
codesign --verify --deep --strict \
  FlClash/build/macos/Build/Products/Debug/FlClash.app
```

新增 PostgreSQL 并发快照测试和客户端倍率/配置绑定测试，都先在修复前复现失败，再在修复后通过。GitHub 付款故障与这些代码缺陷是不同问题；本机测试通过不代表 GitHub CI 已变绿。

只读 `backend/cmd/doctor` 检查显示：`test@test.com` 权益符合当前开发配置，存在管理员，但启用节点为 0。管理员必须录入自己实际可用的节点后，才能进行真实线路测试。当前 macOS managed 模式是回环 mixed listener 与系统代理，不是完整 TUN；开发测试、付款、签名公证、节点强制撤销及生产反作弊的界限见 [架构](architecture.md) 和 [计费说明](admin-node-metering.md)。

## 偶发失败与判断方法（2026-11-05 起）

合并后 main 的 CI 偶发失败，先区分环境问题还是代码问题，再决定重跑还是修：

- **Android 计费验收：模拟器没启动。** 日志里有 `Not enough space to create userdata partition`（托管 Runner 磁盘只剩约 3 GB，模拟器要 7.3 GB）。这时一个测试都没跑，与代码无关，只重跑失败的作业即可。计费作业另有几项会在负载高的模拟器上偶发失败（B01、B04、N01、C01，handoff_fix.md R2-09 也列了），处理方式相同。
- **macOS bundle 作业的 `U-GO-IPC`（Core 的 Go 测试）有 1 项失败，而同一提交 Linux 的 "Native Core / IPC contract / shared policy"（同一套测试，带 `-race`）通过。** 先在本机跑 `CGO_ENABLED=0 go test -count=1 ./...`（native/core），再重跑失败的作业；两次都通过就是负载敏感的偶发项。`scripts/test-native-macos.py` 从第 3 轮起在 observed 和 evidence 里直接写出失败测试的名字和前后日志（以前只保留输出最后 400 个字符，往往只看到最后一个通过的包），下次可以直接定位。
- 只有同一个测试在重跑后、或在本机也失败，才按代码问题处理。

网速类问题（不是 CI）的排障顺序见 docs/speed-troubleshooting.md。
