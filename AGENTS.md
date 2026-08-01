# GPT-Load 本地协作说明

本说明适用于 GPT-Load 仓库中的所有 coding agent 工具；`AGENTS.md` 与 `CLAUDE.md` 必须逐字保持同步。

## 1. 项目概述

GPT-Load 是用 Go 构建的自托管 AI API 密钥聚合与原生协议网关。它管理 OpenAI、Anthropic、Gemini 及兼容上游的密钥，并通过统一服务提供数据面与管理面 API。

当前状态：`main` 保持 **1.4.x 维护线**；截至 2026-07-28，本地 `v2` 交付线已包含 M0–M4 的仓库实现与本轮 remediation 候选，**2.0.0 尚未发布**。后续状态以 Notion 正式文档和当前代码、验证证据共同核对（见「文档管理」）。

- native 进程默认监听 `127.0.0.1:3001`；容器内监听与宿主机发布边界见「运行配置」
- M1 的历史交付范围是 Go 后端数据面最小闭环，不含管理前端；当前本地交付线已经包含 M2 调度与健康、M3 控制面与管理 UI、M4 用量与成本估算实现
- 本地实现与已有本地门禁证据不等于发布证明；Windows、真实 Provider、远端 CI、GitHub 发布策略、五平台制品、真实 registry/runtime、迁移演练及 tag/Release/registry 写入等外部门禁仍未完成
- 不得把本地未提交 remediation 候选描述为已 commit、已 push、已合并、已发布或已获发布授权

## 2. 文档管理（Notion，必须遵守）

项目正式文档（设计文档 / 技术方案 / 参考资料 / 运维部署）的唯一事实源是 Notion teamspace「GPT-Load 2.0」，不存放在仓库中。文档按页面树组织（非数据库）：teamspace 根目录下固定 4 个分类页面，具体文档作为其子页面存放。

| 分类 | 用途 | 页面 |
|---|---|---|
| 📐 设计文档 | 产品设计、架构设计、功能规格 | <https://app.notion.com/p/39b5e49ce6ae812dbcd6d54b000c1c68> |
| 🔧 技术方案 | 技术选型、实施方案、迁移与重构方案 | <https://app.notion.com/p/39b5e49ce6ae81b7968dd2257a984bc8> |
| 📚 参考资料 | 外部资料、竞品调研、协议规范摘录 | <https://app.notion.com/p/39b5e49ce6ae812f9651f75a45de08b9> |
| 🚀 运维部署 | 部署指南、迁移操作、运维 runbook | <https://app.notion.com/p/39b5e49ce6ae814cb4dbcd768df4c5a4> |

读写规则：

1. **读**：先判断主题所属分类，fetch 对应分类页面（会自动列出其下全部子文档标题），再 fetch 目标文档获取全文。
2. **写**：判断正确分类后，在该分类页面下创建子页面（parent 指向该分类的 page_id）。页面树没有数据库属性，新文档内容**第一行**必须是一句话简介（置于正文标题之前），便于打开即知梗概。
3. **更新**：创建前先 fetch 对应分类页面，检查是否已有同主题文档；已存在则更新原页面，避免重复创建。
4. **边界**：仓库工程文件（README*、CLAUDE.md、AGENTS.md、SECURITY.md、代码注释）不属于正式文档；superpowers 工作流产物（specs / plans）保留在本地 `docs/superpowers/`，不进 Notion。
5. **降级**：Notion MCP 不可用时，文档暂存本地 `docs/` 并明确告知用户待同步，不得静默跳过或虚报已同步。

## 3. 仓库地图与架构

- **后端**（Go 1.25.12，Gin）：`main.go`、`internal/**`
- **管理前端**（Vue 3、Vite、TypeScript）：`web/**`；构建产物嵌入 `internal/webui/dist`
- **容器与发布运维**：`Dockerfile`、`docker-compose.yml`、`.github/workflows/**`、`.github/scripts/**`

### 后端分层（internal/）

