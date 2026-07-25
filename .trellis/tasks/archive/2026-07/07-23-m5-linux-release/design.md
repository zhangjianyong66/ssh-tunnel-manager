# M5：Linux 发布与交付技术设计

## 范围与边界

M5 只交付 Linux amd64/arm64 用户级发行包，不引入系统级安装、systemd、容器、包管理器仓库或其他操作系统适配。应用继续由当前单一 Go 入口启动，所有 Web 和转发监听仍受现有回环地址校验保护。

## 程序启动契约

- 入口增加构建时注入的版本变量和 `--version` 参数；开发构建返回 `dev`，正式包通过 `-ldflags` 注入标签版本。
- 入口增加默认关闭的 `--open-browser` 参数。服务必须先成功绑定监听地址，再输出带令牌 URL 并尝试调用 Linux `xdg-open`。
- 浏览器启动失败只在日志中记录不含秘密的错误，不中止已启动的服务；可手动访问 URL 仍只写标准输出，不进入日志。
- 普通命令行运行不自动打开浏览器；桌面入口显式传入 `--open-browser`。
- 外层已认证 HTTP 路由提供 `POST /api/shutdown`。处理器先返回 `202 Accepted`，再触发入口已有的停止上下文；资源仍由 `shutdownRuntime` 按既有顺序清理。
- 控制台提供带确认步骤的“退出程序”命令。未认证请求不能触发停止，页面关闭仍不影响程序生命周期。

同步监听是安全边界的一部分：如果默认端口已被其他本地进程占用，程序不得先把新令牌 URL 交给浏览器再发现监听失败。

## 发布包契约

`scripts/build-release.sh <version>` 是本地与 GitHub Actions 共用的唯一构建入口。脚本只接受安全的版本字符串，使用 `CGO_ENABLED=0`、`GOOS=linux` 分别构建 `amd64` 和 `arm64`，并使用 `-trimpath` 与版本链接参数。

每个压缩包使用目录 `ssh-tunnel-manager_<version>_linux_<arch>/`，包含：

- `ssh-tunnel-manager` 可执行文件；
- `install.sh` 与 `uninstall.sh`；
- `ssh-tunnel-manager.desktop` 桌面入口模板；
- `README.md` 与 `LICENSE`。

输出目录为仓库根目录 `dist/`，同时生成覆盖两个压缩包的 `checksums.txt`。构建使用临时目录，失败时清理临时内容，不删除 `dist/` 中无关文件。

## 安装与卸载

安装脚本只操作当前用户目录：

- 可执行文件：`${XDG_BIN_HOME:-$HOME/.local/bin}/ssh-tunnel-manager`；
- 卸载命令：同目录下的 `ssh-tunnel-manager-uninstall`；
- 桌面入口：`${XDG_DATA_HOME:-$HOME/.local/share}/applications/ssh-tunnel-manager.desktop`。

安装脚本从已解压发布目录复制文件，使用临时文件加重命名完成覆盖升级，并将桌面入口中的执行路径替换为绝对路径。可执行文件和卸载命令权限为 `0755`，桌面入口权限为 `0644`。

安装和卸载在操作文件前使用 `realpath -m` 规范化 HOME 与 XDG 路径，拒绝 HOME 根目录、相对路径、路径穿越和解析后位于 HOME 外的目标，确保脚本不能退化为系统级安装。

卸载脚本只删除上述三个本项目拥有的交付文件，并尽力刷新桌面入口缓存。它不得删除 `${XDG_CONFIG_HOME:-$HOME/.config}/ssh-tunnel-manager`、`~/.ssh`、Secret Service 凭据或运行时目录。重复安装和重复卸载都必须有确定结果。

## 自动发布流程

`.github/workflows/release.yml` 只响应 `v*` 标签。工作流使用最小的 `contents: write` 权限，按以下顺序执行：

1. 检出代码并安装与 `go.mod` 匹配的 Go 工具链；
2. 运行 `go test -race ./...`、`go vet ./...`、安装回归和发布构建；
3. 验证版本标签与程序版本一致，并保留两个架构压缩包及校验文件；
4. 只有前述步骤全部成功，才使用 GitHub CLI 创建对应 Release 并上传产物。

普通分支推送和拉取请求不创建 Release。本任务只提交工作流，不创建标签、不发布外部 Release。

## 测试与安全回归

- Go 单元测试验证版本输出、浏览器命令选择、监听失败时不会触发浏览器，以及退出接口的认证和触发语义。
- Shell 回归测试在临时 HOME/XDG 目录中执行安装、覆盖和重复卸载，断言文件权限、桌面执行路径，并放置哨兵配置验证卸载不误删。
- 发布测试构建两个架构，使用 `file` 或 ELF 头验证目标架构，检查包内容和 SHA-256 校验。
- 现有认证、随机令牌、回环监听、SSH 参数数组、秘密存储和资源清理测试继续通过完整竞态测试。

## 兼容性与回滚

- 新参数均为可选，现有命令行启动和配置格式不变。
- 安装覆盖不迁移也不删除用户配置，因此回滚只需重新安装旧版本发布包。
- 自动发布异常时不修改本地安装；删除错误的 GitHub Release 或标签属于仓库维护操作，不由安装脚本执行。
