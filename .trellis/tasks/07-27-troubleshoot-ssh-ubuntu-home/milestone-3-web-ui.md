# 里程碑 3：Web 控制台

## 目标

提供完整的项目 Host 管理和分阶段连接交互，保持当前本地运维控制台的紧凑布局与既有端口、隧道工作流。

## 任务

- [ ] M3-T1：调整服务器表格，展示来源、目标和跳板；增加项目 Host 的新增、编辑、删除入口及稳定加载状态。
- [ ] M3-T2：实现结构化 Host 对话框、字段校验、单层跳板筛选、不可变 Alias 和删除凭据确认。
- [ ] M3-T3：把连接流程改为基于 `stageHost` 的凭据挑战与指纹确认状态机，覆盖取消、重试、密钥变化和引用失效提示。
- [ ] M3-T4：补充模板/API 契约测试，并在桌面与移动视口检查文本、表格、对话框和操作控件无重叠。

## 关键文件

- `internal/web/page.go`
- `internal/web/page_test.go`
- `internal/web/app.go`
- `internal/web/app_test.go`

## 验证

```bash
gofmt -w ./internal/web
go test -race ./internal/web
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

启动本地服务后检查新增、编辑、删除、两阶段凭据、两阶段指纹确认、错误状态和原有端口隧道操作。若环境具备浏览器自动化，再补充桌面/移动截图与控制台错误检查。

## 回滚点

后端 API 与 M1/M2 能力保持独立。若页面状态机不稳定，回滚页面接线，不删除已验证的 Store 和 SSH Manager 能力。
