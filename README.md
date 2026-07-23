# SSH 隧道管理器

SSH 隧道管理器是一个运行在本地的 Go 单文件程序。它启动一个仅限本机访问的 Web 控制台，用于读取系统 SSH 配置、查看远程 Linux 服务器正在监听的 TCP 端口，并将选中的远程端口通过 SSH 隧道代理到本地。

## 当前状态

项目处于 MVP 骨架阶段。目前已实现：

- Go 单文件程序入口；
- 仅监听 `127.0.0.1` 的本地 Web 服务；
- 启动时生成随机访问令牌，并通过 HttpOnly Cookie 保护控制台；
- `/healthz` 健康检查；
- 产品、架构和路线文档。

SSH 配置读取、远程端口探测、凭据密钥环和端口转发将在后续迭代实现，具体边界见 [docs/product-design.md](docs/product-design.md)。

## 开发运行

需要 Go 1.22 或更高版本：

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
docs/                    产品与技术文档
go.mod                   Go 模块定义
```

## 安全边界

- Web 控制台和本地代理端口默认只绑定 `127.0.0.1`。
- 不开放公网或局域网访问。
- SSH 凭据不写入普通配置文件，持久化方案使用 Linux Secret Service / GNOME Keyring。
- 系统 `~/.ssh/config` 只读，不由程序直接改写。

## 许可证

本项目使用 MIT License，详见 [LICENSE](LICENSE)。