```
app/         → 应用生命周期、进程 HTTP 服务、健康检查与优雅关闭
container/   → uber/dig 依赖装配与路由注册
gateway/     → 数据面认证、静态路由、转发、重试、流式处理与请求观测
scheduler/   → 纯逻辑路由检查、上游密钥选择与权重调度
dialect/     → OpenAI / Anthropic / Gemini 原生方言、模型与 usage 解析
protocol/    → 跨域 canonical 协议标识与数据面能力边界
state/       → ConfigSnapshot、KeyRegistry、运行时设置与持久层加载适配
health/      → 上游结果分类、失败判定、冷却/黑名单与健康统计
ratelimit/   → AccessKey RPM 限流
control/     → 管理 API、配置发布、发现、运行时维护与 M3/M4 应用服务
requestlog/  → 异步请求日志持久化、查询、留存、usage 聚合与成本估算
usage/       → provider-neutral token usage 类型与安全算术
pricing/     → 不可变模型价格规则编译与 usage 报价
telemetry/   → 数据面到请求日志运行时的最小观测接口
storage/     → SQLite 打开、权限边界、schema 与持久模型
webui/       → 嵌入式管理 UI 与显式 SPA/静态资源路由
platform/    → config / authkey / encryption / securefile / httpclient / i18n / response / errors / utils / version
testutil/    → fake upstream 与三方言测试 fixtures
```

### 架构级特性

- **双平面架构**：数据面使用原生服务商路径；管理 API 统一位于 `/api`，Vue 管理 UI 作为静态资源嵌入同一二进制
- **历史里程碑边界**：M1 不包含管理 UI；当前仓库已实现 M2 调度/健康、M3 管理面/UI 及 M4 usage/pricing，不得把当前状态回写成 M1 阶段
- **单实例 2.0.0**：只保证单应用实例正确性
- **SQLite-only 2.0.0**：空 `DATABASE_DSN` 使用受管 `${DATA_DIR}/gpt-load.db`；非空 DSN 仍为 SQLite，但位置与权限由 operator 管理
- **Store 已删除**：`internal/storage/store` 已移除，不再保留通用运行时状态 Store 层
- **强制管理认证与静态加密**：`AUTH_KEY` / `ENCRYPTION_KEY` 的非空环境值优先；为空时分别读取或生成 `${DATA_DIR}/auth.key` / `${DATA_DIR}/encryption.key`，不允许明文回退
- **用量与成本边界**：M4 提供三方言 usage、价格规则、聚合 API 与 UI；成本是 estimate，不是 invoice/ledger，价格变更不回算历史
- **发布边界**：release workflow、五目标 native 构建、multi-arch image 与 smoke 合同存在于仓库中，但只有实际远端/制品证据才能证明对应发布门禁

## 4. 运行配置与常用命令

### 静态进程配置

- native 默认：`HOST=127.0.0.1`、`PORT=3001`、`DATA_DIR=./data`
- `PORT` 必须是 `1–65535`；`GRACEFUL_SHUTDOWN_TIMEOUT=10`、`READ_TIMEOUT=60`、`IDLE_TIMEOUT=120` 的单位均为秒
- `LOG_LEVEL=info`；`LOG_FORMAT` 只接受 `text` 或 `json`
- 空 `DATABASE_DSN` 选择 application-managed SQLite；非空值一律选择 operator-managed external SQLite，即使文本上等于默认路径也不改其父目录或 DB/WAL/SHM 权限
- managed POSIX `DATA_DIR` 收紧为 `0700`，DB/WAL/SHM、`auth.key`、`encryption.key` 等受管普通文件收紧为 `0600`；Windows 使用当前用户专属 ACL。无法安全确认目录、文件、owner 或链接类型时 fail closed
- release image 与 Compose 在容器内将 `HOST` 覆写为 `0.0.0.0`、`DATA_DIR` 覆写为 `/app/data`；Compose 默认只向宿主机 `127.0.0.1:${PORT:-3001}` 发布，并使用 `gpt-load-data` 具名卷
- 将 native 或宿主机监听改为全接口属于显式暴露，生产环境必须配置 TLS reverse proxy、ACL/firewall 等网络边界

