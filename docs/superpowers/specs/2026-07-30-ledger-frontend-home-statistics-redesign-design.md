# GPT-Load 2.0 Ledger 前端基础、首页、登录与统计重构设计规格

> 状态：待用户审核；本规格未授权实施。
>
> 基线分支：`v2`
>
> 基线提交：`52e497f1`
>
> 编写日期：2026-07-30
>
> 下一门禁：用户书面确认本规格后，才能编写实施计划；实施计划再次确认后，才能创建
> worktree、分支并由 subagent 开始实施。

## 1. 摘要

本轮不是在现有首页上继续打补丁，而是以已经确认的三个临时模板为视觉事实源，重建统一的
Ledger 前端基础层，并重写首页与登录页。首页只保留“尚未导入”和“已有配置”两种业务形态，
不再根据健康状态、密钥异常或用量是否为空切换页面结构。已有配置时，缺少的统计值按零展示。

后端按目标页面重新设计，不要求新首页兼容当前由 `groups + health + usage` 拼装出的旧查询
模型。首页使用一个非轮询基础接口和一个 30 秒轮询统计接口；统计持久层按小时、访问密钥、
分组和用户请求模型聚合，能够稳定支持 24 小时与 30 天窗口、趋势和三个维度的前五排行。

本轮同时统一持久化与管理 API 的绝对时间合同：数据库使用 Unix 毫秒整数，API 使用带
`_ms` 后缀的 Unix 毫秒整数，前端通过唯一公共格式化层按浏览器时区显示。不存在 RFC 3339
双轨或旧字段兼容层。

前端不新增、保留或执行任何单元测试、组件测试、视觉测试或 E2E。前端仍执行 format、
lint、type-check 和生产构建，这些属于静态质量与构建门禁。Go 后端及 Go 承载的路由、存储、
嵌入资源和发布合同继续按 TDD 实施并保留测试覆盖。

## 2. 事实源与裁决顺序

### 2.1 最终视觉基线

以下文件是本轮 Home 与 Login 的最终视觉事实源。它们只用于视觉、文案层级、几何和交互
意图对照；正式 Vue 实现不得复制其临时 class、内联样式或演示数据。

| 页面 | 文件 | SHA-256 |
|---|---|---|
| 已有配置首页 | `tmp/home-ledger-preview.html` | `567e685d5ca087011ecf2a4095ea48f3b6ab86283d01a493962433000a66cd1b` |
| 尚未导入首页 | `tmp/home-empty-ledger-preview.html` | `4051551fc8aeaab146eb2ae612b3a30c03b8177f49d23b0909a1cbbc7509297c` |
| 登录页 | `tmp/login-ledger-preview.html` | `900caa087afb3deb24b96b558a52f75bf9c999ecec48227667978d0b0f4aca92` |

模板中的固定数量、时间、版本、密钥、金额和客户端配置均为演示数据，不得硬编码进生产代码。
模板中标明“演示”的一键导入成功动画也不是已实现功能。

### 2.2 正式文档

