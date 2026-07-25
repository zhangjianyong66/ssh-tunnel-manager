# Linux 发布说明

## 发布产物

每个版本提供以下文件：

- `ssh-tunnel-manager_<版本>_linux_amd64.tar.gz`；
- `ssh-tunnel-manager_<版本>_linux_arm64.tar.gz`；
- `checksums.txt`，包含两个压缩包的 SHA-256。

压缩包包含程序、`install.sh`、`uninstall.sh`、桌面入口模板、README 和 MIT License。程序版本由构建参数注入，必须与 Git 标签一致。

## 本地验证

需要 Go 1.22 或更高版本，以及 Linux 环境中的 `tar`、`sha256sum` 和 `file`：

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
./scripts/test-packaging.sh
./scripts/test-release.sh v1.0.0
```

最后一个命令同时构建 Linux amd64/arm64，检查包内容、程序版本、ELF 架构和 SHA-256。生成文件位于 `dist/`，该目录不进入 Git。

也可以只构建发布包：

```bash
./scripts/build-release.sh v1.0.0
```

版本必须是 `v1.2.3`、`1.2.3` 或带安全预发布后缀的形式，例如 `v1.2.3-rc.1`。

## 标签发布

`.github/workflows/release.yml` 只响应 `v*` 标签。发布前应确认目标提交已合入默认分支，工作区干净，并在本地完成上一节的验证。

```bash
git tag -a v1.0.0 -m "release: 发布 v1.0.0"
git push origin v1.0.0
```

GitHub Actions 会重新执行格式检查、竞态测试、静态检查、常规构建、安装回归和双架构发布回归。全部成功后，它先创建草稿 Release 并上传三个文件，最后才转为正式 Release。失败重跑会先把已有 Release 改回草稿，再覆盖同一标签的发布文件，避免上传中断时留下半更新的正式发布。

推送标签和创建外部 Release 会改变远端状态，不属于普通本地构建步骤；执行前应单独确认版本号和目标提交。

## 安装回滚

安装器不修改配置格式，也不删除配置或凭据。回滚应用版本时，退出当前程序，解压旧版本发布包并再次执行：

```bash
./install.sh
```

如果标签或 Release 内容错误，先把 Release 保持为草稿或撤下错误 Release，再根据仓库发布策略处理远端标签。不要通过安装脚本删除用户配置来完成回滚。

## 安全检查

- 工作流只授予 `contents: write`，检出和 Go 环境操作固定到官方 Action 的具体提交。
- 发布包不包含配置、日志、SSH 文件、访问令牌或凭据。
- 安装和卸载只操作当前用户的 XDG 目录，不使用 `sudo`，不创建 systemd 服务。
- 桌面入口传入 `--open-browser`，不改变只允许回环监听的地址校验。
