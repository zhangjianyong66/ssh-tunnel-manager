# 发布 GitHub 开源仓库

## 目标

将当前 SSH 隧道管理器项目发布为 GitHub 公开仓库，保留本地提交历史，配置可复用的 origin 远程，并验证公开仓库和远程分支可访问。

## 已确认事实

- 本地仓库路径为 /home/zhangjianyong/project/ssh-tunnel-manager。
- 当前分支为 master，工作区在发布前应保持干净。
- 最近的项目初始化提交为 692de71 chore: 初始化项目骨架与开发配置。
- go.mod 的模块路径为 github.com/zhangjianyong66/ssh-tunnel-manager。
- GitHub CLI 已登录账号 zhangjianyong66，Git 操作协议为 HTTPS。
- GitHub 上 zhangjianyong66/ssh-tunnel-manager 当前不存在，本地没有配置远程。
- 项目 README、产品/架构/路线文档和 Trellis 规范已在本地提交。

## 要求

1. 创建 GitHub 用户 zhangjianyong66 下名为 ssh-tunnel-manager 的公开仓库。
2. 将本地仓库 origin 设置为该 GitHub 仓库的 HTTPS 地址。
3. 推送 master 分支及当前完整提交历史。
4. 保留 README.md 作为仓库首页说明，不修改业务代码。
5. 发布前扫描待推送文件，禁止提交私钥、访问令牌、密码、密钥环数据或本机运行时秘密。
6. 在仓库公开可访问后验证远程 URL、可见性、默认分支和远程提交。

## 许可证决策

用户已确认采用 MIT License。仓库应新增标准 MIT LICENSE 文件，版权年份使用 2026，版权主体使用 GitHub 账号 zhangjianyong66。

## 验收标准

- [x] GitHub 公开仓库 zhangjianyong66/ssh-tunnel-manager 创建成功。
- [x] origin 指向该仓库，协议为 HTTPS。
- [x] master 分支推送成功，远程包含提交 692de71。
- [x] GitHub API/CLI 显示仓库 visibility 为 PUBLIC。
- [x] README 可作为仓库首页展示，且敏感信息扫描通过。
- [x] 许可证决策已记录，并添加 MIT LICENSE 文件。

## 不在范围内

- 不修改 SSH 隧道业务实现。
- 不新增 CI、Release、容器镜像、安装包或 GitHub Actions，除非另行提出。
- 不自动配置分支保护、协作者、Issue 模板或项目看板。
