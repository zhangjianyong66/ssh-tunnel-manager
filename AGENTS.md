# 项目协作说明

## 项目定位

- 项目名称：SSH 隧道管理器。
- 目标：通过本地 Web 控制台管理系统 OpenSSH 连接、远程 TCP 监听端口和本地端口转发。
- 第一版面向 Ubuntu/Linux 本地环境和常见 Linux 目标服务器。
- 浏览器只访问本地控制台，不承担 SSH 或 TCP 隧道能力。

## 技术约定

- 使用 Go 1.22 或更高版本构建单文件可执行程序。
- 底层连接调用系统 `ssh`，复用 `~/.ssh/config`、`ssh-agent`、`known_hosts`、`ProxyJump` 等能力。
- Web 服务和本地转发端口只监听 `127.0.0.1`。
- 远程端口探测优先执行 `ss -ltnp`，权限不足时退化为 `ss -ltn`。
- 远程端口自动刷新按服务器启用，固定间隔 10 秒且默认关闭；失败保留上次成功结果。
- 默认远程目标地址为 `127.0.0.1`；第一版不要求用户处理复杂监听地址。
- 程序退出或用户显式停止时关闭隧道；浏览器页面关闭不影响程序和隧道。

## 配置与秘密

- 只读系统 `~/.ssh/config`，不直接修改该文件。
- 普通偏好使用 XDG 配置目录保存。
- 密码和私钥口令使用 Linux Secret Service / GNOME Keyring 持久化，禁止明文落盘和写日志。
- 控制台只允许本机访问，并使用进程启动时生成的随机令牌。

## 验证约定

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

## 当前目录与运行约定

- cmd/ssh-tunnel-manager/main.go 是当前唯一可执行入口；internal/credential 负责 Secret Service，internal/ssh 负责 OpenSSH ControlMaster 生命周期和受控远程命令，internal/portdiscovery 负责 `ss` 解析、端口快照与自动刷新，internal/sshconfig 负责 SSH 配置解析，internal/web 负责本地页面和 API；docs/ 保存产品、架构和路线文档。
- 本地开发使用 go run ./cmd/ssh-tunnel-manager，默认控制台地址为 127.0.0.1:8765；构建使用 go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager。
- 测试文件与对应 Go 包同目录；涉及并发连接、端口刷新或生命周期的变更必须运行 `go test -race ./...`，并继续运行 `go vet ./...` 和构建命令。
- 当前部署是直接运行单文件 Linux 可执行程序，没有安装脚本、systemd 单元或容器暴露配置；不得通过部署配置把回环服务暴露到局域网或公网。
- SSH 连接运行目录创建在 `${XDG_RUNTIME_DIR:-系统临时目录}/ssh-tunnel-manager-*`，权限为 `0700`，每个 Host 使用独立短 ControlPath；程序退出或显式断开时清理。
- 端口发现只复用已经连接的 ControlMaster；程序退出时先停止 `internal/portdiscovery` 的自动刷新和进行中探测，再关闭 SSH 会话。
- 凭据持久化通过 Go D-Bus 客户端访问 Linux Secret Service/GNOME Keyring；没有可用会话时不得降级为明文存储。
- 源码公开仓库为 https://github.com/zhangjianyong66/ssh-tunnel-manager.git，默认分支为 master，使用 GitHub CLI 登录账号通过 HTTPS 推送。

所有计划、设计和提交说明使用中文。新增可复用的运行方式、目录结构或部署约定时，及时更新本文件。
<!-- TRELLIS:START -->
# Trellis Instructions

These instructions are for AI assistants working in this project.

This project is managed by Trellis. The working knowledge you need lives under `.trellis/`:

- `.trellis/workflow.md` — development phases, when to create tasks, skill routing
- `.trellis/spec/` — package- and layer-scoped coding guidelines (read before writing code in a given layer)
- `.trellis/workspace/` — per-developer journals and session traces
- `.trellis/tasks/` — active and archived tasks (PRDs, research, jsonl context)

If a Trellis command is available on your platform (e.g. `/trellis:finish-work`, `/trellis:continue`), prefer it over manual steps. Not every platform exposes every command.

If you're using Codex or another agent-capable tool, additional project-scoped helpers may live in:
- `.agents/skills/` — reusable Trellis skills
- `.codex/agents/` — optional custom subagents

Managed by Trellis. Edits outside this block are preserved; edits inside may be overwritten by a future `trellis update`.

<!-- TRELLIS:END -->
