# SSH 隧道管理器

SSH 隧道管理器是一个运行在本地的 Go 单文件程序。它启动一个仅限本机访问的 Web 控制台，用于读取系统 SSH 配置、查看远程 Linux 服务器正在监听的 TCP 端口，并将选中的远程端口通过 SSH 隧道代理到本地。

## 当前状态

项目已完成 Linux MVP。目前已实现：

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
- 复用既有 ControlMaster 创建仅监听 `127.0.0.1` 的本地端口转发；
- 本地端口同号优先，冲突时自动分配其他回环端口；
- 同时管理多台服务器和多个远程端口，并对重复创建保持幂等；
- 在控制台查看、复制、打开、重试和精确停止隧道；
- 断开服务器或退出程序时按资源依赖顺序有界清理隧道；
- 隧道意外退出后执行 1、2、4、8、16 秒的有界自动重连，同一 Host 共享一次主连接恢复；
- 控制台显示连续运行时长、累计重连次数、等待重连和需要人工处理状态；
- 每条隧道在内存中保留最多 100 条、总计不超过 64 KiB 的脱敏近期日志；
- 按 Host 将端口自动刷新开关保存到 XDG 配置目录，程序重启后仍只在用户手动连接时恢复；
- Linux amd64/arm64 可校验发布包、无 `sudo` 用户级安装和桌面入口；
- 桌面入口自动打开浏览器，控制台可触发程序优雅退出；
- 产品、架构和路线文档。

程序不会在重启后自动连接服务器或恢复隧道，诊断日志也不会写入磁盘。远程服务器需要提供 `ss` 命令。

## Linux 安装

运行依赖：系统 OpenSSH。桌面入口自动打开浏览器还需要 `xdg-open`（Ubuntu 的 `xdg-utils` 包）；保存密码或私钥口令需要可用的 Secret Service/GNOME Keyring 会话。

从 GitHub Release 下载当前机器架构对应的压缩包和 `checksums.txt`。例如 amd64：

```bash
grep 'linux_amd64.tar.gz$' checksums.txt | sha256sum -c -
tar -xzf ssh-tunnel-manager_1.0.0_linux_amd64.tar.gz
cd ssh-tunnel-manager_1.0.0_linux_amd64
./install.sh
```

arm64 用户把命令中的 `amd64` 替换为 `arm64`。安装器不需要 `sudo`，默认写入：

- 命令：`~/.local/bin/ssh-tunnel-manager`；
- 卸载命令：`~/.local/bin/ssh-tunnel-manager-uninstall`；
- 桌面入口：`~/.local/share/applications/ssh-tunnel-manager.desktop`。

如果设置了 `XDG_BIN_HOME` 或 `XDG_DATA_HOME`，安装器会使用对应路径；解析后的路径必须位于当前用户 HOME 下。请确认命令目录已加入 `PATH`；即使未加入，也可从桌面应用列表启动。

升级时解压新版本并再次运行 `./install.sh`。退出程序后运行以下命令即可卸载：

```bash
ssh-tunnel-manager-uninstall
```

卸载不会删除 `~/.ssh`、自动刷新偏好或系统密钥环中的凭据。使用自定义 XDG 目录时，卸载命令应在相同 XDG 环境下运行。

## 运行

从桌面应用列表打开“SSH 隧道管理器”会自动启动默认浏览器。控制台右上角的“退出程序”会停止全部隧道并关闭程序；仅关闭浏览器页面不会停止程序。

命令行运行：

```bash
ssh-tunnel-manager
```

程序会输出带访问令牌的本地 URL。使用浏览器打开该 URL，按 `Ctrl+C` 停止服务。需要显式自动打开浏览器时使用 `ssh-tunnel-manager --open-browser`，查看版本使用 `ssh-tunnel-manager --version`。

## 开发运行

需要 Go 1.22 或更高版本和系统 OpenSSH：

```bash
go run ./cmd/ssh-tunnel-manager
```

构建：

```bash
go build -o ssh-tunnel-manager ./cmd/ssh-tunnel-manager
```

完整验证：

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
./scripts/test-packaging.sh
./scripts/test-release.sh
```

维护者发布流程见 [docs/release.md](docs/release.md)。

## 目录结构

```text
cmd/ssh-tunnel-manager/  可执行程序入口
internal/credential/     Secret Service 凭据接口与适配器
internal/portdiscovery/  远程 TCP 监听端口解析与刷新状态
internal/preference/     XDG 非敏感偏好设置
internal/ssh/            OpenSSH ControlMaster 连接管理
internal/sshconfig/      SSH 配置与 Include 解析
internal/tunnel/         本地端口分配、自动重连与隧道生命周期
internal/web/            本地控制台页面和 MVP API
packaging/               用户级安装、卸载和桌面入口模板
scripts/                 发布构建与打包回归脚本
docs/                    产品与技术文档
.github/workflows/       标签触发的 Linux 自动发布流程
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
