# 运行、构建与部署

## 环境要求

- 目标环境是 Ubuntu/Linux 本地机器；需要 Go 1.22 或更高版本。
- SSH 能力由系统 OpenSSH 提供，后续实现复用用户的 ~/.ssh/config、ssh-agent、known_hosts 和 ProxyJump。
- 远程端口探测依赖目标 Linux 上的 ss 命令，优先 ss -ltnp，权限不足时退化为 ss -ltn。

## 本地运行

    go run ./cmd/ssh-tunnel-manager

默认监听 127.0.0.1:8765。可用 `--addr` 指定端口，但地址仍必须是 127.0.0.1:<port>、localhost:<port> 或 [::1]:<port>。程序启动时输出带一次性 token 的本地 URL；`--open-browser` 在成功监听后调用默认浏览器，`--version` 输出构建版本。页面关闭不会停止进程或隧道，使用 Ctrl+C 或已认证控制台退出操作关闭。

## Scenario: 源码目录一键启动

### 1. Scope / Trigger

- 适用于 Ubuntu/Linux 下从源码仓库启动开发版，不取代发布包的用户级安装。

### 2. Signatures

- 启动命令：`./start.sh [ssh-tunnel-manager 参数...]`。
- 等价程序调用：`go run ./cmd/ssh-tunnel-manager --open-browser "$@"`。

### 3. Contracts

- 脚本需要 Go 1.22 或更高版本和系统 `ssh`；只检查并提示，不自动安装软件或调用 `sudo`。
- 脚本默认附加 `--open-browser`，原样透传用户参数，并以前台进程运行。
- 脚本必须根据自身路径进入仓库根目录，从任意当前目录调用都能找到 Go 入口。
- 缺少 `xdg-open` 只输出提示，不阻止 Web 服务启动；用户仍可打开标准输出中的带令牌网址。

### 4. Validation & Error Matrix

| 条件 | 行为 |
|---|---|
| 未找到 `go` | 启动前失败，提示安装 Go 1.22+ |
| Go 低于 1.22 或版本无法识别 | 启动前失败并显示当前版本 |
| 未找到 `ssh` | 启动前失败，提示安装 OpenSSH 客户端 |
| 未找到 `xdg-open` | 警告后继续启动 |
| 默认或指定端口已被占用 | 保留 Go 程序原有监听错误并退出，不自动换端口 |

### 5. Good/Base/Bad Cases

- Good：在其他目录执行脚本绝对路径并传入 `--addr 127.0.0.1:8766`，程序从正确仓库启动并打开浏览器。
- Base：在仓库根目录执行 `./start.sh`，默认监听 `127.0.0.1:8765`，按 Ctrl+C 退出。
- Bad：脚本自动调用包管理器、`sudo`、切换到后台，或在端口冲突时静默启动第二个实例。

### 6. Tests Required

- `sh -n start.sh`：检查 POSIX shell 语法。
- 从仓库外执行 `<repo>/start.sh --version`：断言输出 `dev` 且退出码为 0。
- 修改脚本依赖或参数透传时，覆盖 Go 版本过低、缺少 `ssh`、缺少 `xdg-open` 和自定义 `--addr` 场景。

### 7. Wrong vs Correct

Wrong：`sudo apt install ... && go run ... &`，这会擅自修改系统并丢失前台生命周期。

Correct：`exec go run ./cmd/ssh-tunnel-manager --open-browser "$@"`，依赖由用户安装，信号和参数保持原有语义。

## 构建

    go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager

常规构建产物是单个 Go 可执行文件。发布使用 `./scripts/build-release.sh <version>` 生成 Linux amd64/arm64 压缩包和 `dist/checksums.txt`；详细契约见 release-delivery.md。

## 测试与静态检查

    gofmt -w ./cmd ./internal
    go test -race ./...
    go vet ./...
    go build ./cmd/ssh-tunnel-manager
    ./scripts/test-packaging.sh
    ./scripts/test-release.sh

测试与被测 Go 包放在同一目录。连接管理、端口刷新和服务关闭包含并发状态，完整验证必须使用 `go test -race ./...`。

## 部署与数据目录

- 发布包使用 `packaging/install.sh` 安装到当前用户 HOME 下的 XDG 命令和桌面目录，不需要 sudo 或服务器端 Agent；不创建 systemd 服务。
- Web 服务和本地转发端口只监听回环地址，不应通过反向代理、容器端口映射或防火墙规则暴露到局域网/公网。
- 非敏感自动刷新偏好写入 XDG_CONFIG_HOME（未设置时使用 ~/.config）下的 ssh-tunnel-manager/config.json；应用目录权限为 `0700`，文件权限为 `0600`。
- 隧道事件和脱敏诊断只在当前进程内存中有界保留，不写入 XDG_STATE_HOME；停止隧道或退出程序后清除。
- 密码和私钥口令只进入 Linux Secret Service / GNOME Keyring；不得随部署产物或配置文件复制。
- 显式断开 Host 时先停止该 Host 的隧道，再停止自动刷新，最后断开 ControlMaster。程序退出时依次关闭隧道管理器、端口发现服务、SSH 管理器和 HTTP 服务；浏览器页面生命周期不参与服务生命周期。
