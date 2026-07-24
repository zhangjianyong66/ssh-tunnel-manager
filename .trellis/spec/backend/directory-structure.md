# 目录结构

本项目是单仓库 Go 程序，当前没有数据库层、前端工程或多模块 workspace。新增代码应保持单进程、标准库优先的边界。

## 当前布局

    cmd/ssh-tunnel-manager/main.go  可执行程序入口、HTTP 路由和 MVP 页面
    internal/credential/            Secret Service 凭据接口和 D-Bus 适配器
    internal/ssh/                   OpenSSH ControlMaster、askpass 和连接状态
    internal/sshconfig/             OpenSSH 配置、Include 和显式 Host 解析
    internal/web/                   本地控制台页面和 M1 HTTP API
    docs/                           产品、架构和开发路线文档
    go.mod                          Go 模块定义（Go 1.22）
    .trellis/spec/backend/          后端代码规范

## 放置规则

- 可执行程序只放在 cmd/<program>/，入口包使用 package main。当前入口为 cmd/ssh-tunnel-manager/main.go。
- 可复用但不希望被外部模块导入的实现放在 internal/<包名>/；不要把业务代码塞回 cmd 入口文件。
- 产品边界、架构决策和路线图放在 docs/，不在代码注释中复制整篇设计文档。
- 测试文件与被测 Go 包放在同一目录，使用 <name>_test.go；当前仓库尚未有测试文件。
- 不创建数据库目录、迁移目录或 ORM 层，除非未来需求明确引入持久化实现。

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
- 不要因为路线图提到数据库就预先添加 ORM、迁移或 SQLite 代码；当前偏好配置和凭据存储尚未实现。
