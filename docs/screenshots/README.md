# 截图来源 / Screenshot provenance

这四张图由真实 Chromium 渲染完整 X-Panel 中文界面，不是静态仿制页面、接口 mock 或生产服务器截图。

- **程序来源**：现有 ARM64 静态候选包 `x-panel-candidate-linux-arm64-static-f4f351adef05.tar.gz`，源码提交 `f4f351adef059040cab935492640920e947ba186`。界面显示面板 `v26.9.13`、Xray `v26.9.9`。文档基线 `5aa0b126` 与此提交的运行时代码无差异（差异仅文档、工作流及工作流安全测试）；没有重新设计或修改运行时代码。
- **独立实例**：仅绑定 loopback，使用新建合成数据库、独立 Xray 配置与资产目录；未读取或连接 RN5 生产数据库、账户或面板。通过既有本地验收模式初始化随机单用户，使用测试 API 会话登录；凭据不进入文档或图片。
- **演示数据**：三个客户端均使用保留域名 `example.net`，注释明确写明“演示数据”，额度 100 GB、实际客户端流量为 0；没有注入用量或在线状态。系统 CPU、内存、磁盘、网络等为隔离运行主机实际采集，不能当作客户端业务流量或生产性能证据。
- **脱敏**：截图前移除客户端 UUID/密码/订阅等表单行和 IP 地址卡片；未打开订阅、二维码或配置详情。增加明确的演示来源页脚。除这些截图级脱敏和手机长图展开外，保留原面板风格、功能和中文布局；未用图像编辑伪造数值。
- **安全警告**：保留 loopback HTTP 的未启用 TLS 警告。隔离演示不使用真实凭据；生产浏览器访问应启用 HTTPS。
- **验证与清理**：真实 API 添加后浏览器访问仪表盘、展开客户端、打开客户端编辑；月重置开关开启，示例重置日 31，面板时区 `Asia/Shanghai`。四张最终图均经图像审查；浏览器 `pageerror` 为 0。测试完成后终止自己的面板/核心进程组，删除合成数据库、凭据相关日志与临时运行资产。

Monthly reset is reached through **Inbound list → expand clients → Edit**. Choose **1–31** in the **panel timezone**; short months use their last day. These images illustrate the editor, not execution of a monthly boundary.

These are real full-panel browser captures from the pinned isolated build above. Client identities are synthetic `example.net` data with zero usage; host telemetry is real, not mocked. Credential fields and the IP card were removed before capture. No production account/database, subscription URL or QR payload is published. The mobile image is a 390 px-wide vertical layout capture, not a claim of native-device testing.

## 图片尺寸与校验 / Image integrity

| 文件 | 尺寸 | SHA-256 |
|---|---|---|
| [dashboard.png](dashboard.png) | 1600 × 1500 | `75b385cf3e7920a44e364a5d937e3be370b0c6a5eb76005b922e4dd8d653f96e` |
| [clients.png](clients.png) | 1440 × 1080 | `cbedfdcba36c0fb0a8261b830770291e3c94c87b985bc97326a2ec26067fc6ae` |
| [monthly-reset.png](monthly-reset.png) | 1440 × 1080 | `184cbdcf22d4680af17a8b42755e7d3bc3e1752724f4905e571079781e9d0fd3` |
| [mobile.png](mobile.png) | 390 × 3100 | `fb2bd553602eced1aade5e0211a3670129a2d1b1f42378379f0a4b3e8fc2a424` |

[返回中文主页](../../README.md) · [English homepage](../../README.en.md)

## PR #8 阶段5：工具导航截图

以下两张是本轮新建随机账户、通过真实 `/login` 登录、由 Chromium 截取的完整隔离面板页面，不是机器人画面，也不替换上述旧版本四图的来源说明。

- 程序：从已接受基线 `e72e14ce0a51e4ec258269f514e929dce1cf47d9` 加本轮 `navigation.html` 广告清理重建的本地 ARM64/CGO 程序；未变更面板/核心版本，未加载核心资产。最终源码 SHA 由阶段交付记录固定，不把旧图标为新提交截图。
- 仅 loopback；全新合成空业务数据库，无生产账户、客户端、订阅、证书或 bot 凭据。关闭 Telegram、登录通知、订阅及外部流量上报；外部 HTTP 使用不可达本地代理，浏览器拒绝非 loopback 请求。没有调用安装/更新/核心下载。
- 两张页面不含凭据或客户端字段，未替换 DOM、mock 响应、修改图片或伪造统计。原样保留侧栏版权、实用资源/社区链接。全页面长图是 1440px 桌面视口截图，不是手机设备验收。
- 导航两个标签、旧推广页参数回退、资源页直达、首页真实状态轮询、手动 Pro 限制和本地备份弹窗通过；浏览器 pageerror / 外部页面请求均为零。面板子进程留在本任务受管进程组内，验收后终止并删除合成数据库及日志。
- 无真实 Telegram 客户端会话，本轮不能提供 bot 菜单/向导聊天截图；不会把本地 API 载荷绘制成假聊天图。

| 文件 | SHA-256 |
|---|---|
| [utility-navigation.png](utility-navigation.png) | `65216b2b83f6bf833a3a539bb009848a291637ec42fdddc6d3a11cbfe7d0b08c` |
| [utility-resources.png](utility-resources.png) | `76d1b16720ff4919badffa40a6e7432db7590395bbd3728fec3ea9877af0412f` |

本地可复核的程序、脚本、日志和 `browser-evidence.json` 位于 `/root/.hermes/cache/scratch/xpanel-stage5/`；这些不是发行包或线上验收。
