# 第2阶段：依赖审计与下一阶段交接

审计基线：`cbd571e45dd7cdc11dade820c850113f72a0710c`，分支 `feat/tgbot-admin-cleanup`，PR #8。开始时工作区 clean，远端 PR head 与本地一致，PR 为 open/draft。本轮唯一 writer；仅依赖元数据标准化和本报告，不修改第1阶段安全修复、monthly UI、菜单、通知、Pro 限制、安装器或核心。

## 真实 before / after

结论：**无可删除依赖，精简为 no-op**。没有把元数据调整冒充依赖体积减少。

| 项目 | Before | After |
| --- | --- | --- |
| go.mod require 模块 | 88 | 88 |
| 直接依赖 | 21 | 22 |
| 间接依赖 | 67 | 66 |
| go.sum 行数 | 256 | 256 |
| 删除 / 新增模块 | — | 0 / 0 |
| 模块版本、Go 指令、replace | 原值 | 全部不变 |

唯一 go.mod 差异：`github.com/mattn/go-sqlite3 v1.14.52` 从 indirect 移至直接依赖。`web/service/tgbot_binding_generation.go:6` 已直接 blank-import 该 SQLite 驱动，第1阶段独立可信发行账本通过 `database/sql` 的 `sqlite3` 使用它，不能删除。标准 `go mod tidy -diff` 在修改前 exit 1，输出只有这项分类差异；按其结果修改后 exit 0、无 diff。未运行任何依赖升级命令。

`go.sum` SHA-256 前后完全一致：`b04fd7a74aa6eef4c4c0da539735abffd194f8fae8a6766985bf380b6a9e7340`。

审计包括 `go list -m -json all`、`go list -deps -test -json ./...`、`go mod why -m all`、实际直接 import 和标准 tidy。223 个选中模块（含主模块）的路径、版本和 replacement 前后相同；JSON 的 direct/indirect 元数据不同，不能用逐字节 JSON 相等作为模块图不变的判据。对 88 个显式 require 的 why 结果逐项检查，无 `(main module does not need module ...)`。整个依赖图中的其他节点出现此提示，不等于本仓库存在可删除的 require；也不能据此手动删 checksum。

娱乐/广告源码移除提交 `9f0ba9ca` 没有 go.mod/go.sum 差异。当前 Telegram 的 uuid 用于客户端生成，telego 用于实际机器人操作，fasthttp/fasthttpproxy 用于机器人代理；cron、gopsutil、国际化、Gin、GORM 与 Xray/grpc 依赖仍有运营摘要、调度、面板、数据库和核心调用。跨平台间接依赖和依赖测试需要的 checksum 均保留，未因本机 ARM64 的包选择盲删。

## 本轮实际验证

工具链：`/root/.hermes/cache/scratch/xpanel-toolchain/go/bin/go`，Go 1.27.0，linux/arm64，CGO_ENABLED=1。

- 修改前 `go test -vet=off -count=1 ./...`：PASS。
- 修改后 `go test -vet=off -count=1 ./...`：PASS（强制重跑，非只引用历史结果）。
- `go test -vet=off -race -count=1 ./web/service`：PASS。
- `node web/assets/js/model/monthly-reset.test.cjs`：PASS。
- `node --test web/assets/js/model/monthly-help.test.cjs`：PASS。
- `node web/assets/js/util/traffic-identity.test.cjs`：PASS。
- `node --test web/assets/js/util/home-promotion.test.cjs`：PASS。
- `go build -o /root/.hermes/cache/scratch/stage2-x-panel .`：PASS，仅本地验证程序，不是包装/发行候选。
- `go mod tidy -diff`：PASS，无差异；`go mod verify`：PASS。
- workflow-safety：使用已有 `/root/.hermes/cache/scratch/x-panel-doccheck-venv/bin/python` PASS。默认 python 首次执行缺少 yaml，未安装/升级仓库依赖，切换已有验证 venv 后成功。
- `go vet ./...`：仍是两个已知继承错误：`tgbot.go:3359` 函数 Message 与 nil 比较；`server.go:1396` 非常量 fmt.Errorf 格式串。没有 vet-clean 声明，也不在本轮顺手修复。

