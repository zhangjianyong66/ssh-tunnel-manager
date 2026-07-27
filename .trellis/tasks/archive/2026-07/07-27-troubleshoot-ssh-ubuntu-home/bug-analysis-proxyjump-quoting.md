# Bug Analysis: ProxyJump 引号被 OpenSSH 保留

## 1. Root Cause Category

- **Category**: D - Test Coverage Gap；E - Implicit Assumption。
- **Specific Cause**: 渲染器假设所有 OpenSSH 选项值都可使用同一个双引号编码函数。字符串单元测试只验证了输出文本，没有验证 `ssh -G` 的有效语义；OpenSSH 9.6 对 `ProxyJump` 保留了引号。

## 2. Why Fixes Failed

1. 初始实现：通用引用函数对路径是正确的，但把同一规则扩展到具有专用 Alias 语法的 `ProxyJump`，属于错误的跨语法抽象。
2. 初始测试：只使用 `strings.Contains` 检查生成文本，无法区分“看起来合法”和“OpenSSH 实际解析正确”。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | `ProxyJump` 只接受 `ValidateAlias` 后的原子值，单独无引号输出 | DONE |
| P0 | Test Coverage | 渲染结果写入临时文件并由真实 `ssh -G` 解析 | DONE |
| P1 | Documentation | 在 managed-host-configuration.md 写入 Wrong vs Correct 契约 | DONE |
| P1 | Code Review | 修改渲染器时区分路径值、普通值和专用语法值 | DONE |

## 4. Systematic Expansion

- **Similar Issues**: `ProxyCommand`、`Match`、逗号分隔跳板链等专用语法不能套用普通字符串编码；当前范围明确不生成这些值。
- **Design Improvement**: 渲染器按字段语法选择编码方法，不暴露接收任意指令名和值的通用 API。
- **Process Improvement**: 外部工具配置的测试必须至少有一条通过真实工具的解析/检查模式，而不只检查字符串。

## 5. Knowledge Capture

- [x] 新增真实 `ssh -G` 渲染集成测试。
- [x] 更新后端 Host 配置代码规范。
- [x] 将错误根因与预防措施保存在当前任务中。
