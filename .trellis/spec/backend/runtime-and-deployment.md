# 运行、构建与部署

## 环境要求

- 目标环境是 Ubuntu/Linux 本地机器；需要 Go 1.22 或更高版本。
- SSH 能力由系统 OpenSSH 提供，后续实现复用用户的 ~/.ssh/config、ssh-agent、known_hosts 和 ProxyJump。
- 远程端口探测依赖目标 Linux 上的 ss 命令，优先 ss -ltnp，权限不足时退化为 ss -ltn。

## 本地运行

    go run ./cmd/ssh-tunnel-manager

默认监听 127.0.0.1:8765。可用 -addr 指定端口，但地址仍必须是 127.0.0.1:<port>、localhost:<port> 或 [::1]:<port>。程序启动时输出带一次性 token 的本地 URL，页面关闭不会停止进程或隧道，使用 Ctrl+C 退出。

## 构建

    go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager

构建产物是单个 Go 可执行文件。交付阶段按 docs/roadmap.md 目标构建 Linux amd64 / arm64，当前仓库没有安装脚本、systemd 单元或桌面入口。

## 测试与静态检查

    gofmt -w ./cmd ./internal
    go test -race ./...
    go vet ./...
    go build ./cmd/ssh-tunnel-manager

测试与被测 Go 包放在同一目录。连接管理、端口刷新和服务关闭包含并发状态，完整验证必须使用 `go test -race ./...`。

## 部署与数据目录

- 当前部署方式是将构建出的单文件程序放在用户可执行路径中直接运行，不需要服务器端 Agent。
- Web 服务和本地转发端口只监听回环地址，不应通过反向代理、容器端口映射或防火墙规则暴露到局域网/公网。
- 未来非敏感偏好写入 XDG_CONFIG_HOME（未设置时使用 ~/.config）下的 ssh-tunnel-manager/config.json；运行日志和诊断写入 XDG_STATE_HOME（未设置时使用 ~/.local/state）下的 ssh-tunnel-manager/。
- 密码和私钥口令只进入 Linux Secret Service / GNOME Keyring；不得随部署产物或配置文件复制。
- 程序退出或用户显式停止时关闭 SSH 子进程和监听端口；浏览器页面生命周期不参与服务生命周期。