证据：scratch 下 `stage2-before.json`、`stage2-modules-{before,after}.json`、`stage2-packages-before.json`、`stage2-why-before.txt`、`stage2-tidy-before.diff`、`stage2-allgo-{before,after}.txt`、`stage2-race.txt`、`stage2-vet.txt`。本轮只改配置元数据与文档，未新增生产函数或行为，因此无新行为 RED/GREEN 声明；不把分类检查当作安全行为 TDD。独立代码审查 skill 对纯配置/文档可跳过，本轮未启动第二 writer 或另一个 reviewer。

## 菜单 / addinbound：仅源码核验，下一阶段执行边界

现有事实：

- `web/service/tgbot.go:203` 注册 start/help/status/id/subconverter/restartx；`answerCommand` 在 :511 起还处理 usage/inbound/restart。命令可处理不代表出现在 Telegram 命令列表。
- `SendAnswer`（:2049）管理员键盘现有服务器用量/重启面板、排序报告/全量重置、备份/封禁日志、入站/即将耗尽、命令/在线、全部客户端/添加客户端、订阅转换；普通用户只有本人流量/命令。这是现状清单，不是已批准的新分组/排序。
- `add_client`（:1510）选择已有入站；`SubmitAddClient`（:2018）调用 `InboundService.AddInboundClient`（:2037）。这不是创建新入站。
- 在本仓库全源码搜索，未发现字面 `addinbound` Telegram 命令或 callback。不能将它描述为已实现或把 `/inbound` 查询误认为新增入站。
- Web 的入站新增是真实路径：`web/controller/inbound.go:32` POST /add → :94 `addInbound`（绑定当前登录面板用户、生成 tag）→ :110 `InboundService.AddInbound`。服务 :172 起校验端口/邮箱/客户端，预留流量身份、设置时间戳和月重置状态、数据库事务 COMMIT 后再调用 Xray AddInbound（:348）；需要重启时返回标记。已有入站导入及事务失败测试不得回退。

已有批准规格查找范围：仓库 docs/README、`/root/hermes-work` 项目输出、scratch 中前阶段报告/日志，以及默认 Hermes 本地 Markdown 报告/协调输出；没有找到明确的新版菜单分组/按钮文案/顺序或 Telegram addinbound 字段与协议规格。旧 upstream README 和旧源码副本不是新增功能授权。已知的是阶段推进顺序，不是功能细节，不能从该顺序补造需求。

父协调者下一阶段可直接执行的已确定范围：

1. 以本阶段提交为基线保留全部第1安全边界与 monthly UI，先冻结上述现有命令/按钮/handler 对照表；继续从父对话原始批准记录获取菜单规格。只有明确批准的排序/分组/文案才做最小菜单改动，不扩增能力。
2. 对已批准的菜单改动先写本地 Telegram API 载荷 RED 测试，GREEN 后验证管理员和普通用户菜单、token 发行/重绘/旧消息拒绝，保留通知、危险操作确认及 Pro 限制；不修改业务通知调度。
3. 将 Telegram addinbound 与既有 AddInboundClient 分开。未找到明确批准规格之前，仅可继续源代码/调用链核验，不发布一个自行猜测的新增向导。若父记录已批准，先冻结协议/默认字段/用户归属与 tag 规则、输入校验、确认/取消/超时/重复提交/多管理员策略、提交失败可修正重试及核心不可用时的结果规则，再做行为测试与最小实现；复用既有服务，不能绕过事务/流量身份/月重置规则。
4. docs 阶段应修正 `docs/telegram-admin-bot.md:21` 仍称失败登录通知发送密码的过时描述，依据第1实际处理器和测试更新；本轮没有扩展文档修订范围。

仍缺上下文：菜单的精确分组、排序、文案以及是否补注册 usage/inbound/restart；addinbound 是否要求新增命令/按钮、支持哪些协议及必填/默认字段、交互和失败语义。父协调者应优先找原始批准记录，只对无法检索的功能选择升级给用户，不能要求用户重做已有材料。

## 交付与授权边界

PR #8 保持 draft，不 merge。本轮仅独立依赖审计提交；推送后的 exact SHA、远端 head 和 CI readback 记录在交付消息，避免自引用 SHA。本轮未触碰生产、RN5、包装、release、安装、Hermes provider/config；下一阶段由父协调者串接，本文不自动启动后续 writer。第1阶段证据边界继续以 `docs/stage1-telegram-security.md` 为准，依赖回归成功不等于生产验收。
