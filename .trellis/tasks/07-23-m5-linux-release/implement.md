# M5：Linux 发布与交付执行清单

## 1. 入口与测试

- [x] 在 `cmd/ssh-tunnel-manager` 增加构建版本、`--version` 和默认关闭的 `--open-browser` 参数。
- [x] 把 HTTP 监听改为先同步绑定、再输出/打开控制台 URL，保持回环地址校验和原有关闭顺序。
- [x] 增加单元测试，覆盖默认行为、浏览器启动成功/失败以及监听失败不打开浏览器。
- [x] 增加经过现有令牌认证的退出接口和控制台操作，复用原有优雅关闭顺序并补充接口/页面契约测试。

## 2. 发布与安装文件

- [x] 新增单一 `scripts/build-release.sh`，构建 Linux amd64/arm64 包并生成 `dist/checksums.txt`。
- [x] 新增用户级 `packaging/install.sh`、`packaging/uninstall.sh` 和桌面入口模板。
- [x] 新增临时 HOME/XDG 环境下的脚本回归测试，覆盖重复安装、覆盖升级、权限、绝对执行路径、重复卸载和配置保留。
- [x] 验证发布压缩包的文件集合、程序版本和 ELF 目标架构。

## 3. 自动发布与文档

- [x] 新增仅由 `v*` 标签触发的 `.github/workflows/release.yml`，质量检查成功后使用 GitHub CLI 创建 Release。
- [x] 更新 `README.md`，说明依赖、下载校验、安装、桌面启动、命令行启动、升级和卸载。
- [x] 新增 `docs/release.md`，记录维护者本地构建、标签发布、失败处理和回滚步骤。
- [x] M5 完成后更新 `docs/roadmap.md`、根 `AGENTS.md` 与 Trellis 运行/交付规范。

## 4. 质量门禁

- [x] `gofmt -w ./cmd ./internal`。
- [x] `go test -race ./...`。
- [x] `go vet ./...`。
- [x] `go build ./cmd/ssh-tunnel-manager`。
- [x] 运行安装/卸载脚本回归测试。
- [x] 使用发布脚本生成两个架构的干净产物并校验 SHA-256、包内容、版本和 ELF 架构。
- [x] 校验 GitHub Actions YAML 语法、触发条件、发布权限和先检查后发布顺序。

## 5. 收尾

- [x] 审查差异，不提交 `dist/` 或本地二进制，确认工作区不包含凭据和带令牌 URL。
- [x] 使用 `trellis-check` 完成全量规范与质量复核。
- [x] 提交 M5 功能变更，归档 M5 子任务并更新父 MVP 任务进度和开发 journal。

## 风险与回滚点

- 改动 HTTP 启动顺序后，若生命周期测试失败，先回退入口实现，不影响发布脚本的独立验证。
- 安装测试必须只使用测试创建的临时目录；不得对真实 HOME/XDG 路径执行安装或卸载。
- 发布脚本不得递归删除 `dist/`，只覆盖本次版本的已知文件和 `checksums.txt`。
- 本任务不执行 `git tag`、`git push` 或 `gh release create`，外部发布需用户另行明确授权。
