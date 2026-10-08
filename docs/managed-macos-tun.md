# Managed macOS：真实 TUN 与既有 Core 生命周期

## 范围

基于 main `ad9795f77ad14f1c1e616a17a1a0705fdf622062`，在独立 worktree `.worktrees/managed-macos-tun`、分支 `feat/managed-macos-tun` 开发。主目录已有的打包修改没有复制、覆盖、stash 或混入提交；不修改前后端、数据库结构、真实账号、套餐及节点凭据，不自动合并。

审查并选择性复用了历史未合并提交 `f7b501f` 的 TUN、路由检查、错误恢复和初始化生命周期代码。本轮额外移除了 macOS mixed 监听依赖，添加当前 Core 身份 IPC、登录前权限门禁及对应回归。没有引入后续 PF 防火墙代码，也没有重新打开历史 PR。其他平台保留已有入口，不能把 macOS 结果推广为其他平台验收。

## 启动与授权

本仓库 macOS 使用 FlClash 已有的 `System.authorizeCore()` 授权可执行文件，再通过 `CoreAction` → `CoreController` → `DesktopCoreLifecycle` 重启 Core。它不是 Windows/Linux 的常驻 Helper 服务，也不是本轮新建的 macOS SMAppService Helper。

新 IPC `managedTunReady` 由实际回应请求的 Core 检查初始化状态和 effective UID。它只返回布尔值，不返回进程路径、凭据或配置，也不授予代理会话。文件已经 root-owned/setuid 不代表正在运行的旧进程已经提权；不再依靠文件权限或按名称扫描其他进程来推断运行身份。

流程为：Core 初始化确认 → 查询当前 Core 权限 → 首次授权并经唯一生命周期持有者重启 → 重新确认初始化和权限 → 用户登录 → 后端套餐/节点授权 → 打开 TUN。账户页在权限确认前禁用登录，账户状态机也在发送登录凭据前再次检查权限。已经提权的 Core 不反复展示授权按钮，重复授权结果为 none 且 Core 也确认就绪时不无意义重启。重启清除旧会话，要求重新登录；选择记住密码时仍沿用既有 Keychain 存储。

Core `connecting` 不再误报不可用；只有初始化成功后才发布 connected。初始化失败和过程中崩溃不能被迟到的成功覆盖。权限查询不访问未连接/正在重启的 Core，避免无效 IPC 定时器。错误只展示白名单诊断码，不回显 YAML、密码或服务端响应。

## 数据面与配置

正常路径：

```text
macOS TCP/UDP socket → utun（IPv4/IPv6）→ Mihomo managed data plane
                    → 后端授权的选中节点 → 目标
```

macOS 网络策略固定使用 gVisor TUN、自动出口探测、双栈路由及 TCP/UDP 53 DNS 接管。使用成对 `/1` 路由而不是覆盖系统已有 `/0`；发现其他 VPN 占用所需路由时拒绝启动，不删除其他应用的路由，不强行断开其他 VPN。Mihomo 出站及管理 API 的连接沿既有物理出口绑定，避免管理流量进入自己的隧道循环。

服务端 YAML 继续通过受控节点/规则验证，本地特权 TUN、DNS、监听器策略不由管理员上传的任意字段透传控制。严格验证后添加终止 `MATCH,REJECT`，防止选中不支持 UDP 的节点后 Mihomo 跳过匹配并隐式回落 DIRECT。支持 UDP 的节点仍可以正常代理 UDP。

**成功连接也不启动 macOS mixed 监听。** 为保持原生 RPC 兼容，`mixed_port` 字段仍接收合法端口值，但 macOS 最终监听端口固定为 0；其他平台维持原逻辑。应用 HTTP 客户端不设置显式 HTTP 代理，使用 OS 路由进入 TUN。保存过的 system-proxy 偏好不会启用系统代理；原 ProxyManager 只负责按既有所有权日志恢复本应用遗留设置，不接管其他软件代理。

## 停止、同步与计量

TUN 的资源所有权保存在现有配置锁内，只有网络启动成功才允许报告 running。权限不足、路由冲突或启动失败不降级为系统代理。清理失败保留资源所有权并拒绝再次开启；取消、断开、配置替换和退出沿同一停止路径关闭入口与连接。