### 常用命令

```bash
make dev
make run
make build
make test
make check
```

## 5. 测试与验收

- 后端使用 Go 测试；最终完整测试固定为：
  ```bash
  go test -count=1 . ./internal/...
  ```
- 不运行 Go race 测试。
- 前端不编写或运行单元、组件、视觉、浏览器或 E2E 测试，也不要求浏览器级视觉与交互验收。
- `internal/webui` 的 Go 测试属于后端测试，继续保留。
- 统一验收门禁为：
  ```bash
  make check
  ```

## 6. 完成工作前的必要检查

声明完成前执行：

```bash
make check
```

不得追加 race、前端测试、E2E 或浏览器视觉与交互验收作为交付门禁。门禁无法执行时必须明确说明。

## 7. 代码风格

### 通用

- UTF-8、LF 换行、文件末尾保留换行。
- `.go` 与 `Makefile` 用 Tab；其余文件使用 2 空格缩进。
- 改动保持小而聚焦，不覆盖工作区中的无关改动。
- 权威配置以 `.editorconfig` 及仓库内实际工具配置为准。

### 前端（M3）

- 正式视觉事实源是 Notion《[GPT-Load 2.0 M3 前端视觉规范](https://app.notion.com/p/3a55e49ce6ae8120833dc1c7434b7aa3)》；页面内容与交互语义以《[GPT-Load 2.0 交互设计文档](https://app.notion.com/p/3a55e49ce6ae813eba04eac597a0e7c1)》为准。
- 当前本地完整视觉参考位于 `tmp/m3-frontend-reference/`；它只用于核对 Product / 锌灰风格、信息层次和排版密度，不得直接复制其中的静态 HTML、class 或内联样式作为 Vue 实现。
- 任何前端 UI 工作开始前必须先读取上述两份 Notion 文档和可用的本地参考；若本地参考缺失，以 Notion 视觉规范中的嵌入基线为准。
- 实现时将明暗主题、颜色、圆角、阴影和基础间距集中为语义 design tokens，并通过统一基础组件复用；页面和业务组件不得自行硬编码 Hex 色值或重复定义按钮、卡片、状态、表单和表格基础样式。
- 首页与 Group 卡片保持舒适密度，密钥表和请求日志等长列表只在局部收紧；状态始终同时使用图标、文字和颜色表达。
- 有意改变视觉方向、token 或全局组件规则前，必须先获得用户确认并同步更新 Notion 视觉规范，再更新实现与视觉基线。

### Go

- 使用 gofmt 兼容格式（Tab）。
- import 分组（空行分隔）：stdlib → 第三方 → 内部包（`gpt-load/internal/...`）。
- 常用别名：`app_errors "gpt-load/internal/platform/errors"`（避免与 stdlib `errors` 冲突）。
- 命名：导出标识符用 `PascalCase`，非导出标识符用 `camelCase`；struct JSON tag 用 `snake_case`；构造函数用 `NewXxx(params) *Xxx`。
- DI 参数结构体内嵌 `dig.In`，命名为 `NewXxxParams`；全部装配在 `internal/container/container.go`。
- 始终检查 `err` 并尽早返回；复用 `internal/platform/errors` 的预定义错误；响应统一走 `internal/platform/response` 助手。
- GORM 错误使用 `errors.ParseDBError(err)` 转为 `APIError`；正常控制流不用 `panic`。
- 日志使用 `logrus` 和结构化字段；handler 内优先关联请求 context。

## 8. API 与错误响应约定

- 成功：`{ code: 0, message: string, data?: any }`
- 失败：`{ code: string, message: string, data?: any }`
- 失败响应的 `data` 仅用于客户端决策所需的结构化信息，不承载偶发诊断细节。
- Handler 流程：严格解析并校验 JSON → 调用 control/service 层 → 通过 `platform/response` 助手返回。

### 当前数据面路由

- `POST /v1/chat/completions`
- `POST /v1/messages`
- `POST /v1beta/models/{model}:generateContent`（`model` 非空且不含 `/`）
- `POST /v1beta/models/{model}:streamGenerateContent`（`model` 非空且不含 `/`）
- `GET /v1beta/models`
- `GET /v1/models`（非空 `anthropic-version` header 使用 Anthropic 模型列表格式，否则使用 OpenAI 格式）
- `/v1/responses` 与 `/v1/responses/**` 按命名空间边界匹配并透传普通 HTTP method；已解码的 `.` / `..` 路径段及 `OPTIONS`、`CONNECT`、`TRACE` 在本地拒绝
- `GET /health` 是进程健康端点，不属于 provider 数据面协议

除显式注册的 `/v1/chat/completions` 外，其余 provider 路径由 `engine.NoRoute(handler.Handle)` 进入中央 resolver；认证先于路由解析。数据面使用 AccessKey 认证；Group 由 AccessKey 与运行时配置选择，不作为 URL 路径段传入。canonical 协议值为 `openai-completions`、`openai-responses`、`anthropic`、`gemini`，均为普通可多选协议；旧值 `openai`、`openai-response` 与 `openai-chat-completions` 无效且不兼容。OpenAI 仅是同时预选两个 OpenAI 协议的供应商预设。Responses 资源接口直接透传，不提供跨 Key 亲和；选择 Responses 的零模型 Group 仍可服务不含 model 的资源请求，含 model 的请求仍需模型路由。

### 当前管理面路由

所有 `/api` 管理路由都要求 `Authorization: Bearer <AUTH_KEY 或受管 auth.key>`。

- `GET /api/auth/session`
- `GET /api/home`
- `GET /api/home/statistics`
- `GET /api/health`
- `GET /api/logs`
- `GET /api/usage`
- `POST /api/route/inspect`
- `GET /api/settings`
- `PUT /api/settings`
- `GET /api/model-prices`
- `PUT /api/model-prices`
- `DELETE /api/model-prices`
- `GET /api/system/info`
- `GET /api/groups`
- `GET /api/groups/options`
- `POST /api/groups`
- `GET /api/groups/{group_id}`
- `DELETE /api/groups/{group_id}`
- `GET /api/groups/{group_id}/models`
- `PUT /api/groups/{group_id}/models`
- `GET /api/groups/{group_id}/settings`
- `PUT /api/groups/{group_id}/settings`
- `GET /api/groups/{group_id}/keys`
- `PUT /api/groups/{group_id}/keys/{key_id}`
- `POST /api/groups/{group_id}/keys/{key_id}/restore`
- `POST /api/groups/{group_id}/keys/batch`
- `DELETE /api/groups/{group_id}/keys/{key_id}`
- `POST /api/groups/{group_id}/keys/import`
- `POST /api/groups/{group_id}/models/discover`
- `POST /api/models/discover`
- `GET /api/access-keys`
- `GET /api/access-keys/options`
- `POST /api/access-keys`
- `POST /api/access-keys/{id}/reveal`
- `PUT /api/access-keys/{id}`
- `DELETE /api/access-keys/{id}`

## 9. i18n 约定

- 后端使用 `internal/platform/i18n` 中间件与翻译助手。
- 对外错误消息优先使用翻译 key，不在 handler 中硬编码用户可见文案。

## 10. 安全规则

- 未经明确批准不新增依赖。
- 未经明确确认不执行破坏性命令。
- 未被明确要求不 commit、不 push。
- 不忽略错误，不写吞掉失败的空处理逻辑。
- 涉及 `ENCRYPTION_KEY` 或未来密钥轮换的操作，先备份 SQLite 数据库与 `encryption.key` 再执行。
- 不在日志、测试输出、文档或提交中泄露明文密钥。

## 11. Pull Request 约定

- 严格遵循 `.github/pull_request_template.md`：保持双语标题与章节结构不变。
- 基于真实验证与文档更新情况如实勾选自查清单。
- PR 标题遵循 Conventional Commits 风格。
