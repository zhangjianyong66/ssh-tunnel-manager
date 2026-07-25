# Linux 发布与桌面交付契约

## Scenario: M5 用户级发布、安装和桌面生命周期

### 1. Scope / Trigger

- 触发范围：修改程序启动参数、HTTP 监听启动顺序、控制台退出能力、`packaging/`、`scripts/`、`.github/workflows/release.yml` 或发布文档。
- 目标：同一套本地脚本和 GitHub Actions 生成可验证的 Linux amd64/arm64 发布包；安装始终限制在当前用户 HOME 下，桌面启动后仍能安全退出程序。
- 不包含：系统级安装、systemd、容器暴露、包管理器仓库及 macOS/Windows 发布。

### 2. Signatures

- 程序：`ssh-tunnel-manager [--addr <loopback-host:port>] [--open-browser] [--version]`。
- 退出 API：`POST /api/shutdown`，必须经过入口随机令牌 Cookie 或查询令牌认证；成功返回 `202 Accepted`。
- 发布构建：`./scripts/build-release.sh <version>`。
- 发布回归：`./scripts/test-release.sh [version]`。
- 安装回归：`./scripts/test-packaging.sh`。
- 发布包安装：解压后执行 `./install.sh`；安装后卸载命令为 `ssh-tunnel-manager-uninstall`。

### 3. Contracts

- `--version` 的开发值为 `dev`；正式构建通过 `-ldflags "-X main.version=<tag>"` 注入版本。`--open-browser` 默认关闭，只调用参数数组形式的 `xdg-open <token-url>`。
- HTTP 启动顺序固定为：校验回环地址 → 同步 `net.Listen` → 启动 `http.Server.Serve` → 标准输出带令牌 URL → 可选打开浏览器。监听失败不得把 URL 交给浏览器。
- 浏览器启动失败只记录不含 URL/令牌的错误，服务继续运行。控制台“退出程序”调用退出 API，入口停止上下文再复用 `shutdownRuntime` 的隧道 → 端口发现 → SSH → HTTP 清理顺序。
- 版本只接受 `v1.2.3`、`1.2.3` 或 `v1.2.3-rc.1` 同类安全字符格式。构建固定使用 `CGO_ENABLED=0 GOOS=linux` 和 `GOARCH=amd64|arm64`。
- 每个 `ssh-tunnel-manager_<version>_linux_<arch>.tar.gz` 只包含顶层同名目录及程序、`install.sh`、`uninstall.sh`、桌面模板、README、LICENSE；`dist/checksums.txt` 恰有两个 SHA-256 条目。
- 安装环境键为 `HOME`、可选 `XDG_BIN_HOME`、可选 `XDG_DATA_HOME`。路径经 `realpath -m` 后必须位于非根目录 HOME 下。默认程序目录为 `$HOME/.local/bin`，默认桌面目录为 `$HOME/.local/share/applications`。
- 安装使用同目录临时文件和重命名覆盖，程序/卸载命令权限 `0755`，桌面入口权限 `0644`。卸载只删除这三个交付文件，不删除 XDG 配置、`~/.ssh`、Secret Service 凭据或其他目录内容。
- `.github/workflows/release.yml` 只匹配 `v*` 标签，权限为 `contents: write`。完整质量门禁成功后先创建草稿并上传两个压缩包和校验文件，最后才转正式 Release；重跑已有 Release 时也必须先改回草稿再覆盖文件。本地任务不得擅自创建或推送标签。

### 4. Validation & Error Matrix

| 条件 | 必须行为 |
|---|---|
| `--addr` 非回环或端口无效 | 启动前拒绝，不创建监听或打开浏览器 |
| 回环端口已占用 | 返回监听错误，不输出/打开新的令牌 URL |
| `xdg-open` 不存在或启动失败 | 记录脱敏错误，HTTP 服务继续运行 |
| 未认证 `POST /api/shutdown` | `401 Unauthorized`，不得触发停止上下文 |
| 版本含空格、斜杠或非法预发布后缀 | 构建脚本退出码 2，不创建该版本产物 |
| HOME 为 `/`，或 XDG 路径解析到 HOME 外 | 安装/卸载拒绝，不写入或删除目标文件 |
| 发布包缺文件、版本不匹配、架构错误或校验失败 | `test-release.sh` 失败，GitHub Release 步骤不得执行 |
| 浏览器页面关闭 | 不改变程序、SSH 连接或隧道生命周期 |

### 5. Good/Base/Bad Cases

- Good：`v1.0.0` 标签通过竞态测试和脚本回归，生成两个架构包与校验文件，草稿上传完整后转正式 Release。
- Good：用户使用含空格的 HOME 内 XDG 路径重复安装，新版本原子覆盖；卸载后配置哨兵仍存在。
- Base：命令行不传 `--open-browser`，只在标准输出显示 URL，用户用 `Ctrl+C` 退出。
- Base：桌面调用 `--open-browser`，用户从已认证页面点击退出，全部资源按既有顺序清理。
- Bad：工作流自己拼另一套 `go build`/打包逻辑、安装器接受 `/usr/local/bin`、卸载递归删除配置目录、监听失败后仍打开令牌 URL，或把 URL 写入日志。

### 6. Tests Required

- Go：`go test -race ./...`；断言参数默认值、浏览器成功/失败、先监听后打开、未认证退出不生效、认证退出返回 202、页面包含退出契约。
- 静态与常规构建：`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`。
- 安装：`./scripts/test-packaging.sh`；断言权限、绝对桌面 Exec、重复覆盖、重复卸载、配置保留，以及相对/HOME 外 XDG 路径被拒绝。
- 发布：`./scripts/test-release.sh <version>`；断言精确包清单、两个校验条目、amd64 可执行版本和两个 ELF 架构。
- 工作流与 Shell：`actionlint .github/workflows/release.yml` 和 `shfmt -d`；同时检查官方 Action 固定提交、标签触发、最小权限及草稿后发布顺序。
- 界面：至少检查桌面与窄屏截图，确认退出按钮、标题、表格和文字不重叠，页面内容非空。

### 7. Wrong vs Correct

#### Wrong

```sh
sudo install ssh-tunnel-manager /usr/local/bin/
rm -rf "$HOME/.config/ssh-tunnel-manager"
```

这会把用户级交付升级为系统级权限，并在卸载/回滚时破坏非敏感偏好。

#### Correct

```sh
./install.sh
./scripts/test-packaging.sh
./scripts/test-release.sh v1.0.0
```

安装脚本自行验证 HOME/XDG 边界；本地和自动发布复用同一构建脚本及回归契约。
