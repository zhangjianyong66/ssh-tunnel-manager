# SSH 隧道管理器

SSH 隧道管理器是一个运行在本地的 Go 单文件程序。它启动一个仅限本机访问的 Web 控制台，用于读取系统 SSH 配置、查看远程 Linux 服务器正在监听的 TCP 端口，并将选中的远程端口通过 SSH 隧道代理到本地。

## 当前状态

项目已完成 MVP 的远程端口发现阶段。目前已实现：

- Go 单文件程序入口；
- 仅监听 `127.0.0.1` 的本地 Web 服务；
- 启动时生成随机访问令牌，并通过 HttpOnly Cookie 保护控制台；
- `/healthz` 健康检查；
- 递归读取 `~/.ssh/config` 的 `Include`，只展示显式 Host 别名；
- 通过系统 OpenSSH 建立每台服务器独立的 ControlMaster 主连接；
- 手动连接、断开、状态查询和脱敏诊断 API；
- 通过 `SSH_ASKPASS` 文件描述符通道处理密码和私钥口令；
- 通过 Linux Secret Service D-Bus 保存用户明确选择持久化的凭据；
- 程序退出时有界清理本程序创建的 SSH 子进程和运行目录；
- 通过已有 ControlMaster 执行并解析远程 `ss -ltnp`，必要时退化为 `ss -ltn`；
- 在控制台查看 TCP 监听端口和可见进程名，支持手动刷新；
- 按服务器启用默认关闭的 10 秒自动刷新，失败时保留最后一次成功结果；
- 产品、架构和路线文档。

端口转发和自动重连将在后续迭代实现，具体边界见 [docs/product-design.md](docs/product-design.md)。远程服务器需要提供 `ss` 命令。

## 开发运行

需要 Go 1.22 或更高版本和系统 OpenSSH。保存密码或私钥口令还需要可用的 Secret Service/GNOME Keyring 会话：

```bash
go run ./cmd/ssh-tunnel-manager
```

程序会输出带访问令牌的本地 URL。使用浏览器打开该 URL，按 `Ctrl+C` 停止服务。

构建：

```bash
go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager
```

## 目录结构

```text
cmd/ssh-tunnel-manager/  可执行程序入口
internal/credential/     Secret Service 凭据接口与适配器
internal/portdiscovery/  远程 TCP 监听端口解析与刷新状态
internal/ssh/            OpenSSH ControlMaster 连接管理
internal/sshconfig/      SSH 配置与 Include 解析
internal/web/            本地控制台页面和 M1/M2 API
docs/                    产品与技术文档
go.mod                   Go 模块定义
```

## 安全边界

- Web 控制台和本地代理端口默认只绑定 `127.0.0.1`。
- 不开放公网或局域网访问。
- SSH 凭据不写入普通配置文件，持久化方案使用 Linux Secret Service / GNOME Keyring。
- 系统 `~/.ssh/config` 只读，不由程序直接改写。
- 未知或变化的主机指纹严格交给系统 OpenSSH 校验，不自动接受或写入 `known_hosts`。
- 密码和口令不进入命令参数、环境变量、普通配置、日志或 HTTP 响应。

## 许可证

本项目使用 MIT License，详见 [LICENSE](LICENSE)。
