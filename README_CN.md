# GPT-Load

[English](README.md) | 中文 | [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load 是一个用 Go 构建的自托管多渠道 AI 网关。它通过单个内嵌管理 UI 的二进制管理渠道预设和加密凭据，并对外提供 OpenAI、Anthropic 与 Gemini 兼容端点。

已发布的 1.4.x 维护线文档请访问[官方文档](https://www.gpt-load.com/docs?lang=zh)。

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp%2Fgpt-load | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>

## 赞助商

<table>
<tbody>
<tr>
<td width="180"><a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory"><img src="./screenshot/teamorouter.png" alt="TeamoRouter" width="150"></a></td>
<td>感谢 TeamoRouter 赞助了本项目！TeamoRouter 是企业级 Agentic LLM gateway，让开发者、AI 团队和企业可以通过一个统一 API 访问 Claude Code、Codex、Gemini CLI 和其他 AI agents，无需分别订阅，并可享受最高 90% 的折扣。它连接官方提供商和 OpenAI、Anthropic、Vertex、Azure、AWS Bedrock 等可信合作伙伴，提供经过验证的 Agent protocol 兼容性、请求可追踪性、接近官方的 TTFT、99.6% SLA 以及最高 5,000 QPM。它还包含集中计费、团队管理、BYOK、smart routing、analytics、provider optimization 和专属支持。Teamo Desktop 支持一键设置，无需管理 API key 或手动配置，新用户可通过<a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory">此链接</a>注册，首充可享 10% 折扣。</td>
</tr>
<tr>
<td width="180"><a href="https://unity2.ai/register?source=gptload"><img src="./screenshot/unity2ai.jpg" alt="Unity2.ai" width="150"></a></td>
<td>感谢 Unity2.ai 赞助了本项目！Unity2.ai 是面向个人开发者、团队和企业的高性能 AI 模型 API 中转平台，长期服务国内头部企业，日均承载超 300 亿 token 调用，支持 5000 RPM 级高并发。支持余额计费、首充赠额、组合订阅、企业开票和专属对接。通过<a href="https://unity2.ai/register?source=gptload">此链接</a>注册可领取 $2 余额，加入官方群再送 $10 余额，最高可领 $12 免费额度。</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>非常感谢 LINUX DO 社区的支持！</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean Referral Badge" width="150"></a></td>
<td>本项目由 DigitalOcean 支持。</td>
</tr>
</tbody>
</table>

## 2.0 发布状态

> [!WARNING]
> 2.0 当前是**发布前本地候选**。M3/M4 候选代码及本地验证留存证据已经存在，但正式出口和发布仍未完成；目前没有证据表明 `v2.0.0` tag、GitHub Release、公开二进制或公开容器镜像已经可用。checkout 或分支状态不能作为发布证据。

2.0 是与 1.x 及早期预发布 2.x 数据均不兼容的 greenfield rewrite。预发布开发阶段必须使用空的独立数据库和匹配的新 encryption key。`main` 继续承载 1.4.x 维护线。发布契约预留显式的 `2`、`2.0`、`v2.0.0` 容器 tag，且不会自动移动 `latest`；这些名称本身不代表镜像已经发布。

## 2.0 能力

- **双平面**：数据面保留服务商原生路径；管理 API 统一位于 `/api`，管理 UI 内嵌在同一个 Go 二进制中。
- **四种客户端协议**：继续提供 OpenAI Completions、OpenAI Responses、Anthropic Messages 与 Gemini 入口。每个渠道明确声明可原生执行或转换的 operation；不支持的能力组合会在发送前拒绝。
- **渠道与流量管理**：用户只需搜索并选择代码内置渠道，再填写模型和加密凭据。GPT-Load 继续负责 AccessKey 筛选、跨 Group 的凭据调度、重试决策、健康状态、cooldown、blacklist 与自动权重。
- **Provider 执行**：官方 Bifrost Core Go SDK 负责不同 Provider 的认证、请求/响应转换、流式、usage 归一化和错误解析。每个逻辑 attempt 由 GPT-Load 固定一个凭据，并关闭 Bifrost 可配置的重试和 fallback。
- **Codex 订阅**：OpenAI Group 可选择 API Key，或通过浏览器 OAuth/CPA JSON 接入 Codex 订阅账号。订阅账号继续复用 GPT-Load 现有调度、重试、健康、权重和亲和机制；凭据始终加密，账号权益只在用户主动同步时请求上游。
- **控制与可观测性**：运行设置、路由检查、健康视图、RequestLog，以及中文、英文、日文管理 UI。
- **用量与估算成本**：对四种协议中会返回生成 usage 的接口进行采集，提供 24 小时/30 天汇总、明细质量状态、可用时从 Models.dev 同步的精确四槽模型价格，以及用户管理的价格。

M3 控制面 UI 与 M4 用量/定价范围已经进入本地候选，但正式出口与公开发布尚未完成。价格和成本是基于上游返回 usage 与当前价格规则的 best-effort **估算**，不是 billing ledger、发票或供应商账单，也不会对历史请求重新计价。

## 2.0.0 支持边界

- 只保证**单应用实例**正确性，不支持多实例协调。
- 通过统一的 `DATABASE_DSN` 同时支持 SQLite、MySQL 和 PostgreSQL；当前版本面向这三种数据库的最新稳定版，不支持其他数据库产品。
- Group 由 AccessKey 和运行时配置选择，不出现在数据面 URL 中。
- AccessKey 的客户端协议筛选使用 `openai-completions`、`openai-responses`、`anthropic`、`gemini`。Group 只保存一个 `channel_id`；代码中的渠道预设统一决定 Provider 行为、客户端协议、凭据结构、模型发现与 Models.dev 映射。
- OpenAI Responses 资源路由暂时没有 Credential 亲和。使用 `previous_response_id` 或 `conversation` 的有状态多轮，以及后续 retrieve/delete/cancel/input-items 请求，只有在单 Credential 或上游跨 Credential 共享资源存储时才可靠；否则可能由被选中的上游返回资源不存在。
- 渠道凭据必须静态加密，不允许明文回退；2.0.0 不支持主密钥轮换，`migrate-keys` 仍是明确失败的延后命令。
- 不支持从 1.x 或早期预发布 2.x schema 自动迁移或反向同步。当前基线只初始化空数据库；后续结构变更从该基线继续追加迁移。
- 不提供在线账单对账、在线备份 API 或备份 CLI。Models.dev 同步只提供估算元数据，不是服务商账单或发票。

## 快速开始

### Docker Compose

当前 2.x Compose 候选契约引用 `ghcr.io/tbphp/gpt-load:2`、容器内 `/app/data` 和 named volume `gpt-load-data`。这只是本地契约，不证明公开镜像已经可用；契约不使用 `latest`。执行前先检查当前 checkout：

```console
cp .env.example .env
docker compose config
```

只有当解析结果满足以下条件时才继续：image 为 `ghcr.io/tbphp/gpt-load:2`；**容器内**环境为 `HOST=0.0.0.0`、`DATA_DIR=/app/data`；`DATABASE_DSN` 保持空/未设置，由进程在运行时选择 managed `/app/data/gpt-load.db`；**宿主机**发布地址为 `${BIND_ADDRESS:-127.0.0.1}`；`/app/data` 使用 named volume。服务不固定 `container_name`，不同实例通过 Compose project name 隔离。若公开镜像不可用，应启用注释中的本地 build override，不能假定镜像已经发布。

满足上述配置及 image/build 可用性前置条件后：

```console
docker compose up -d
curl --fail http://localhost:3001/health
# 首次自动生成 AUTH_KEY 时，只在安全终端读取一次并立即保存到 secret manager。
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

默认 named volume 会保存 SQLite、`auth.key` 和 `encryption.key`。生产环境建议通过受保护的 secret 注入显式 `AUTH_KEY` 与 `ENCRYPTION_KEY`；不要把真实 secret 提交到 `.env`、日志或 issue。MySQL 或 PostgreSQL 的 `DATABASE_DSN` 属于 operator 管理配置，应通过部署 secret/configuration system 提供。

Compose 仅在容器内部监听所有接口，默认仍只发布到宿主 loopback；同时会把 Codex OAuth 客户端固定的回调端口发布到 `127.0.0.1:1455`。除非已有明确的受控网络边界，否则必须保持回环地址。由于该端口由 OAuth 客户端固定，同一宿主机同一时刻只能有一个容器执行浏览器授权。设置 `BIND_ADDRESS=0.0.0.0`、`OAUTH_CALLBACK_BIND_ADDRESS=0.0.0.0`，或为原生进程设置 `HOST=0.0.0.0`，都属于显式 opt-in；生产环境只能在受控网络边界、TLS reverse proxy 及 ACL/firewall 保护下暴露。

### 原生二进制

公开发布后，从 GitHub Release 下载与平台匹配的 artifact，并先校验 `SHA256SUMS`。在 release artifact 尚未出现前，可按“构建与验证”从当前 checkout 构建；不要假定文件已经发布。

以下以 Linux amd64 artifact 为例：

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

另一个终端验证：

```console
curl --fail http://localhost:3001/health
```

然后在浏览器打开 <http://localhost:3001>。

`AUTH_KEY` 与 `ENCRYPTION_KEY` 都可以显式设置。留空时，首次启动分别在 `${DATA_DIR}/auth.key` 与 `${DATA_DIR}/encryption.key` 创建并复用；应用只记录生成文件的路径，不记录 secret 内容。

## 原生数据面

数据面请求使用 AccessKey。按照服务商惯例，可通过 `Authorization: Bearer`、`x-api-key`、`x-goog-api-key` 或 Gemini 的 `key` 查询参数传递凭据。

| 服务商 | 方法与路径 | 行为 |
|---|---|---|
| OpenAI | `POST /v1/chat/completions` | OpenAI Completions 原生请求 |
| OpenAI | `/v1/responses` 与 `/v1/responses/...` | OpenAI Responses 原生命名空间；普通 HTTP method 直接转发 |
| OpenAI / Anthropic | `GET /v1/models` | 默认返回 OpenAI 格式；携带 `anthropic-version` 时返回 Anthropic 格式 |
| Anthropic | `POST /v1/messages` | Anthropic Messages 原生请求 |
| Gemini | `GET /v1beta/models` | Gemini 原生模型列表 |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini 非流式生成 |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Gemini 流式生成 |

Group 由 AccessKey 与运行时配置选择，不作为 URL 路径段传入。被选中的渠道决定一次 operation 使用原生执行还是协议转换；转换必须通过能力门禁，不承诺任意 JSON 的无损互转。

规范的客户端协议筛选值与展示名如下：

| 配置值 | 展示名 |
|---|---|
| `openai-completions` | OpenAI Completions |
| `openai-responses` | OpenAI Responses |
| `anthropic` | Anthropic |
| `gemini` | Gemini |

内置 `openai` 渠道支持两种 OpenAI 客户端协议。其他官方、常用中转、兼容和云渠道只暴露代码预设中明确声明的 operation 与能力；用户不再为每个 Group 配置协议复选框。

Responses 路由按命名空间边界匹配，不维护资源接口白名单。AccessKey 认证完成后，`/v1/responses` 及其普通子路径都会进入同一套调度和执行管线；已解码的 `.` 或 `..` 路径段会在本地拒绝，避免路径规范化或重定向逃逸已授权命名空间。`OPTIONS`、`CONNECT`、`TRACE` 也在本地拒绝，其他 method（包括 `GET`、`POST`、`DELETE`、`HEAD`）直接转发。路径与 query 在 Go URL 规范化边界内保留：已解码的 `URL.Path` 会重新编码，`RawPath` 不保留。GPT-Load 不会根据资源 ID 跨 Credential 查找；被选中上游的响应（包括资源不存在错误）会经过统一响应安全边界后返回。

渠道支持 Responses 的 Group 可以不配置模型，并继续服务不含 model 的 Responses 资源接口；含 model 的请求（包括常规 create）仍需存在可匹配的模型路由。

> [!WARNING]
> 2.0.0 尚未实现 Responses 亲和。携带 `previous_response_id` 或 `conversation` 的有状态多轮，以及对既有 response ID 的资源操作，可能命中不同 Group/Credential 并收到上游 404。在亲和完成前，请使用单 Credential、`store: false` 的无状态 item replay，或使用跨 Credential 共享资源存储的上游。

Responses create 与 compact 请求参与 usage 抽取；retrieve、delete、cancel、input-items、input-token-count 及未知扩展子路径在 RequestLog 中记录为 usage `not_applicable`。模型发现与验活由渠道预设决定，不再依赖用户选择协议。

OpenAI Completions 示例：

```console
curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","messages":[{"role":"user","content":"Hello"}]}'
```

Responses 示例：

```console
curl http://127.0.0.1:3001/v1/responses \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","input":"Hello","store":false}'
```

OpenAI 官方 SDK 可以直接使用同一个原生端点：

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3001/v1",
    api_key=os.environ["GPT_LOAD_ACCESS_KEY"],
)
response = client.responses.create(
    model="<MODEL_ID>",
    input="Hello",
    store=False,
)
print(response.output_text)
```

## 管理面与用量成本

管理 UI 位于 `/`，管理 API 位于 `/api`；两者都使用 `AUTH_KEY`。UI 包含可搜索渠道目录、Group、加密凭据、AccessKey、运行设置、健康、日志、路由检查、Usage 与模型价格管理。完整管理 API 以当前代码和 UI 为准，本 README 不复制容易漂移的路由清单。

模型目录自动同步默认启用，只在控制面访问固定端点 `https://models.dev/api.json`；启动过程保持异步，并可使用持久化的 last-known-good 目录。手动同步始终可用，数据面请求不会访问 Models.dev。

Usage/Cost 质量边界：

- `complete` 与 `partial` usage 的已知 token 维度进入汇总；`missing` usage 只进入请求数和质量计数。
- `priced` 请求的已知估算成本进入汇总；`pricing_partial` 会保留可计算部分并标记价格覆盖不完整，`unpriced` 请求不会被猜价。
- 流式连接 clean EOF 不代表一定获得完整 usage；兼容中转站也可能不返回官方终态 usage。
- 价格按 `(channel_id, 上游模型)` 精确匹配。Models.dev 是唯一自动价格源，同时保留用户显式覆盖和现有 fallback 规则。四个平面价格槽为输入、输出、缓存读取和缓存写入；显式 `0` 表示免费，未设置表示不可估算。
- 修改模型价格只影响后续写入，不重算历史 RequestLog 或 UsageStat。
- 当前进程的 dropped/write-failure 计数与数据库窗口内的耐久汇总是不同口径。

## 核心配置

| 变量 | 默认值 | 用途 |
|---|---|---|
| `HOST` | `127.0.0.1` | 原生 HTTP 监听地址；`0.0.0.0` 是显式 opt-in，发布容器仅在内部覆盖为 `0.0.0.0` |
| `BIND_ADDRESS` | `127.0.0.1` | Compose 宿主机侧发布地址，不是进程配置 |
| `OAUTH_CALLBACK_BIND_ADDRESS` | `127.0.0.1` | Compose 中固定 Codex OAuth 回调端口 `1455` 的宿主机发布地址 |
| `PORT` | `3001` | HTTP 监听端口 |
| `DATA_DIR` | `./data` | 原生持久目录；容器内覆盖为 `/app/data` |
| `DATABASE_DSN` | 空 → `${DATA_DIR}/gpt-load.db` | 空值选择 managed SQLite；非空值必须使用统一的 `sqlite`、`mysql` 或 `postgres` URL，并由 operator 管理 |
| `AUTH_KEY` | 自动生成 keyfile | 管理 bearer 凭据；显式值不能包含空白，留空时读取或创建 `${DATA_DIR}/auth.key` |
| `ENCRYPTION_KEY` | 自动生成 keyfile | 加密渠道凭据的主密钥；留空时读取或创建 `${DATA_DIR}/encryption.key` |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | 未设置 | Models.dev 自动同步的可选严格布尔覆盖；未设置时使用运行设置，其默认启用 |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | 优雅停机超时，单位为秒 |
| `READ_TIMEOUT` | `60` | 读取完整请求的最长时间，单位为秒 |
| `IDLE_TIMEOUT` | `120` | keep-alive 空闲超时，单位为秒 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose 停机预算；必须大于应用优雅停机超时 |
| `LOG_LEVEL` | `info` | 应用日志级别 |
| `LOG_FORMAT` | `text` | 日志格式：`text` 或 `json` |

完整的进程配置说明见 [`.env.example`](.env.example)。原生响应/流式首事件、请求总时长、流式空闲超时和 RequestLog 保留期属于运行设置，由管理 UI/API 管理，不是额外环境变量。SDK 不提供响应开始信号的转换型非流式请求只使用请求总时长。

`DATABASE_DSN` 使用统一 URL 约定，不增加数据库类型变量或按数据库拆分的应用配置。示例：

```text
sqlite:///var/lib/gpt-load.db
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

空值是唯一由应用管理数据库文件的模式；非空 SQLite 路径/URL 以及所有 MySQL/PostgreSQL URL 都由 operator 管理。

## 持久化与安全

- 数据库归属只由 raw `DATABASE_DSN` 决定：空值表示 `${DATA_DIR}` 下 managed SQLite DB/WAL/SHM；任何非空值都表示 operator 管理的 external 数据库，GPT-Load 不为其 mkdir、chmod、创建数据库或用户，必须单独备份。
- secret 归属与数据库归属相互独立。`/api/system/info` 会分别报告 secret source：无论数据库是哪种 source，`key_file` 都必须归档 `DATA_DIR` 中对应的 `auth.key` / `encryption.key`；`environment` 则必须从受保护的外部 secret system 单独恢复。
- POSIX 下 managed `${DATA_DIR}` 权限收紧为 `0700`，managed DB/WAL/SHM 及应用创建的 key 文件为 `0600`。Windows 使用当前用户专属 ACL，但当前候选尚未执行 Windows runtime 停机/ACL 门禁。
- 无论来自哪种 source，丢失匹配的 `encryption.key` 都会使已加密渠道凭据无法恢复。2.0.0 没有自动修复或主密钥轮换。
- Managed SQLite 使用 WAL。备份前先停止入口流量并等待 clean exit：POSIX 使用 `SIGTERM`，Windows 使用 Ctrl+C、Ctrl+Break 或 service manager stop。MySQL/PostgreSQL 必须按 operator 的数据库原生备份流程执行。
- 不要把 AUTH_KEY、ENCRYPTION_KEY、AccessKey 或渠道凭据粘贴到日志、公开 issue、截图或普通备份清单中。

### 公开运维基线

以下清单可独立使用，不需要访问项目的私有 Notion 工作区：

1. 从实际 environment、service 或 container 配置判断数据库 source 与位置，再通过管理认证调用 `GET /api/system/info`，记录已选择的数据库驱动以及每个 secret 的安全 source/path 元数据但不记录 secret 值。该端点刻意不返回数据库 DSN、凭据或位置。
2. 停止入口流量，按上面的 POSIX 或 Windows 方式等待进程正常退出。使用 Compose 时执行 `docker compose stop`，并确认服务容器已经停止。
3. 沿两个正交维度组成完整恢复资产：`DATABASE_DSN` 为空时归档 managed DB/WAL/SHM，非空时按 operator 流程单独备份 external DB；两种数据库场景都必须归档 auth/encryption 的每个 `key_file`，并从受保护的外部 secret system 恢复每个 `environment` secret。归档名必须唯一、禁止覆盖、限制访问并记录 SHA-256。
4. 使用完全相同的二进制或镜像，在空目标中同时恢复数据库与 secret 两部分。先校验 checksum，并恢复完全匹配的 encryption key；不要把恢复与升级合并为一步。
5. 启动恢复实例并验证 `/health`、`/api/system/info`、Group、AccessKey、模型价格、Usage、RequestLog 和真实数据面 canary。Managed SQLite 停机后执行 `PRAGMA quick_check`，结果必须为 `ok`；MySQL/PostgreSQL 使用 operator 的原生一致性检查。

2.0.0 没有 backup CLI，也不支持 encryption key rotation。不得为已有数据库替换 encryption key。

## 从 1.x 切换

2.0 不能打开、导入或原地升级 1.x 数据库，也不能复用 1.x `DATA_DIR`。推荐流程是：

1. 保持 1.x 运行并先验证其备份可恢复。
2. 为 2.0 使用独立端口、`DATA_DIR`、数据库、Compose project 与 named volume，不与 1.x 共享任何一项。
3. 手工重建最小 Group、渠道凭据、AccessKey 与规则，在隔离环境验证所需客户端协议、日志及 usage/cost。
4. 在维护窗或小流量阶段切换入口；失败时停止 2.0 并切回原 1.x，不把 2.0 新数据反向导入 1.x。

`latest` 不是 1.x → 2.0 的安全升级通道。备份和恢复按上面的公开运维基线执行，并在回滚窗口关闭前保留原 1.x 部署及其数据。

## 构建与验证

基线：Go `1.26.6`、Node.js `>=24.11.0`、pnpm `11.17.0`。

构建内嵌管理 UI 的单二进制：

```console
make build
```

完整的本地质量门禁：

```console
make check
```

项目工作流不包含前端单元测试或浏览器 E2E；前端验证范围为依赖安装、lint、format、type-check 与 build。

2.0.0 预期提供五个原生 raw binary，以及 `SHA256SUMS`、CycloneDX SBOM、项目和第三方许可证声明：

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

这些是发布契约中的预期名称，不代表当前已经存在可下载的 GitHub Release。

## 许可证与安全

GPT-Load 使用 [MIT License](LICENSE)。发行物中的第三方声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，完整许可文本位于 [`LICENSES/`](LICENSES/)。安全漏洞请按 [SECURITY.md](SECURITY.md) 中的流程报告。
