# X-Panel-WH 社区派生版

基于 [xeefei/X-Panel](https://github.com/xeefei/X-Panel) 免费开源版本的小范围下游维护，保留原面板界面与主要功能。不是上游官方发行版或 Pro 产品，也不提供商业授权绕过。

[English](README.en.md) · [文档索引](docs/README.md) · [构建与安装边界](docs/build-install-upgrade.md) · [来源与许可证](docs/credits.md)

## 下游改动

- **独立 client monthly reset**：每个客户端可选开启、设置每月 1–31 日，默认关闭；按面板时区计算，短月取月末。只重置周期 Up/Down，保留 AllTime、额度和到期规则。详见[功能与安全边界](docs/downstream-monthly-reset.md)。
- **去首页自动广告**：删除首页自动弹出的 Pro 欢迎推广及其专属触发逻辑；不等于清除所有手动功能限制提示或改写上游全部界面。[改动说明](docs/home-promotion-removal.md)
- **删除硬编码凭据外报**：移除继承的第三方 Telegram 凭据及未经用户配置的外报路径，保留用户自行配置的通知。HEAD 清理不代表凭据已撤销、历史已清除或完成全仓安全审计。

## 当前状态：开发候选，未正式发布

| 分支 / PR | 用途 |
| --- | --- |
| `main` | 旧默认分支，版本文件为 `26.4.25`，**不含上述候选功能** |
| [`integration/upstream-v26.9.13`](https://github.com/luhaihui106/X-Panel-WH/tree/integration/upstream-v26.9.13) / [draft PR #1](https://github.com/luhaihui106/X-Panel-WH/pull/1) | 未改动的上游免费源码同步 |
| [`feat/client-monthly-reset`](https://github.com/luhaihui106/X-Panel-WH/tree/feat/client-monthly-reset) / [draft PR #2](https://github.com/luhaihui106/X-Panel-WH/pull/2) | 上游同步之上的下游功能候选 |
| [`docs/downstream-project-entry`](https://github.com/luhaihui106/X-Panel-WH/tree/docs/downstream-project-entry) / [PR #3](https://github.com/luhaihui106/X-Panel-WH/pull/3) | 本文档与改名后的链接同步，目标为功能分支；尚未更新 `main` 首页 |

本文档记录的候选源码为 `f4f351adef059040cab935492640920e947ba186`，面板版本仍为 `26.9.13`，不是独立正式版本号。核查时本仓库 [Releases](https://github.com/luhaihui106/X-Panel-WH/releases) 为空；既有受控部署或本地交付包不等于公开发行。此文档分支不修改功能分支、部署或包身份。

## 获取与使用

目前可靠入口是[固定提交的源码构建](docs/build-install-upgrade.md)，需要 Go 1.27 与启用 CGO 的 SQLite/C 工具链。没有本派生版公开的一键下载/安装命令。仓库继承的 `install.sh`、`x-ui.sh` 仍含上游下载、更新及 Pro 分流逻辑，**不要作为保留本派生功能的生产升级入口**。

升级前备份一致的数据库、配置、程序和服务定义；先隔离验证，再审批切换。回滚需恢复相互匹配的旧程序与数据库快照，不能假设新数据库可直接降级。[构建、升级、回滚与同步上游](docs/build-install-upgrade.md) · [兼容性与未完成门禁](docs/compatibility.md)

## 安全与来源

月重置并非跨 Xray/SQLite 的原子事务：破坏性读取后、数据库提交前崩溃仍可能丢失未持久化增量；未知非零身份会 fail closed 并阻塞后续采集/重置。退休身份与统计归档持久保留，不自动 GC；DB 提交后的核心恢复异步且可能失败。详见[身份与归档](docs/traffic-identity-retirement.md)。不要将生产数据库、凭据、订阅链接或机器信息上传至仓库/Issue。

遵循 [GPL v3](LICENSE)，保留上游版权及来源；X-UI、3X-UI、X-Panel 和第三方数据/组件的贡献与许可见[致谢](docs/credits.md)。不宣称原创或拥有第三方组件全部权利。
