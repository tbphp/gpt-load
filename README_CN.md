# GPT-Load

[English](README.md) | 中文 | [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load 是一个用 Go 构建的自托管 AI API Key 聚合与原生协议网关。它通过单个内嵌管理 UI 的二进制，管理 OpenAI、Anthropic、Gemini 及其兼容上游的密钥，并分别暴露三家的原生数据面端点。

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
> 2.0 当前处于 release-ready 收口阶段，但这不表示 `v2.0.0` tag、GitHub Release、二进制或容器镜像已经公开发布。部署前请核对实际可用的 release artifact；不要把仓库分支状态当作发布成功证据。

2.0 是与 1.x 数据不兼容的 greenfield rewrite。`main` 继续承载 1.4.x 维护线；2.0 不会自动移动 `latest`，稳定容器通道使用显式的 `2` / `2.0` / `v2.0.0` tag。

## 2.0 能力

- **双平面**：数据面保留服务商原生路径；管理 API 统一位于 `/api`，管理 UI 内嵌在同一个 Go 二进制中。
- **三种原生方言**：OpenAI、Anthropic、Gemini 请求分别按对应协议转发，不做协议互转。
- **密钥与流量管理**：Group、加密上游 Key、AccessKey、模型发现、筛选与限流、调度、健康状态、cooldown、blacklist 和自动权重。
- **控制与可观测性**：运行设置、路由检查、健康视图、RequestLog，以及中文、英文、日文管理 UI。
- **用量与估算成本**：采集三种方言可获得的 usage，提供 24 小时/30 天汇总、明细质量状态、内置价格和用户价格覆盖。

价格和成本是基于上游返回 usage 与当前价格规则的 best-effort **估算**，不是 billing ledger、发票或供应商账单，也不会对历史请求重新计价。

## 2.0.0 支持边界

- 只保证**单应用实例**正确性，不支持多实例协调。
- 只支持 **SQLite**；不支持 PostgreSQL、MySQL 或其他数据库。
- Group 由 AccessKey 和运行时配置选择，不出现在数据面 URL 中。
- 上游密钥必须静态加密，不允许明文回退；2.0.0 不支持主密钥轮换，`migrate-keys` 仍是明确失败的延后命令。
- 不支持 1.x 数据自动迁移、原地升级或反向同步。
- 不提供协议转换、在线账单对账、自动价格抓取、在线备份 API 或备份 CLI。

## 快速开始

### Docker Compose

2.x 的 Compose 发布契约使用 `ghcr.io/tbphp/gpt-load:2`、容器内 `/app/data` 和 named volume `gpt-load-data`，绝不使用 `latest`。执行前先检查当前 checkout：

```console
cp .env.example .env
docker compose config
```

只有当解析结果满足以下条件时，才继续：image 为 `ghcr.io/tbphp/gpt-load:2`，`DATA_DIR=/app/data`，`DATABASE_DSN=/app/data/gpt-load.db`，且 `/app/data` 使用 named volume。若解析结果仍是 `latest` 或 host bind mount，不要把该 Compose 文件用于 2.0 生产部署，也不要自行改用 `latest`。

满足前置条件后：

```console
docker compose up -d
curl --fail http://localhost:3001/health
# 首次自动生成 AUTH_KEY 时，只在安全终端读取一次并立即保存到 secret manager。
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

默认 named volume 会保存 SQLite、`auth.key` 和 `encryption.key`。生产环境建议通过受保护的 secret 注入显式 `AUTH_KEY` 与 `ENCRYPTION_KEY`；不要把真实 secret 提交到 `.env`、日志或 issue。自定义容器 `DATABASE_DSN` 时，必须通过 Compose override 同时提供**容器内**路径和匹配的 volume mount。

### 原生二进制

公开发布后，从 GitHub Release 下载与平台匹配的 artifact，并先校验 `SHA256SUMS`。在 release artifact 尚未出现前，可按“构建与验证”从当前 checkout 构建；不要假定文件已经发布。

以下以 Linux amd64 artifact 为例：

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
DATA_DIR=./data ./gpt-load-linux-amd64
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
| OpenAI | `POST /v1/chat/completions` | OpenAI Chat Completions 原生请求 |
| OpenAI / Anthropic | `GET /v1/models` | 默认返回 OpenAI 格式；携带 `anthropic-version` 时返回 Anthropic 格式 |
| Anthropic | `POST /v1/messages` | Anthropic Messages 原生请求 |
| Gemini | `GET /v1beta/models` | Gemini 原生模型列表 |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini 非流式生成 |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Gemini 流式生成 |

GPT-Load 不把一种方言转换为另一种方言。Group 由 AccessKey 与运行时配置选择，不作为 URL 路径段传入。

## 管理面与用量成本

管理 UI 位于 `/`，管理 API 位于 `/api`；两者都使用 `AUTH_KEY`。UI 包含 Group、上游 Key、AccessKey、运行设置、健康、日志、路由检查、Usage 与模型价格管理。完整管理 API 以当前代码和 UI 为准，本 README 不复制容易漂移的路由清单。

Usage/Cost 质量边界：

- `complete + priced` 请求进入默认 token 与估算成本汇总。
- `missing`、`partial` 与 `unpriced` 仍进入请求数和对应质量计数，但不进入默认 token/成本汇总；`complete + unpriced` 也不会被猜价。
- 流式连接 clean EOF 不代表一定获得完整 usage；兼容中转站也可能不返回官方终态 usage。
- 修改模型价格只影响后续写入，不重算历史 RequestLog 或 UsageStat。
- 当前进程的 dropped/write-failure 计数与数据库窗口内的耐久汇总是不同口径。

## 核心配置

| 变量 | 默认值 | 用途 |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTP 监听地址 |
| `PORT` | `3001` | HTTP 监听端口 |
| `DATA_DIR` | `./data` | 原生进程的持久目录；容器发布契约固定为 `/app/data` |
| `DATABASE_DSN` | `${DATA_DIR}/gpt-load.db` | SQLite 路径/DSN；容器路径必须存在于容器命名空间并有匹配 volume |
| `AUTH_KEY` | 自动生成 keyfile | 管理 bearer 凭据；显式值不能包含空白，留空时读取或创建 `${DATA_DIR}/auth.key` |
| `ENCRYPTION_KEY` | 自动生成 keyfile | 加密上游 Key 的主密钥；留空时读取或创建 `${DATA_DIR}/encryption.key` |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | 优雅停机超时，单位为秒 |
| `READ_TIMEOUT` | `60` | 读取完整请求的最长时间，单位为秒 |
| `IDLE_TIMEOUT` | `120` | keep-alive 空闲超时，单位为秒 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose 停机预算；必须大于应用优雅停机超时 |
| `LOG_LEVEL` | `info` | 应用日志级别 |
| `LOG_FORMAT` | `text` | 日志格式：`text` 或 `json` |

完整的进程配置说明见 [`.env.example`](.env.example)。连接、首字节、请求、流式空闲超时和 RequestLog 保留期属于运行设置，由管理 UI/API 管理，不是额外环境变量。

## 持久化与安全

- 默认 `${DATA_DIR}` 同时包含 SQLite、`auth.key` 和 `encryption.key`；这三类资产必须作为一组保护和备份。
- 丢失或替换 `encryption.key` 会使已加密上游 Key 无法解密。2.0.0 没有自动修复或主密钥轮换。
- 使用外部 `DATABASE_DSN` 或显式 secret 时，必须单独纳入备份；“备份 DATA_DIR”不再覆盖这些外部资产。
- SQLite 使用 WAL。备份前先停止入口流量，发送 `SIGTERM` 并等待进程正常退出，再整体复制持久化资产；不要在运行时只复制 `gpt-load.db`。
- 不要把 AUTH_KEY、ENCRYPTION_KEY、AccessKey 或上游 Key 粘贴到日志、公开 issue、截图或普通备份清单中。

正式运维事实源是 Notion teamspace「GPT-Load 2.0」的「🚀 运维部署」分类下的[《GPT-Load 2.0 部署、备份恢复与 1.x 切换 Runbook》](https://app.notion.com/p/3a95e49ce6ae813db7f9c7d6b8d83f02)。

## 从 1.x 切换

2.0 不能打开、导入或原地升级 1.x 数据库，也不能复用 1.x `DATA_DIR`。推荐流程是：

1. 保持 1.x 运行并先验证其备份可恢复。
2. 为 2.0 使用独立端口、`DATA_DIR` / named volume 和数据库。
3. 手工重建最小 Group、上游 Key、AccessKey 与规则，在隔离环境验证三方言、日志及 usage/cost。
4. 在维护窗或小流量阶段切换入口；失败时停止 2.0 并切回原 1.x，不把 2.0 新数据反向导入 1.x。

`latest` 不是 1.x → 2.0 的安全升级通道。切换、备份、恢复和回滚步骤以正式 Runbook 为准。

## 构建与验证

基线：Go `1.25.12`、Node.js `>=24.11.0`、pnpm `11.15.1`。

构建内嵌管理 UI 的单二进制：

```console
make build
```

完整的本地质量门禁：

```console
corepack pnpm --dir web install --frozen-lockfile
corepack pnpm --dir web run lint
corepack pnpm --dir web run format
corepack pnpm --dir web run type-check
corepack pnpm --dir web run test
corepack pnpm --dir web run build
go build -o gpt-load .
go test -race . ./internal/...
corepack pnpm --dir web run test:e2e
```

2.0.0 预期提供五个原生 raw binary 和一个 `SHA256SUMS`：

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

这些是发布契约中的预期名称，不代表当前已经存在可下载的 GitHub Release。

## 许可证与安全

GPT-Load 使用 [MIT License](LICENSE)。安全漏洞请按 [SECURITY.md](SECURITY.md) 中的流程报告。
