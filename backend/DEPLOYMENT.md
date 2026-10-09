# 后端单目录部署与编译

## 本次修复

旧版 `go.mod` 把 `vpn/nodepolicy` 替换为 `../shared/nodepolicy`。把 `backend/` 独立上传为 `/www/wwwroot/test.hyshentou.cn_backend` 后，Go 就会寻找 `/www/wwwroot/shared/nodepolicy`，缺少它即出现 `replacement directory ... does not exist`。这不是 PostgreSQL、`.env`、代理节点参数或文件权限错误，也不是传入 `main.go` 绝对路径引起的。

现在后端使用 `replace vpn/nodepolicy => ./nodepolicy`，所需源文件和测试夹具已随 `backend/` 提交。服务器不需要 FlClash、Flutter、Mihomo 子模块或 Python 来构建 API。完整复制 `backend/`，无论目录叫什么名字都可以构建；不要只复制 `cmd/` 或只修改 go.mod 而漏掉 `nodepolicy/`。

```text
test.hyshentou.cn_backend/
  go.mod
  go.sum
  nodepolicy/
    go.mod
    policy.go
    policy_test.go
    testdata/proxies.yaml
  cmd/api/main.go
  cmd/doctor/main.go
  internal/...
  shared-sources.json
  .env                  # 仅放服务器自己的配置，不在源码包中
```

## 已部署服务器的操作

先在新发布目录解压这次完整的 backend 源码，核对包校验值。保留服务器原有 `.env`、节点加密密钥、数据库及旧二进制备份；源码包只含 `.env.example`，不要把它直接改名覆盖有效配置。不修改数据库、不需要为本次构建修复运行迁移。

在包含新版 `go.mod` 的实际后端目录执行：

```bash
cd /www/wwwroot/test.hyshentou.cn_backend
go version
test -f nodepolicy/go.mod && test -f nodepolicy/policy.go
GOWORK=off go list -m -f '{{.Replace.Dir}}' vpn/nodepolicy
GOWORK=off go mod download
mkdir -p bin
GOWORK=off go build -mod=readonly -trimpath -o bin/api.next ./cmd/api
GOWORK=off go build -mod=readonly -trimpath -o bin/doctor ./cmd/doctor
```

`go list` 应指向此后端目录中的 `nodepolicy`，不再指向外面的 FlClash。推荐按整个包 `./cmd/api` 编译，确保将来新增同包源文件不会漏编；当前 `go build /绝对路径/cmd/api/main.go` 也经过隔离回归验证。

CI 使用 Go 1.26.6，可优先保持一致；不要盲目升级依赖或删除 go.sum。网络首次构建仍须能下载 go.mod/go.sum 中的普通 Go 依赖，但 `vpn/nodepolicy` 已在本地，不需要 `go get vpn/nodepolicy`。无需修改 GOPROXY、关闭节点校验或绕过生产保护来修复本错误。

编译成功不代表现有服务自动更新。确认进程管理器原来的启动文件路径和 WorkingDirectory，再在维护窗口停止旧进程、替换对应二进制并重启。这里的 `bin/api.next` 不会覆盖运行中的文件；服务应以非 root 专用用户运行。保持 WorkingDirectory 为后端目录，因为 API 在工作目录加载 `.env`。检查 `/healthz` 和普通账号登录；不要启动第二个进程争抢相同端口。

本次只是打包/依赖路径修复，没有改变 API 格式、15 种协议规则、流量倍率、套餐、TLS、鉴权或数据库结构，也不需要为此重编译已提供的多协议 macOS DMG。更详细的真实部署与商业边界见仓库 docs/deployment.md 和 docs/managed-protocols.md。

## 开发者维护：校验逻辑不能分叉

唯一手工维护源仍为 `shared/nodepolicy`。`backend/nodepolicy` 是提交到仓库的精确快照，不是独立实现。修改源规则或共享原生 API 测试契约后，在完整仓库中运行：

```bash
python3 scripts/sync-backend-shared.py --write
python3 scripts/sync-backend-shared.py --check
python3 scripts/test-backend-shared.py
# 将新增源文件纳入 Git 索引后：
python3 scripts/test-backend-standalone.py
```

同步检查只读且不自动修复 CI 内容，逐字节校验 Go 源码、测试及脱敏夹具，维护 SHA-256 清单，拒绝未审查文件和符号链接。与真实 pinned Core Option 结构体的对照测试仍在 canonical 模块运行，不能为适配独立服务器而移除。客户端原生构建依赖位置不变，避免改变已验证的构建缓存输入追踪。

新增独立部署回归只把 Git 已跟踪的 backend 文件复制到临时 `test.hyshentou.cn_backend` 目录，刻意不带 `.env`、FlClash、Git 元数据或 go.work。执行后端/协议模块测试与 vet、包路径及 main.go 绝对路径构建，并交叉编译 Linux amd64/arm64。它不运行 API、不访问真实数据库；数据库集成回归仍由 `scripts/test-native.py` 的私有 PostgreSQL 执行。
