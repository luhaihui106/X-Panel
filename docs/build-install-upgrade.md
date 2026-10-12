# 构建、安装边界、升级/回滚与同步上游

## 固定源码与本机构建

需要 Git、Go 1.27（以 `go.mod` 为准）、本机 C 编译器/开发工具和启用 CGO 的 SQLite；Node 22 用于前端回归。以下仅构建/测试，不安装服务，不获取生产数据。选择新目录，固定候选而非随分支漂移：

```sh
git clone --branch feat/client-monthly-reset https://github.com/luhaihui106/X-Panel-WH.git X-Panel-WH-candidate
cd X-Panel-WH-candidate
git checkout --detach f4f351adef059040cab935492640920e947ba186
git rev-parse HEAD
go version
cc --version
mkdir -p build
CGO_ENABLED=1 go build -mod=readonly -o ./build/x-panel .
CGO_ENABLED=1 go test -vet=off ./...
CGO_ENABLED=1 go test -vet=off -race ./web/service
node web/assets/js/model/monthly-reset.test.cjs
node web/assets/js/util/traffic-identity.test.cjs
node --test web/assets/js/util/home-promotion.test.cjs
```

本次文档复核在独立源码 checkout 中实际执行了以上 clone/固定提交、Go 1.27.0 ARM64 + GCC 11.4.0 CGO 构建、全量 `-vet=off` 测试、service race 与三项 Node 22 回归，全部通过；源码归档、SHA256 和上游 tag 审查分支命令也实际通过。为限制资源，仅在测试子进程中设置 `GOMAXPROCS=2`、`GOFLAGS=-p=2`，没有全局安装工具链、启动生产程序或替换已有交付包。普通 vet 仍有下文链接中的上游遗留问题，不能解释为 vet 全绿。

普通本机构建不等于静态跨发行版包；CGO 交叉构建须匹配目标架构 C 工具链，不能简单把 CGO 关闭来规避 SQLite。静态候选使用 musl 工具链、`netgo,osusergo` 和 external/static 链接，应另记录 Go/C/musl 版本、来源和校验。

面板使用嵌入式前端，修改模板后必须重建程序；这不意味着可以复用旧包宣称包含新 UI。上面仅生成面板，完整运行仍需要核对外部 Xray 与数据资产、架构、命名、许可证和服务配置，不应直接在生产数据库上启动它。

## 源码归档与交付身份

在固定源码 checkout 中可导出不含 `.git` 或未跟踪运维文件的源码归档（先核查受跟踪源码，没有秘密或生产数据）：

```sh
git archive --format=tar.gz --prefix=X-Panel/ --output=../x-panel-source-f4f351adef059040cab935492640920e947ba186.tar.gz f4f351adef059040cab935492640920e947ba186
sha256sum ../x-panel-source-f4f351adef059040cab935492640920e947ba186.tar.gz
```

归档不是已发布 Release，也不导出/清除历史。二进制交付应附源码 commit、源码归档、完整许可证、构建工具链/命令、面板与 Xray 各自版本、包清单和 SHA256；重新构建的产物不能冒称与既有已部署包逐字节相同。不要把凭据、数据库、日志、机器地址或内部交付记录打进公共包。

## 当前安装入口的真实边界

目前本派生仓库 Releases 为空，没有公开派生二进制下载、一键安装或派生 Docker 镜像保证。源码存在 `install.sh`、`x-ui.sh`、`x-ui.service`，但继承脚本仍指向上游、包含免费/Pro 分流和上游更新，因此本说明**不提供生产执行这些脚本的命令**。上游官方 installer 不会自动安装本功能，自动更新还可能覆盖它。

本地验收包与安装/服务辅助工具是受控交付，不是 GitHub assets，也不在文档中伪造公网地址。新安装应另行取得授权、选择目标架构和服务管理方式，并使用审核过且来源/校验完整的包。只编译成功不足以声称安装完成。

## 升级与回滚检查清单

1. 私下确认目标机器、当前程序/core 版本、架构、服务定义、DB/配置路径和权限；不要将其提交到仓库。
2. 保护性备份：旧程序/包、服务定义、配置和一致的 SQLite 快照。不能在写入中仅复制主 DB 忽略 WAL；使用一致备份方式，并在授权切换窗口停止写入后取得新鲜快照。
3. 用脱敏数据库副本在隔离环境验证迁移、客户端月设置、额度/到期/周期标记、真实流量采集、核心配置和 UI；区分注入计数与真实代理流量。
4. 切换需单独批准；固定包校验、停写与原子替换、受限启动探测、账户/订阅/配置/核心状态核对，设定明确超时及回退条件。停机前内存计数不保证零损失。
5. 回滚旧二进制与**匹配的旧数据库/配置/服务快照**，核对权限、监听及实际流量。新 schema 与旧二进制兼容不能凭空假设；恢复旧快照会回退快照之后的状态，须明确影响。

## 同步上游（开发，不是生产更新）

先读取上游 release/tag 与精确提交、免费版范围，审核工作流及下载地址；在新 integration 分支同步，再保留小范围下游补丁。示例只 fetch 并建立本地审查分支，不 merge/push/main/deploy：

```sh
git fetch https://github.com/xeefei/X-Panel.git tag v26.9.13
git rev-parse 'v26.9.13^{commit}'
git switch -c integration/review-v26.9.13 v26.9.13
```

未来版本用核查过的 tag 与新分支名，不盲用 `latest`；先跑上游基线，再在单独功能分支迁移/解决冲突，执行下游回归、故障注入、隔离 API/UI 和目标平台/回滚验收，独立 review 后才决定 merge/release/deploy。继承 `release.yml` 与 `docker.yml` 已改为仅显式手动触发，main push、合并、tag 和上游发布均不会自动发行或部署；脚本仍含上游资产或发布 namespace，手动使用前须另行审计。不要手工 dispatch 或 push tag 来试验文档。

## 维护与发行状态

本仓库是本派生版今后的维护与发行来源。main 已集成上游免费 `v26.9.13`、下游功能与文档：PR #1（上游同步）、#2（功能）、#3（文档）、#4（工作流安全）和 #5（维护状态）均已合并。保留 integration/feature/docs 分支及原提交身份，不改写历史。

已验收候选源码为 `f4f351adef059040cab935492640920e947ba186`；后续 main 合并及文档/CI 提交不等于重新验收或替换该包。面板内部版本仍为 `26.9.13`，不能独自识别派生版。既有受控部署或本地交付包不是公开发行；[Releases](https://github.com/luhaihui106/X-Panel-WH/releases) 当前为空。

今后上游更新先进入新的 integration 分支，经上游/下游回归、独立审查和隔离验收后再合并 main；正式发布另行批准。计划采用独立派生标签（例如 `v26.9.13-wh.1`，仅为命名示例，尚未创建），同时记录上游基线、精确源码提交和包校验值，不冒充上游版本。

流量实现及风险详见[月重置说明](downstream-monthly-reset.md)、[身份归档](traffic-identity-retirement.md)与[兼容性门禁](compatibility.md)。退休身份与统计归档持久保留，不自动 GC；HEAD 凭据清理不代表凭据已撤销、历史已清除或完成全仓安全审计。