1. [GPT-Load 2.0 页面设计](https://app.notion.com/p/3ac5e49ce6ae818ca1b1ecfb71943edd)
2. [首页设计](https://app.notion.com/p/3ac5e49ce6ae8186bd20d33f52159eb2)
3. [连接到网关设计](https://app.notion.com/p/3ad5e49ce6ae81238459fab1d113f909)
4. [GPT-Load 2.0 交互设计文档](https://app.notion.com/p/3a55e49ce6ae813eba04eac597a0e7c1)
5. [GPT-Load 2.0 M3 前端视觉规范](https://app.notion.com/p/3a55e49ce6ae8120833dc1c7434b7aa3)

稳定的认证、安全、mutation、路由、无障碍和单二进制交付合同继续有效。页面视觉、首页状态、
连接区内容、内容宽度和前端测试策略，以当前会话及三个最终模板为准。

### 2.3 冲突裁决

冲突时按以下顺序裁决：

1. 当前会话已确认要求；
2. 本规格经用户确认后的内容；
3. 三个最终模板；
4. Notion 页面设计与交互设计；
5. 历史 Superpowers 规格；
6. 当前代码。

本规格替代以下历史内容：

- 首页健康正常、异常、未知、零用量等多状态结构；
- 首页问题密钥明细和健康判断；
- 连接区静态占位或 Base URL、可用模型、协议列表等旧结构；
- `1280px` 内容宽度；
- 旧 Ledger canvas 色值和 64px 顶栏；
- Home 继续复用 `/api/health`、`/api/groups`、`/api/usage` 拼装数据的要求；
- 前端 Vitest、Playwright、视觉 evidence 和 E2E 验收要求；
- API 中 RFC 3339 时间字符串和显式 `timezone` 字段。

## 3. 目标与非目标

### 3.1 目标

1. 将视觉模板抽象为单一全局 design token、排版、间距、容器和组件系统。
2. 所有受认证管理页面使用相同 `1120px` 最大内容宽度和同一 AppShell。
3. 精确复刻最终首页和登录页的 light/dark、桌面与移动端表现。
4. 首页只有两种业务形态，统计缺失不再触发第三种页面。
5. 24h/30d 切换原子更新成功率、成本、趋势和三个排行。
6. 统计接口每 30 秒刷新，并用后端观测时间更新顶部“更新”字段。
7. 建立能够支持多时间窗口、多维排行和未定价 token 的统计持久层。
8. 统一数据库、API 和前端的时间、成本、数量、token 和持续时间格式。
9. 支持简体中文、English、日本語；首次访问按浏览器语言列表选择并持久化。
10. 连接区保留高频实用功能，同时只呈现访问密钥选择、客户端标签和客户端配置。
11. 不新增第三方依赖。

### 3.2 非目标

- 不把成本升级为账单、invoice 或财务 ledger；仍是 estimate。
- 不新增多实例一致性、消息队列 outbox 或复杂统计补偿系统。
- 不为旧 API 字段、旧数据库时间类型、旧 DOM、旧 selector 或旧前端测试建立兼容层。
- 不在本轮重新设计 Group Detail、Import、Access Keys、Monitor、Settings 的业务流程。
- 不在首页展示健康结论、异常分组、问题密钥、请求日志管线告警或失败日志链接。
- 不增加 Base URL 输入、局域网模拟、可用模型列表或支持协议列表。
- 不在 GPT-Load 页面、缓存、当前页面 URL、日志或错误对象中显示访问密钥明文；用户明确确认
  NextChat 导入后，允许把明文放入新窗口的客户端侧 hash fragment 完成一次性交接。
- 不新增任何前端自动化测试或前端测试基础设施。
- 本规格确认前不创建 worktree、不写实施计划、不修改业务代码。

## 4. 方案比较与选择

### 4.1 采用：独立首页读模型 + 规范化统计聚合 + 全局前端基础层

- `GET /api/home` 返回配置与运行基础信息，不轮询。
- `GET /api/home/statistics?range=24h|30d` 返回整个窗口的首页统计，每 30 秒轮询。
- request log 写入时，在同一 SQLite 事务中更新规范化小时聚合。
- Home 与 Login 直接重写；其他页面迁移到相同 tokens、AppShell、页面容器和基础组件。
- Monitor 保留自己的健康与 usage 能力，不再充当 Home 的数据接口。

该方案让接口职责、轮询成本、统计维度和页面结构与最终设计一一对应。

### 4.2 不采用：继续组合现有 Groups、Health、Usage 接口

现有 Home 同时订阅三个资源，形成健康状态驱动的多态页面。现有 usage 聚合缺少 AccessKey
维度，使用上游模型，未定价请求不累计 token，无法生成目标排行。继续适配会把旧语义带入
新设计，并增加客户端状态组合和轮询次数。

### 4.3 不采用：单个大型 Home 接口全部 30 秒轮询

分组数、模型数、版本、进程启动时间和访问密钥列表不随统计窗口变化。把这些数据和趋势一起
轮询会产生无意义查询，也会在范围切换时重复加载连接配置。用户已明确要求静态基础信息与
窗口统计分开。

## 5. 总体架构

```text
Design tokens + typography + layout primitives
                  |
                  +--> Authenticated AppShell --> all management pages
                  |
                  +--> PublicShell -----------> Login
                  |
                  +--> Home
                        +--> GET /api/home                on entry/invalidation
                        +--> GET /api/home/statistics     30s, 24h or 30d
                        +--> window.location.origin       connection base
                        +--> POST /api/access-keys/:id/reveal on explicit copy/import

Gateway final request observation
                  |
                  +--> request_logs
                  |
                  +--> usage_stats hourly aggregate
                         dimensions:
                         access_key_id + group_id + client_model
                         |
                         +--> dense 24 hourly points
                         +--> dense 30 daily points
                         +--> model/group/access-key top 5
```

边界规则：

- API projector 负责 wire data 的严格运行时校验；
- Home presenter 负责两种业务形态、单位格式和展示 DTO；
- Vue 页面只负责呈现与用户 intent，不在模板里拼查询、金额、时间或密钥；
- Go control 层拥有 Home API 契约；
- requestlog 层拥有统计写入、窗口计算和排行查询；
- storage 层拥有 Unix 毫秒、固定点成本和 schema migration；
- 连接客户端预设是纯前端配置，不要求后端识别当前站点域名或客户端类型。

## 6. 全局 Ledger 视觉规范

### 6.1 颜色 token

页面和业务组件不得硬编码 Hex、RGB 或重复定义 light/dark 值。所有颜色只引用下列语义
token；overlay 等带透明度派生值也集中定义。

| 语义 | Light | Dark |
|---|---|---|
| canvas | `#eeede9` | `#0b0d10` |
| panel | `#ffffff` | `#171b20` |
| sunken | `#f5f4f1` | `#12151a` |
| foreground | `#15181b` | `#e8eaec` |
| foreground-muted | `#4e545b` | `#969ca3` |
| foreground-faint | `#787f87` | `#6d747c` |
| rule | `#e6e5e0` | `#232830` |
| rule-strong | `#cfcfc9` | `#333a43` |
| action | `#1c4f6e` | `#6fb2d6` |
| action-soft | `#e8eff4` | `#142633` |
| action-ink | `#ffffff` | `#0c1a22` |
| success | `#1a6b3f` | `#4fb178` |
| success-bg | `#e6f3ec` | `#112a1d` |
| warning | `#8f6212` | `#d5a341` |
| warning-bg | `#f8f0dd` | `#241d10` |
| danger | `#d03b3b` | `#e66767` |
| danger-bg | `#fbebe9` | `#2a1613` |

状态色和弱文字不得共用同一语义 token。disabled 使用 opacity 与 disabled cursor 组合，不通过
另一个随意灰色模拟。

### 6.2 字体与字号

字体栈：

- Sans：`system-ui, -apple-system, "Segoe UI", sans-serif`
- Serif：`ui-serif, "Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif`
- Mono：`ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace`

基础字号为 `13.5px`。全局类型尺度集中为：

| Token | 像素 | 用途 |
|---|---:|---|
| label-xs | 10.5 | uppercase eyebrow、表头 |
| text-sm | 11.5 | stamp、field label、hint |
| text-meta | 12 | KPI 辅助数据、表格次级信息 |
| text-body | 13.5 | 正文、控件 |
| title-section | 16 | 区块 serif 标题 |
| title-panel | 22 | 登录表单标题 |
| title-lede | 26 | 首页事实句、欢迎标题 |
| title-login | 34 | 登录页主叙述 |
| stat-value | 36 | KPI 主值 |

字重只使用浏览器可稳定合成的 `400`、`500`、`560`、`600`、`650`。导航普通态不加粗，
当前项 `560`；KPI 标签和主值均不加粗；首页事实句只对数字使用 `650`。

### 6.3 几何、阴影与动效

- 顶栏高度：`54px`
- 顶栏桌面水平 padding：`30px`
- 页面 stage：桌面 `26px 24px 60px`，小屏 `16px 12px 40px`
- 页面最大宽度：`1120px`
- sheet radius：`10px`
- control radius：`7px`
- sheet 桌面 padding：`28px 32px 30px`
- sheet 小屏 padding：`22px 18px 24px`
- sheet shadow：与模板一致的双层轻阴影
- 常规交互：`120–180ms`
- 数据区切换：`160–220ms ease-out`
- `prefers-reduced-motion: reduce` 时禁用非必要动画

阴影、圆角、间距、z-index、焦点环和动效时长都必须集中成 token。页面不得创建第二套尺度。

### 6.4 页面宽度

所有受认证页面和登录 sheet 都通过同一个 `PageFrame`/`LedgerSheet` 约束到 `1120px`。
页面不得自行定义 `max-width`。长表仅允许在 sheet 内局部横向滚动，不能扩大页面宽度。

## 7. 全局组件与代码所有权

### 7.1 Shell 与布局

- `AuthenticatedAppShell`：品牌、主导航、导入、偏好菜单、退出和移动抽屉。
- `PublicShell`：登录页品牌、语言和主题；不显示认证导航或退出。
- `PageFrame`：统一 stage 和 `1120px` 宽度。
- `LedgerSheet`：统一 panel、边框、圆角、阴影和响应式 padding。
- `PageSection`：统一区块标题、分隔线和垂直节奏。

品牌统一显示 `GPT-Load`，不得出现 `gpt-load`、`GPT-LOAD` 或 `GPT-Load 2.0` 作为顶栏品牌。

### 7.2 基础交互组件

- `AppButton`、`IconButton`
- `AppSelect`
- `FormField`
- `SegmentedControl`
- `PreferencesMenu`
- `CopyAction`
- `SecretCopyAction`
- `InlineFeedback`
- `EmptyState`
- `DataTable`
- `CodeBlock`

所有按钮、输入、select、tab、popover、drawer 和 feedback 复用基础组件，不允许页面复制基础
边框、hover、focus 或 disabled 样式。

### 7.3 首页组件

- `HomeSummary`
- `StatFigure`
- `TrendChart`
- `ConsumptionRanking`
- `GatewayConnection`
- `HomeWelcome`

组件不拥有 API client；页面资源层拥有请求，presenter 输出严格 props。

### 7.4 公共格式化模块

建立唯一格式化入口，至少包括：

- `formatLocalInstant(ms, locale, options)`
- `formatLocalTime(ms, locale)`
- `formatDuration(startedAtMs, nowMs, locale)`
- `formatInteger(value, locale)`
- `formatPercent(success, total, locale)`
- `formatTokens(value, locale)`
- `formatEstimatedCost(nanoUSD, locale)`
- `formatMaskedAccessKey(suffix)`

页面和业务组件禁止直接使用 `new Intl.*`、`Date#toLocale*` 或拼接单位。

## 8. AppShell、导航与响应式

### 8.1 桌面导航

顺序固定为：

1. 首页
2. 分组
3. 访问密钥
4. 监控
5. 设置

当前项使用正文色、`560` 字重和 `1.5px` 下划线。普通项使用 muted 色和正常字重。导航不显示
图标。

当前路由缺少 `/groups` collection。为避免“分组”成为死链接，本轮增加只读入口型
Groups collection，复用现有 `GET /api/groups` 与全局表格/空态，行进入现有 Group Detail。
该页不改变 Group 的创建、编辑、导入或删除流程，也不推演未确认的复杂页面信息架构。

### 8.2 右侧操作

- “导入密钥”使用 key-round 语义图标，不使用 upload/export 图标。
- 常规桌面显示文字与图标；小屏只保留图标和可访问名称。
- 语言、主题和退出集中在偏好菜单。
- 登录页只显示语言和主题。

### 8.3 小屏

`860px` 以下隐藏桌面主导航，使用菜单抽屉。顶栏仍保持 `54px`。sheet、状态句、stamp、
KPI、趋势、排行和连接区不得横向溢出。

24h/30d selector 在小屏占据 KPI grid 第一行并右对齐；两个 KPI 在第二行并列。selector
不得移动到 KPI 下方。

## 9. 国际化

### 9.1 支持语言

只支持：

- `zh-CN`
- `en-US`
- `ja-JP`

### 9.2 首次选择与持久化

选择顺序：

1. `localStorage["gpt-load.locale"]` 中的合法精确值；
2. 按顺序扫描 `navigator.languages`；
3. 再检查 `navigator.language`；
4. 按语言族匹配：`zh-* → zh-CN`、`en-* → en-US`、`ja-* → ja-JP`；
5. 全部不支持时使用 `en-US`。

切换后立即更新 `document.documentElement.lang` 并写入 localStorage。i18n 的固定
`fallbackLocale` 是 `en-US`，不得跟随当前语言变化。

### 9.3 文案规则

- 中文量词使用“个密钥”，禁止“把密钥”。
- 首页标题只表达统计事实，不表达“正常”“异常”或健康结论。
- 排行、趋势和连接区不使用可点击文字冒充导航。
- 缺失实体统一使用本地化“已删除”。
- 未识别的空模型使用本地化“未知模型”，不显示空字符串。
- 所有 aria-label、toast、tooltip、错误和空态均进入三语资源。

## 10. 时间标准

### 10.1 数据库

所有持久化绝对时间使用 SQLite `INTEGER` Unix milliseconds，不使用 `datetime` 文本。
字段统一以 `_ms` 结尾，例如：

- `created_at_ms`
- `updated_at_ms`
- `completed_at_ms`
- `cooldown_until_ms`
- `bucket_start_ms`

可空时间使用 `NULL`，不得用 `0` 表示缺失。持续时间继续使用 `duration_ms`，它是时长而非
绝对时间。

### 10.2 Go 内部

存储、窗口和 bucket 运算以 `int64` epoch milliseconds 为权威值。需要调用标准库 timer 或
第三方接口时，转换只发生在公共 time adapter。业务层不格式化时区字符串，不持有用户时区。

固定窗口边界：

- 小时：`3_600_000ms`
- 天：`86_400_000ms`

通过整数除法对齐 Unix epoch 边界，不使用服务端本地时区或日历时区。

### 10.3 API

管理 API 的所有绝对时间使用 JSON 整数并带 `_ms` 后缀。移除 RFC 3339 字符串和
`timezone` 字段，不提供旧字段并行返回。

查询参数使用 `from_ms`、`to_ms`。HTTP 标准的 `Retry-After` 秒数和业务
`duration_ms` 不改成绝对时间。

### 10.4 前端

前端只把 timestamp 当绝对 instant，并通过公共格式化层使用浏览器当前时区和当前语言显示。
顶部“更新”显示本地 `HH:mm:ss`，完整日期时间放在可访问 title/tooltip 中。切换系统时区后，
下一次渲染或刷新使用新时区，无需向后端发送时区。

## 11. 数量、token、比例、金额和持续时间

### 11.1 请求和排行数量

请求数和失败数以本地化千分位整数显示。零失败时不显示“0 次失败”；非零时追加失败说明。

### 11.2 成功率

`request_count == 0` 时显示 `0%`。否则后端返回整数计数，前端计算：

```text
success_count / request_count * 100
```

最多一位小数，整数结果不显示 `.0`。KPI 不加粗。

### 11.3 Token

统计和排行使用总 token。显示采用稳定技术单位 `K / M / B / T`，最多一位小数；小于 1000
显示本地化整数。完整精确值放在 title/aria-label 中。

即使没有匹配价格，只要 request log 有完整或部分 usage，token 必须进入统计。

### 11.4 成本

持久化单位为 nano USD：

```text
1 USD = 1_000_000_000 nano USD
```

数据库使用非负 `INTEGER`。API 为避免 JavaScript 安全整数边界，以十进制数字字符串返回，
字段名为 `estimated_cost_nano_usd`。

显示规则：

- `0` → `$0.00`
- `>= $1` → 固定 2 位小数
- `$0.01 <= value < $1` → 最少 2 位、最多 6 位，移除第 2 位后的尾随零
- `$0.000001 <= value < $0.01` → 最多 6 位
- `0 < value < $0.000001` → `<$0.000001`

使用 `USD` narrow symbol 和当前语言的分组/小数分隔。KPI 不加粗。

未定价请求的成本按零累计，但 `unpriced_request_count` 增加；其值为零时不显示“0 条未定价”。

### 11.5 运行时间

基础接口返回 `server_now_ms` 和 `started_at_ms`。前端建立服务器时钟偏移并每分钟刷新持续
时间，避免用一次性“已运行”字符串。单位按三语本地化，最大显示到天和小时。

## 12. 首页业务形态

### 12.1 判定

首页只存在两种业务形态：

```text
group_count == 0 AND upstream_key_count == 0
    => 尚未导入
otherwise
    => 已有配置
```

Group 已存在但没有密钥时仍显示已有配置首页。统计全零、价格缺失、健康请求失败或访问密钥
列表为空都不得切换到欢迎页。

基础接口尚未返回时使用等高 skeleton。基础接口失败时显示可重试错误，因为此时不能可靠判断
两种业务形态；这属于加载错误，不是第三种业务页面。

### 12.2 尚未导入

精确结构遵循 `tmp/home-empty-ledger-preview.html`：

- 顶栏和已有配置首页完全相同；
- sheet 最大宽度、顶部位置和最小高度相同；
- 标题“欢迎使用 GPT-Load”；
- 明确的导入密钥操作；
- 一段简短说明和三步引导；
- 不显示 KPI、趋势、排行或连接区。

### 12.3 已有配置顶部

左侧只显示：

```text
{group_count} 个分组 ·
{available_upstream_key_count}/{upstream_key_count} 个密钥可用 ·
{model_count} 个模型
```

不显示日期、健康状态图标、正常/异常文字或异常明细。

右侧固定三行：

```text
更新 {statistics.observed_at_ms 的本地时间}
版本 {version}
已运行 {started_at_ms 到服务器当前时间}
```

首次统计尚未返回时，“更新”显示 `—`。版本和运行时间来自基础接口。

### 12.4 KPI 与窗口切换

24h/30d selector 位于 KPI 区右上。两个 KPI：

1. 成功率：请求数，失败非零时再显示失败数；
2. 估算成本：总 token，未定价非零时再显示未定价数。

范围切换必须原子更新 KPI、趋势、排行标题与三个排行数据。点击新范围后，不允许用新范围标签
搭配旧范围数据：

- 新数据返回前，目标按钮进入 pending，统计区域显示等高轻量 skeleton；
- 成功后整个统计快照一次替换；
- 失败时恢复原 selector 和原快照，并显示非阻塞错误；
- 初次加载失败且没有快照时用零值结构占位，并显示可重试错误。

### 12.5 趋势

标题固定为“请求量趋势 · 近 24h/30d”。图上不显示图例、开始/中点/结束时间、当前值或其他
静态说明。

数据点：

- 24h：24 个固定小时 bucket，含当前小时；
- 30d：30 个固定 24 小时 bucket，含当前 UTC epoch day；
- 后端返回稠密序列，空 bucket 为零；
- 请求量折线/面积和失败条使用各自独立的纵向归一化，共享横轴；
- 全零时保留平坦基线，不伪造波动。

交互：

- pointer 移动选择最近数据点；
- touch 点击选择数据点，再点外部关闭；
- 图表容器可聚焦，左右方向键切换数据点；
- tooltip 只显示本地化时间范围、请求数和失败数；
- pointer 离开或 Escape 关闭；
- guide、dot 和 tooltip 使用 `120–160ms`；
- 初次绘制和范围替换使用 `160–220ms`，reduced motion 时立即更新。

### 12.6 消耗排行

三个 tab 均由同一统计响应提供，切换 tab 不发请求：

- 模型：按 `group_id + client_model` 聚合，列为模型、分组、请求、tokens、成本；
- 分组：按 `group_id` 聚合；
- 访问密钥：按 `access_key_id` 聚合。

每个 tab 最多 5 行。排序固定为：

1. `estimated_cost_nano_usd DESC`
2. `request_count DESC`
3. 稳定 identity ASC

所有值均为纯文本，不生成链接。小屏隐藏 tokens 列，其他列保留。没有排行数据时显示紧凑
空行，不隐藏整个区块。

### 12.7 轮询

- 统计间隔：30 秒；
- 页面 hidden 时暂停 interval；
- 页面重新 visible 时立即刷新一次；
- 相同 range 不并发重复请求；
- 组件卸载或 range 改变时取消过期请求；
- 后返回的旧 range 响应不得覆盖新 range；
- 后续轮询失败保留最近成功快照，“更新”保持最近成功观测时间。

基础接口只在进入 Home、显式 retry、配置 mutation invalidation 和认证恢复后读取，不按 30 秒
轮询。

## 13. 连接到网关

### 13.1 可见结构

只保留：

1. 标题“连接到网关”；
2. 左侧访问密钥选择与复制；
3. 右侧客户端 tabs；
4. 下方当前客户端配置。

删除并不得以 hidden DOM 保留：

- “客户端配置”二级标题；
- Base URL 展示或输入；
- 局域网/本机模拟；
- 可用模型；
- 支持协议及协议选择。

网关地址直接来自 `window.location.origin`。生产环境前后端同源；开发环境由 Vite 对数据面
路径做代理，因此同一规则仍可手工调试，不需要后端配置域名。

### 13.2 访问密钥

基础接口只返回 active AccessKey，按 ID 升序，并携带用于客户端兼容判断的 effective
protocols；protocols 不单独渲染成“支持协议”列表。显示格式统一为：

```text
{name} · sk-gl-••••••••{4位小写十六进制 suffix}
```

页面只持有 `id`、`name`、`masked_key`。用户执行“复制密钥”、复制含密钥的字段、复制完整
snippet 或一键导入时：

1. 调用现有 `POST /api/access-keys/:id/reveal`；
2. 在当前事件的局部变量中生成 clipboard text 或导入 URL；
3. 完成操作后释放引用；
4. 明文不进入 DOM、Vue Query cache、store、URL query、日志、toast 或错误对象。

reveal 响应继续使用 `Cache-Control: no-store` 等 secret headers。API 字段改为
`revealed_at_ms`。

没有 active AccessKey 时，连接区显示紧凑空态和“创建访问密钥”操作；不伪造示例密钥。

### 13.3 客户端预设

本轮固定提供：

- NextChat
- Cherry Studio
- Claude Code
- curl
- 更多

NextChat、Cherry Studio、Claude Code 和 curl 都显示基于当前 origin 与当前 masked key 的
真实配置说明。可见代码不使用 `$GPT_LOAD_API_KEY` 等占位环境变量，直接使用统一 masked
格式；复制完整配置时通过 reveal 替换为真实密钥后写入 clipboard。

Claude Code 固定生成 Anthropic 原生协议配置；curl 提供 OpenAI Chat Completions 的通用
最小请求，不再依赖已删除的协议选择器。NextChat、Cherry Studio 和 curl 要求
`openai-chat-completions`，Claude Code 要求 `anthropic`。所选 AccessKey 不允许对应协议时，
配置仍可查看，但复制完整配置和一键导入禁用，并显示客户端局部 warning。“更多”只提供
说明，不伪造未实现客户端。

### 13.4 NextChat 一键导入

模板中的成功动画不能直接进入生产。正式行为：

1. 点击后显示确认对话框，明确说明将把网关地址和访问密钥交给 NextChat；
2. 用户确认时同步预打开空白新窗口，避免异步 reveal 后被浏览器拦截；
3. 调用 reveal，失败时关闭空白窗口；
4. 成功后把新窗口导航到
   `https://app.nextchat.club/#/?settings=<percent-encoded JSON>`；
5. payload 只含 `key` 与 `url`，配置位于客户端侧 hash fragment，不随 HTTP request 发送；
6. 本窗口不保存生成 URL，失败时不声称已导入。

实现以 NextChat 官方仓库固定提交
`706a18b95b714ab29b2a4842d3b9ff4f887935d5` 的
[`app/command.ts`](https://github.com/ChatGPTNextWeb/NextChat/blob/706a18b95b714ab29b2a4842d3b9ff4f887935d5/app/command.ts)
和
[`app/components/chat.tsx`](https://github.com/ChatGPTNextWeb/NextChat/blob/706a18b95b714ab29b2a4842d3b9ff4f887935d5/app/components/chat.tsx)
为协议依据。若实施时官方目标与固定协议无法实际打开，验收结果必须是失败，不能降级为
演示成功。该约束不影响手动配置和复制功能。

## 14. 登录页

### 14.1 结构

精确遵循 `tmp/login-ledger-preview.html`：

- `PublicShell` 54px 顶栏；
- 1120px 双栏 sheet，桌面最小高度 500px；
- 左侧为产品主叙述、简短说明和三项能力；
- 右侧 sunken 区为认证表单；
- 860px 以下转单栏；
- 页面 stage 在桌面垂直居中，移动端顶部对齐。

主叙述固定语义为“管理你的模型网关，保持清晰、可控。”，通过 `text-wrap: balance` 和列宽
保证不会只剩一个汉字单独换行。

### 14.2 表单

- AUTH_KEY input 初次渲染自动聚焦；
- 默认 password，可用图标按钮显示/隐藏；
- 保留 autocomplete、autocapitalize 和 spellcheck 安全设置；
- submit 支持 Enter；
- 提交中禁用重复提交；
- 认证成功继续使用 sessionStorage 和既有安全 redirect；
- 不显示 AUTH_KEY 来源说明、折叠箭头、tooltip 或文件路径帮助。

错误区预留固定最小高度，invalid、locked、network、invalid-response 切换不得推动整个登录
sheet。锁定倒计时继续遵循后端 `Retry-After`。

## 15. Home 基础接口

### 15.1 路由

```http
GET /api/home
Authorization: Bearer <AUTH_KEY>
```

只接受无 query 请求。响应使用现有统一 envelope。

### 15.2 响应

```json
{
  "code": 0,
  "message": "Success",
  "data": {
    "server_now_ms": 1785405600000,
    "started_at_ms": 1784865600000,
    "version": "v2.0.0-dev",
    "inventory": {
      "group_count": 6,
      "upstream_key_count": 42,
      "available_upstream_key_count": 33,
      "model_count": 18
    },
    "access_keys": [
      {
        "id": 1,
        "name": "生产网关",
        "masked_key": "sk-gl-••••••••88ab",
        "protocols": [
          "openai-chat-completions",
          "anthropic",
          "gemini"
        ]
      }
    ]
  }
}
```

### 15.3 计数定义

- `group_count`：当前持久化 Group 总数，含 disabled Group；
- `upstream_key_count`：当前持久化 UpstreamKey 总数；
- `available_upstream_key_count`：Group enabled、UpstreamKey active 且 runtime state 为
  available 的数量；
- `model_count`：enabled Group 对外暴露的去重 client model 名数量；存在 alias 时使用 alias，
  否则使用 model ID；
- `access_keys`：active AccessKey，按 ID 升序；`protocols` 是其 filter 与当前 data-plane
  enabled protocols 的有效交集，空 filter 表示全部已启用协议。

计数与 AccessKey 列表在同一个 service read snapshot 内取得；runtime available 计数使用同一
次 registry snapshot，避免单个响应内部重复观察。

## 16. Home 统计接口

### 16.1 路由

```http
GET /api/home/statistics?range=24h
GET /api/home/statistics?range=30d
Authorization: Bearer <AUTH_KEY>
```

只允许一个 `range` 参数，值只能是 `24h` 或 `30d`。缺省为 `24h`。重复参数、未知参数和
未知值 fail closed。

### 16.2 响应

```json
{
  "code": 0,
  "message": "Success",
  "data": {
    "range": "24h",
    "granularity": "hour",
    "from_ms": 1785315600000,
    "to_ms": 1785402000000,
    "observed_at_ms": 1785399104000,
    "summary": {
      "request_count": 16204,
      "success_count": 16058,
      "failure_count": 146,
      "total_tokens": 9200000,
      "estimated_cost_nano_usd": "58200000000",
      "usage_missing_count": 9,
      "partial_count": 3,
      "unpriced_request_count": 31
    },
    "series": [
      {
        "bucket_start_ms": 1785315600000,
        "bucket_end_ms": 1785319200000,
        "request_count": 604,
        "failure_count": 2
      }
    ],
    "rankings": {
      "models": [
        {
          "model": "sonnet-4.5",
          "group": {
            "id": 7,
            "name": "claude-pool",
            "deleted": false
          },
          "request_count": 3104,
          "total_tokens": 2100000,
          "estimated_cost_nano_usd": "18400000000"
        }
      ],
      "groups": [
        {
          "group": {
            "id": 7,
            "name": "claude-pool",
            "deleted": false
          },
          "request_count": 4860,
          "total_tokens": 3200000,
          "estimated_cost_nano_usd": "24100000000"
        }
      ],
      "access_keys": [
        {
          "access_key": {
            "id": 1,
            "name": "生产网关",
            "deleted": false
          },
          "request_count": 9840,
          "total_tokens": 5100000,
          "estimated_cost_nano_usd": "28440000000"
        }
      ]
    }
  }
}
```

### 16.3 删除实体

usage_stats 不建立维度快照表，也不保存历史名称。查询时 left join 当前 Group 和 AccessKey：

```json
{
  "id": 7,
  "name": null,
  "deleted": true
}
```

`group_id == 0`、`access_key_id == 0` 或当前表查不到记录均按 deleted 返回。前端只显示本地化
“已删除”。模型不是独立实体；空 client model 返回空字符串，前端显示“未知模型”。

### 16.4 一致性与限制

- summary、series 和三个 rankings 必须在同一个 SQLite read transaction 中读取；
- series 数量必须严格为 24 或 30；
- `request_count == success_count + failure_count`；
- 所有计数非负并限制在 JavaScript safe integer；
- fixed-point cost 必须非负且不溢出 int64；
- rankings 每类最多 5 行；
- API 不返回 breakdown link、query URL 或健康数据。

## 17. 统计持久化模型

### 17.1 RequestLog

RequestLog 保留用户请求模型和上游实际模型用于诊断，但统计只读取用户请求模型：

- `client_model`：统计维度；
- `upstream_model`：只用于请求日志诊断；
- 不在 usage_stats 增加 `upstream_model`。

RequestLog 的 token 字段无论是否定价都持久化。成本字段改为
`estimated_cost_nano_usd INTEGER NOT NULL DEFAULT 0`。

### 17.2 UsageStat

`usage_stats` 按小时聚合，核心字段：

```text
bucket_start_ms                 INTEGER NOT NULL
access_key_id                  INTEGER NOT NULL
group_id                       INTEGER NOT NULL
model                          TEXT NOT NULL
request_count                  INTEGER NOT NULL
success_count                  INTEGER NOT NULL
failure_count                  INTEGER NOT NULL
uncached_input_tokens          INTEGER NOT NULL
cache_read_tokens              INTEGER NOT NULL
cache_write_5m_tokens          INTEGER NOT NULL
cache_write_1h_tokens          INTEGER NOT NULL
output_tokens                  INTEGER NOT NULL
estimated_cost_nano_usd        INTEGER NOT NULL
usage_missing_count            INTEGER NOT NULL
partial_count                  INTEGER NOT NULL
unpriced_request_count         INTEGER NOT NULL
```

唯一索引：

```text
(bucket_start_ms, access_key_id, group_id, model)
```

usage_stats 不对 Group 或 AccessKey 建外键，避免删除配置时级联删除历史统计。

### 17.3 写入规则

RequestLog 与 UsageStat 在同一个批次、同一个 SQLite transaction 中写入：

- `status == success` → success `+1`
- `status in (error, incomplete, canceled)` → failure `+1`
- 每行 request `+1`
- `usage_state == complete|partial` 时累计已有 token；
- `usage_state == partial` 时 partial `+1`；
- `usage_state == missing` 时 token 和成本为零，usage_missing `+1`，不同时计为 unpriced；
- `usage_state == not_applicable` 时 token 和成本为零，不计 usage_missing 或 unpriced；
- complete/partial 有 token 但没有匹配价格时成本为零，unpriced `+1`，已有 token 仍累计；
- Group ID、AccessKey ID 或 client model 缺失时仍写入，分别使用 `0` 或空字符串；
- 不再因 Group ID 为零或模型为空跳过统计。

批处理队列、1 秒 flush、最大 batch 和优雅 drain 保持现有架构。用户已确认不增加 outbox 或
额外丢失补偿。

### 17.4 成本计算

模型价格也改为 fixed-point。管理 API 的五类价格字段统一为可空十进制字符串：

```text
input_price_usd_per_million_tokens
output_price_usd_per_million_tokens
cache_read_price_usd_per_million_tokens
cache_write_5m_price_usd_per_million_tokens
cache_write_1h_price_usd_per_million_tokens
```

存储列使用对应的 `*_nano_usd_per_million_tokens INTEGER NULL`。pricing 使用标准库精确
十进制解析和溢出检查，不用 float 做累计，不新增 decimal 依赖。

每一类 token 独立按下式计算后求和：

```text
round_half_up(tokens * price_nano_usd_per_million_tokens / 1_000_000)
```

无法定价时返回 unpriced，不影响 token。

### 17.5 保留

- request_logs 继续遵循当前可配置保留天数，默认 7 天；
- usage_stats 固定保留 35 天，覆盖 30 天窗口和 5 天运维缓冲；
- 每小时 retention sweep 同时清理过期 usage_stats；
- retention 只按 `bucket_start_ms` 整数比较。

## 18. Schema migration 与无兼容策略

schema version 从 2 升级到 3。运行时只支持 v3，不保留 v2 API 或 model 双轨。

迁移在事务内完成：

1. 配置表的 `created_at`、`updated_at` 等时间转换为 `_ms INTEGER`；
2. RequestLog 转换时间和 fixed-point cost；
3. ModelPrice 转换为 fixed-point price；
4. 删除 UpstreamKey 中未被运行时使用的 `request_count`、`tokens_total`、`cost_total`；
5. 丢弃旧 usage_stats；
6. 创建新的 usage_stats；
7. 从仍保留的 request_logs 回填可恢复的小时统计；
8. 更新 schema_info 到 3。

任何转换、约束或回填失败都回滚整个 migration 并拒绝启动。不存在静默部分升级。由于旧
usage_stats 没有 AccessKey 与 client model 维度，超过 request log 保留期的旧聚合不会保留；
用户已确认不为可重建统计引入额外历史兼容机制。

配置、Group、UpstreamKey、AccessKey 和价格规则必须正常迁移，不得因“无兼容”而清空凭据或
配置。

## 19. 现有 API 的关系

### 19.1 Home 不再使用

Home 不再调用：

- `GET /api/health`
- `GET /api/groups`
- `GET /api/usage`
- `GET /api/system/info`
- `GET /api/access-keys/options`

这些接口是否保留由其他页面真实消费者决定：

- `/api/health` 继续服务 Monitor；
- `/api/groups` 继续服务 Groups、Import 和配置流程；
- `/api/usage` 继续服务 Monitor usage；
- `/api/system/info` 继续服务 Settings；
- `/api/access-keys/options` 若所有消费者迁移后为零则删除，不保留 dead API。

### 19.2 时间合同同步

继续保留的管理 API 同步改为 `_ms` 时间合同，前端所有 projector 和页面一次性迁移。不得
只让 Home 使用 Unix 毫秒而其他页面继续解析 RFC 3339。

### 19.3 Usage monitor

`/api/usage` 可以继续提供筛选和完整 token 分类，但底层改用新 usage_stats，并改为
`from_ms`/`to_ms`、`*_ms` 和 fixed-point cost。Home 不复用其宽泛 breakdown 响应。

## 20. 开发环境与 API client

### 20.1 Vite proxy

`pnpm dev` 默认把以下路径代理到 `http://127.0.0.1:3001`：

- `/api`
- `/health`
- `/v1`
- `/v1beta`

目标可用 `VITE_DEV_PROXY_TARGET` 覆盖，仅影响开发服务器。前端 API client 和连接配置始终
使用相对路径/`window.location.origin`，生产构建不内嵌开发目标。

### 20.2 移除 fake hostname

API client 不再使用 `https://gpt-load.invalid` 作为 URL parser sentinel。控制面相对路径由
显式 path/query validator 校验，禁止 scheme、host、userinfo、fragment、反斜线和协议相对
路径。这样保留 fail-closed 安全边界，同时不在代码和调试信息中出现无业务意义的域名。

## 21. 错误与安全

- 基础接口失败：显示可重试页面错误，不猜测欢迎/正常形态；
- 统计初次失败：零值结构 + 明确可重试错误；
- 统计轮询失败：保留最后成功快照；
- reveal 失败：不复制部分配置，不显示明文，不声称成功；
- clipboard 失败：提供明确错误，不把明文回填到页面；
- NextChat 窗口被拦截：报告失败，不再次 reveal；
- 所有 query 响应通过运行时 projector 严格校验；
- AccessKey 明文不进入 query cache、全局 store、DOM、URL query、日志或错误；
- 状态仍使用图标、文字和颜色共同表达；
- 所有控件具有键盘操作、focus-visible 和可访问名称。

## 22. 前端测试与质量边界

### 22.1 必须删除或保持不存在

- `*.test.ts`、`*.spec.ts`、组件测试；
- Vitest/Jest 前端配置、依赖和脚本；
- Playwright、E2E、visual baseline、evidence；
- 只服务前端测试的 fixture、helper 和 `create*ForTesting` export；
- CI、Makefile、README 和仓库规则中的前端测试命令。

### 22.2 仍执行

- Prettier format check；
- ESLint；
- Vue/TypeScript type-check；
- Vite production build；
- `git diff --check`。

这些命令只证明代码可静态检查和构建，不表述为“前端测试通过”。不把人工浏览器检查改造成
自动化验收脚本。

### 22.3 Go 测试

`internal/webui` 中测试 Go 路由、静态资源嵌入、容器或发布合同的 Go 测试属于后端/交付
测试，继续保留。删除的只是其中对已废弃前端测试命令的文本断言。

## 23. 后端 TDD 覆盖

实施时每个后端行为先写失败测试，再写最小实现。至少覆盖：

1. schema v2→v3 成功、回滚、时间转换和配置保留；
2. ModelPrice fixed-point 解析、舍入、溢出和空价格；
3. RequestLog complete、partial、missing、unpriced token/cost；
4. success/error/incomplete/canceled 计数不变量；
5. Group/AccessKey/model 缺失仍写 aggregate；
6. 小时 UPSERT 的复合维度隔离和 batch replay 幂等；
7. 24 个小时与 30 个日 bucket 的边界和稠密零点；
8. 三个 top-5 的排序、tie-break 和删除实体映射；
9. Home base read snapshot、available runtime count 和 model 去重；
10. Home statistics 参数白名单、响应契约和 safe integer；
11. reveal 的 `_ms`、no-store 与明文不泄漏；
12. 全局 `_ms` API 契约及旧 RFC 3339 字段不存在；
13. usage_stats 35 天 retention；
14. Vite 构建产物仍由 Go 单二进制正确嵌入。

定向测试完成后执行与改动相关的 Go tests，交付门禁执行：

```bash
go test -race -count=1 . ./internal/...
make check
```

`make check` 中只能包含前端静态/构建门禁和 Go tests，不得重新加入前端 test/e2e。

## 24. 实施阶段拆分边界

本节只确定后续计划的并行边界，不授权现在实施。

1. Backend storage/statistics：
   schema v3、fixed-point、requestlog 聚合、查询、retention。
2. Backend Home/control：
   base/statistics API、路由、DTO、全局时间响应迁移。
3. Frontend foundation：
   tokens、shell、layout、i18n、time/number formatter、API projector、Vite proxy。
4. Frontend pages：
   Home、Welcome、Login、Groups collection、Connection、Trend、Ranking。
5. Integration：
   删除旧 Home 组件与 dead API、同步 Monitor/Settings 时间与成本契约、构建和 Go 门禁。

同一文件在同一时间只由一个 subagent 拥有。后端分支按 TDD；前端 subagent 不创建或运行测试。
主 agent 负责接口合同、冲突解决、集成和最终证据。

## 25. 验收标准

### 25.1 视觉

1. Home、Welcome、Login 与对应模板在相同视口下的颜色、宽度、顶栏、字体层级、间距、
   圆角、边线和响应式结构一致。
2. 所有页面最大宽度统一为 1120px。
3. 品牌统一为 `GPT-Load`。
4. 导航字体和字重与模板一致，包含可工作的“分组”入口。
5. 24h/30d 在桌面和移动端都位于 KPI 右上，不落到 KPI 下方。
6. 登录 AUTH_KEY 自动聚焦，显示/隐藏和错误反馈不移动 sheet。

### 25.2 首页行为

1. 仅无 Group 且无 UpstreamKey 时显示欢迎页。
2. 已有配置时统计为空也显示完整首页和零值。
3. 顶部没有日期、健康状态或异常明细，量词为“个密钥”。
4. 30 秒轮询只请求 statistics；基础信息不轮询。
5. range 切换原子更新整个统计区域。
6. 趋势无静态图例和时间文字，只在 hover/focus/touch 显示基础信息。
7. 三个排行各最多 5 条、纯文本、无链接。
8. 失败为零和未定价为零时不显示对应辅助文案。

### 25.3 后端与数据

1. Home 使用独立 base/statistics 接口，不拼旧接口。
2. usage_stats 维度包含 AccessKey、Group 和 client model，不包含 upstream model。
3. 未定价请求 token 正常累计，cost 为零。
4. 删除维度无需额外历史表，left join 缺失时返回 deleted。
5. 数据库与管理 API 的绝对时间全部为 Unix 毫秒 `_ms`。
6. fixed-point cost 从定价到日志、聚合、API 无 float 累计。
7. 后端测试覆盖第 23 节矩阵并取得真实通过证据。

### 25.4 连接与安全

1. 页面不显示 Base URL、局域网模拟、模型或协议列表。
2. origin 自动取当前站点，Vite dev 的数据面代理可用于手工调试。
3. masked key 统一为 `sk-gl-••••••••` + 4 位 suffix。
4. 可见 snippet 不使用 `$GPT_LOAD_API_KEY`。
5. 复制和一键导入通过 reveal 获取明文，但明文不进入 DOM/cache/store/log。
6. NextChat 真实打开并传入配置才算成功，演示动画不算交付。

### 25.5 过程门禁

1. 本规格经用户明确确认；
2. 后续实施计划经用户明确确认；
3. 之后才创建 worktree 和分支；
4. 实施使用 subagent；
5. 后端严格 TDD；
6. 前端不编写、不运行任何测试；
7. 未取得真实门禁证据前，不声称完成、可提交或可合并。
