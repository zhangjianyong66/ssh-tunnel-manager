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
- 运行中的隧道意外退出后最多自动重连 5 次，退避为 1、2、4、8、16 秒；稳定运行满 60 秒后恢复重试额度，同一 Host 共享主连接恢复。
- 默认远程目标地址为 `127.0.0.1`；第一版不要求用户处理复杂监听地址。
- 程序退出或用户显式停止时关闭隧道；浏览器页面关闭不影响程序和隧道。

## 配置与秘密

- 只读系统 `~/.ssh/config`，不直接修改该文件。
- 端口自动刷新偏好保存到 `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/config.json`；页面管理的非敏感 SSH Host 保存到同目录 `hosts.json`。目录权限 `0700`、文件权限 `0600`，损坏文件不得被自动覆盖；启动时不得自动连接。
- 隧道事件和脱敏 SSH 诊断只在内存中有界保留，停止隧道或退出程序后清除，不写磁盘。
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

- cmd/ssh-tunnel-manager/main.go 是当前唯一可执行入口；internal/configfile 负责私有权限原子配置写入，internal/hostconfig 负责项目 Host 存储、系统/项目 Catalog 与受控 OpenSSH 配置渲染，internal/credential 负责 Secret Service，internal/ssh 负责 OpenSSH ControlMaster 生命周期和受控远程命令，internal/portdiscovery 负责 `ss` 解析、端口快照与自动刷新，internal/preference 负责 XDG 非敏感偏好，internal/tunnel 负责回环端口分配、自动重连、隧道状态和精确进程清理，internal/sshconfig 负责系统 SSH 配置与 `ssh -G` 有效配置解析，internal/web 负责本地页面和 API；docs/ 保存产品、架构和路线文档。
- 本地开发可在仓库根目录执行 `./start.sh` 一键检查 Go 1.22+、OpenSSH 和 `xdg-open`，并以前台模式运行 `go run ./cmd/ssh-tunnel-manager --open-browser`；脚本支持透传 `--addr` 等参数且可从其他目录调用。也可直接使用 `go run ./cmd/ssh-tunnel-manager`，默认控制台地址为 127.0.0.1:8765；构建使用 `go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager`。
- 测试文件与对应 Go 包同目录；涉及并发连接、端口刷新或生命周期的变更必须运行 `go test -race ./...`，并继续运行 `go vet ./...` 和构建命令。
- 当前部署仍是直接运行单文件 Linux 可执行程序，M5 提供用户级安装脚本和桌面入口，但不提供 systemd 单元或容器暴露配置；不得通过部署配置把回环服务暴露到局域网或公网。
- Linux 发布由 `scripts/build-release.sh <version>` 统一构建 amd64/arm64 压缩包与 `dist/checksums.txt`；`scripts/test-release.sh` 验证版本、包内容、SHA-256 和 ELF 架构，`dist/` 不进入 Git。
- 发布包通过 `packaging/install.sh` 无 `sudo` 安装到 `${XDG_BIN_HOME:-~/.local/bin}`，桌面入口安装到 `${XDG_DATA_HOME:-~/.local/share}/applications`；卸载保留 SSH 配置、自动刷新偏好和 Secret Service 凭据，不提供系统级安装或 systemd 服务。
- `.github/workflows/release.yml` 只在 `v*` 标签上执行完整质量门禁和 Linux 自动发布；本地开发不得在未获明确授权时创建或推送版本标签、发布 GitHub Release。
- 桌面入口使用 `--open-browser` 自动打开控制台，浏览器失败不终止服务；隐藏终端启动时通过控制台右上角的认证退出操作触发原有优雅关闭，关闭浏览器页面本身仍不停止程序。
- SSH 连接运行目录创建在 `${XDG_RUNTIME_DIR:-系统临时目录}/ssh-tunnel-manager-*`，权限为 `0700`，每个 Host 使用独立短 ControlPath；程序退出或显式断开时清理。
- 项目 Host 连接会在上述运行目录内冻结每会话 `ssh_config`（文件权限 `0600`），主连接、检查、远程命令、转发和退出统一携带同一 `-F`；一层跳板先建立独立 ControlMaster，目标配置通过 `ProxyJump` 固定复用跳板 ControlPath。
- 显式断开跳板前必须检查已连接目标依赖并返回 `host_in_use`；程序退出按目标到跳板的依赖顺序清理。未知主机指纹只能通过阶段化确认继续，变化指纹永久拒绝。
- 端口发现和本地转发只复用已经连接的 ControlMaster；断开 Host 时先停止该 Host 的隧道，再停止自动刷新，最后断开 SSH。程序退出时依次关闭 `internal/tunnel`、`internal/portdiscovery`、SSH 会话和 HTTP 服务。
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