TUN 和既有代理入口共用 managed traffic tunnel、Mihomo 累计计数、停止排空和结算流程。节点切换、套餐/额度检查、配置代际及流量上报的去重和倍率快照规则不重写；旧流量未确认时不先开始新计费段。后端会话刷新不能把本地 TUN 启动错误抹掉或误显示已经连接。

计量依然是 `client_reported`：增加 TUN 接入并没有把它变成节点侧独立账本。隐藏 YAML 也不能阻止设备所有者提取共享节点凭据；商业到期/超额强制执行仍需节点侧每用户鉴权、独立计量及可撤销凭据。原生产保护保持不变。

## 自动验证

普通开发机上的检查不改变系统网络，不访问实际数据库：

```bash
# 在仓库根目录；使用项目工具链
bash scripts/flclash-env.sh flutter pub get
bash scripts/flclash-env.sh dart run build_runner build
bash scripts/flclash-env.sh dart run intl_utils:generate
bash scripts/flclash-env.sh flutter analyze --no-pub --no-fatal-infos
# 按 FlClash/.agents/commands.md 临时禁用两个 build_assets，测试后恢复
bash scripts/flclash-env.sh flutter test --no-pub --concurrency=2 --coverage
bash scripts/flclash-env.sh dart run tool/check_coverage.dart coverage/lcov.info 75
(cd FlClash/core && CGO_ENABLED=0 go test -tags=with_gvisor -count=1 ./...)
(cd FlClash/core && CGO_ENABLED=0 go vet -tags=with_gvisor ./...)
(cd FlClash/core && go test -race -count=1 ./managed)
```

本地不设置 `RUN_MANAGED_TUN_*`。这些 opt-in 测试会修改路由，必须在隔离且可恢复的 macOS runner 上执行。

独立 `.github/workflows/macos-tun.yml` 实际创建 utun，验证普通 IPv4/IPv6 UDP 往返、既有默认路由、其他 VPN 的冲突保护及清理后重连。新增 TCP 测试使用普通无代理 HTTP 客户端，通过真实 TUN 和本地受控 HTTP CONNECT 节点上传/下载，确认 managed 累计计数增长、mixed=0、断开排空及再次连接。sudo 与普通用户启动的 setuid 二进制分别运行；前后比较 `scutil --proxy` 不变。

既有主 CI 保留 PostgreSQL/后端竞态、浏览器到实际 Mihomo 的倍率结算、Flutter 全量及覆盖率和 macOS 普通应用构建。新增真实 Flutter → Rust IPC → Go Core 授权重启测试在隔离 home/socket 下运行；CI 给测试 Core 设置 setuid 后通过账户页首次授权按钮重启，验证旧进程不会因 chmod 自动提权、后续四次重启均完成初始化与权限确认。这个检查不等于用户机器的系统密码授权对话框验收。

测试命令和代码不是通过证明；结果以本 PR 当前提交的 Checks 为准。没有使用历史 PR 的绿灯代替本次结果。

## M4 安装后人工验收与限制

应用复制到可写安装位置后运行，不要从只读 DMG 内授权。首先确认“授权 → 内核重启 → 可登录”，然后使用已有测试账号登录；关闭其他 VPN 的连接而非仅关闭其窗口。选择套餐允许的节点，检查 utun/路由和系统代理状态，再验证普通 TCP、UDP、节点切换、倍率扣量、到期/额度关闭、断开重连及正常退出。真实线路质量、倍率到账与公网出口应使用管理员实际配置逐项核对，不以本地代理夹具代替。

**本轮不提供系统级 kill switch，不宣称 WebRTC 零泄漏。** TUN 路由不等同于防火墙隔离；显式绑定物理接口/源地址的流量可能绕过普通 TUN 路由，root 进程也不受本轮出口限制。没有启用 PF，正常断开恢复直连；Core 崩溃时不能保证阻断外网。浏览器已知的本地/原生公网 IPv6 ICE candidates 也不会因 TUN 配置自动消失。公网 DNS、IPv6、WebRTC、网络切换、睡眠唤醒及异常退出必须另做真实节点验收，不能将普通 UDP 捕获用例解读为这些保证。

商业分发还需要审查继承的 setuid 授权和特权 Core 安全边界、完成正式签名/公证与升级恢复策略；本轮不绕过系统授权，也不声称现有 ad-hoc 开发包已达到商业发行安全标准。
