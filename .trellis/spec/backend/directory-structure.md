# 目录结构

本项目是单仓库 Go 程序，当前没有数据库层、前端工程或多模块 workspace。新增代码应保持单进程、标准库优先的边界。

## 当前布局

    cmd/ssh-tunnel-manager/main.go  可执行程序入口、HTTP 路由和 MVP 页面
    internal/configfile/            XDG 私有权限原子配置写入
    internal/credential/            Secret Service 凭据接口和 D-Bus 适配器
    internal/hostconfig/            项目 Host 存储、合并 Catalog 和 OpenSSH 配置渲染
    internal/portdiscovery/         ss 输出解析、端口快照和自动刷新生命周期
    internal/preference/            XDG 非敏感自动刷新偏好
    internal/ssh/                   OpenSSH ControlMaster、askpass 和连接状态
    internal/sshconfig/             OpenSSH 配置、Include 和显式 Host 解析
    internal/tunnel/                回环端口分配、自动重连、隧道状态和精确进程清理
    internal/web/                   本地控制台页面和 MVP HTTP API
    packaging/                      用户级安装、卸载和桌面入口模板
    scripts/                        发布构建、安装回归和双架构发布回归
    docs/                           产品、架构和开发路线文档
    .github/workflows/              v* 标签触发的 Linux 发布流程
    go.mod                          Go 模块定义（Go 1.22）
    .trellis/spec/backend/          后端代码规范

## 放置规则

- 可执行程序只放在 cmd/<program>/，入口包使用 package main。当前入口为 cmd/ssh-tunnel-manager/main.go。
- 可复用但不希望被外部模块导入的实现放在 internal/<包名>/；不要把业务代码塞回 cmd 入口文件。
- 产品边界、架构决策和路线图放在 docs/，不在代码注释中复制整篇设计文档。
- 安装模板放在 packaging/；本地和 CI 共用的发布/交付检查放在 scripts/，不得在工作流中复制第二套打包逻辑。
- 测试文件与被测 Go 包放在同一目录，使用 <name>_test.go。
- 非敏感偏好使用 `internal/preference` 的版本化 JSON 文件；不创建数据库目录、迁移目录或 ORM 层，除非未来需求明确引入其他持久化实现。
- 项目管理的非敏感 SSH Host 使用 `internal/hostconfig` 的版本化 JSON；`internal/configfile` 只提供共享原子写入，不拥有业务 schema 或校验规则。

## 命名与边界

- Go 包目录使用小写短名；导出标识符按 Go 命名规则使用 MixedCaps，非导出标识符使用 lowerCamelCase。
- HTTP 处理、进程启动和全局模板等入口级代码可以暂留在 cmd/ssh-tunnel-manager/main.go；随着功能增长，应按职责拆到 internal/ 包。
- 包之间通过明确的类型和函数传递状态，不使用跨包可变全局变量；SSH 子进程句柄、隧道状态等资源必须由所属管理器持有。

## 参考实现

- cmd/ssh-tunnel-manager/main.go：命令行参数、回环地址校验、令牌生成、HTTP mux 和页面渲染。
- docs/architecture.md：目标模块边界和单进程模型。
- docs/product-design.md：后续 SSH、端口探测、隧道和凭据能力的职责划分。

## 常见错误

- 不要在仓库根目录新增第二个 main.go 或把可执行逻辑放入 internal/。
- 不要为自动刷新偏好引入 ORM、迁移或 SQLite；当前文件存储已经覆盖所需范围，凭据仍由 Secret Service 独立保存。
