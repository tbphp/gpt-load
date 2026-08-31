<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**面向多渠道、多凭据场景的自托管 AI 网关**

把 API Key、订阅账号、流量调度、故障处理、请求日志与用量统计，收进同一个入口。

[English](README.md) · 中文 · [日本語](README_JP.md)

[官方网站](https://www.gpt-load.com)

[![Release](https://img.shields.io/github/v/tag/tbphp/gpt-load?filter=v2.*)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3Av2beta-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

应用只需要配置一个地址和一个 AccessKey。后面的服务商、账号、凭据、模型与路由策略，全部在管理界面里完成。

<img src="./screenshot/architecture-overview.png" alt="GPT-Load 统一接入与上游分流架构图" width="860">

## 赞助与支持

<table>
<tbody>
<tr>
<td width="180"><a href="https://go.apimart.ai/gh-gpt-load"><img src="./screenshot/apimart.png" alt="APIMart" width="150"></a></td>
<td>感谢 APIMart 赞助了本项目！APIMart 是专注 AI 图片/视频生成的低价 API 平台，GPT-Image-2 低至 $0.006/张，1 美元可出图 160+ 张。图片、视频一套异步 API 通吃，提交任务拿 ID、回调取结果，跑批万张不超时、换模型不改代码。按量付费、无月费，通过<a href="https://go.apimart.ai/gh-gpt-load">此注册链接</a>注册即可开用。</td>
</tr>
<tr>
<td width="180">
<a href="https://openai.com/">
<picture>
<source media="(prefers-color-scheme: dark)" srcset="./screenshot/sponsor-openai-lockup-white.svg">
<source media="(prefers-color-scheme: light)" srcset="./screenshot/sponsor-openai-lockup-black.svg">
<img src="./screenshot/sponsor-openai-lockup-black.svg" alt="OpenAI" width="150">
</picture>
</a>
</td>
<td>感谢 OpenAI 对本项目的赞助支持。</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>感谢 LINUX DO 社区的支持。</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean" width="150"></a></td>
<td>本项目由 DigitalOcean 支持。</td>
</tr>
</tbody>
</table>

## 为什么选择 GPT-Load

- **统一入口，保留原生协议** — 官方 API、云平台、模型服务和兼容中转统一管理；客户端继续使用 OpenAI、Anthropic 或 Gemini 原生接口，无需改造代码。
- **统一管理 API Key 与订阅账号** — Codex、Claude、Antigravity、Grok 等订阅渠道与 API Key 渠道共享凭据管理、调度和健康体系。
- **内置调度与故障隔离** — 多凭据调度、自动权重、重试、冷却、黑名单与会话亲和，降低单个凭据过载或失效的影响。
- **可观测、易部署、数据自持** — 提供健康、路由、日志、用量与成本估算；单个 Go 二进制内嵌管理界面，支持 SQLite、MySQL、PostgreSQL 和本地凭据加密。

## 快速开始

> [!WARNING]
> 如果你正在使用 1.x，请先阅读[从 1.x 切换](#从-1x-切换)。2.0 不能打开、导入或原地迁移 1.x 数据。

### 1. 启动服务

需要 Docker 与 Docker Compose。

```bash
git clone --depth 1 --branch v2 https://github.com/tbphp/gpt-load.git
cd gpt-load

cp .env.example .env
docker compose up -d
```

确认服务已启动：

```bash
curl --fail http://127.0.0.1:3001/health
```

首次启动会自动生成管理密钥，读取并妥善保存：

```bash
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

打开 <http://127.0.0.1:3001>，用该密钥登录控制台。

> 也可以在启动前通过 `.env` 里的 `AUTH_KEY` 显式指定管理密钥。默认只监听本机地址，不会直接暴露到公网。

### 2. 完成首次配置

首次配置只需三步：

1. **添加渠道** — 选择上游服务，填入一个或多个 API Key；订阅渠道按界面提示完成 OAuth 授权或导入凭据。
2. **创建 Group** — 选择渠道，配置可用模型与运行策略。
3. **创建 AccessKey** — 设置允许访问的 Group 与客户端协议，把生成的 AccessKey 交给应用使用。

<details>
<summary>订阅渠道的 OAuth 回调端口</summary>

Codex、Claude、Antigravity 的 OAuth 客户端使用固定回调端口。Compose 会把它们发布到 `HOST` 配置的地址，默认是 `127.0.0.1`；设置 `HOST=0.0.0.0` 时，这些回调端口也会发布到宿主机的全部网络接口。因为端口由上游客户端固定，同一台机器同一时刻只能运行一个默认 Compose 实例。

如果通过 SSH 或远程浏览器操作，浏览器的 `localhost` 可能到不了 GPT-Load —— 此时把完整回调 URL 复制到授权弹窗里即可完成流程。

</details>

## 界面预览

**分组总览** — 统一查看渠道、模型、凭据数量与健康状态

<img src="./screenshot/groups-overview.png" alt="GPT-Load 分组总览" width="860">

**订阅账号** — 查看账号可用性、额度窗口、重置时间与运行诊断

<img src="./screenshot/subscription-accounts.png" alt="GPT-Load 订阅账号与额度状态" width="860">

**访问密钥只读首页** — 使用 AccessKey 登录，只查看该密钥自己的分组、模型、请求、用量与费用额度

<img src="./screenshot/access-key-home.png" alt="GPT-Load 访问密钥只读首页" width="860">

**用量与成本** — 查看请求趋势、缓存命中率、Token 分类与成本估算

<img src="./screenshot/usage-cost.png" alt="GPT-Load 用量与成本监控" width="860">

## 支持范围

### 客户端协议

| 协议                    | 主要入口                     |
| ----------------------- | ---------------------------- |
| OpenAI Chat Completions | `POST /v1/chat/completions`  |
| OpenAI Responses        | `/v1/responses` 及其资源路径 |
| OpenAI Images           | `POST /v1/images/...`       |
| OpenAI Embeddings       | `POST /v1/embeddings`       |
| Anthropic Messages      | `POST /v1/messages`          |
| Gemini                  | `/v1beta/models/...`         |

每个渠道会明确声明自己可执行的协议与能力。GPT-Load 在受支持的能力之间做转换，但不是任意协议、任意 JSON 的通用转换器。

Embeddings 首期只在 OpenAI、OpenRouter 和 OpenAI Compatible API Key 渠道提供原生 OpenAI-compatible Wire，不支持订阅渠道或协议互转。未设置协议过滤器的 AccessKey 会按既有语义允许全部已启用协议，升级后也会获得 Embeddings 访问能力；最小权限部署请显式配置协议过滤器。

### 内置渠道

- **官方与云平台**：OpenAI、Anthropic、Gemini、xAI、Azure OpenAI、AWS Bedrock、Google Vertex AI
- **常用模型服务**：DeepSeek、Moonshot AI、SiliconFlow、Zhipu AI、Alibaba、Volcengine、OpenRouter、Groq
- **订阅渠道**：Codex、Claude、Antigravity、Grok
- **自定义**：OpenAI Compatible（任意兼容中转）

## 部署与数据

Docker Compose 默认使用应用管理的 SQLite，数据存放在 `gpt-load-data` 具名卷中，包含数据库、`auth.key` 和 `encryption.key`。

> [!IMPORTANT]
> `encryption.key` 用于解密渠道凭据。备份或迁移时，数据库和密钥**必须一起保存**；密钥丢失或被替换后，已有加密凭据无法恢复，且当前版本不支持主密钥轮换。

<details>
<summary>使用外部数据库</summary>

通过统一的 `DATABASE_DSN` 连接 SQLite、MySQL 或 PostgreSQL：

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

</details>

常用运维命令：

```bash
docker compose logs -f      # 查看日志
docker compose pull && docker compose up -d   # 更新到最新 2.x 镜像
docker compose stop         # 停止服务
```

官方 2.x Compose 使用 `ghcr.io/tbphp/gpt-load:v2beta`，不依赖 `latest` 标签。

<details>
<summary>使用原生二进制</summary>

从 [GitHub Releases](https://github.com/tbphp/gpt-load/releases) 下载对应平台的文件，建议先用随附的 `SHA256SUMS` 校验：

```bash
chmod +x ./gpt-load-linux-amd64

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

启动后访问 <http://127.0.0.1:3001>。提供 Linux、macOS（amd64 / arm64）与 Windows 共五个平台的便携构建；`gpt-load-windows-amd64.exe` 继续以前台模式运行。

Windows 普通用户可改为下载 `gpt-load-windows-setup.exe`。双击并确认管理员权限后，安装器会注册并启动低权限 Windows 服务、设置开机启动，并创建桌面和开始菜单中的 GPT-Load 管理页面快捷方式。安装过程中会显示首次生成的管理密钥，请在关闭页面前保存；密钥仍保存在 `%ProgramData%\GPT-Load\data\auth.key`。服务配置目录为 `%ProgramData%\GPT-Load` 并从其中读取 `.env`，数据目录为 `%ProgramData%\GPT-Load\data`。

覆盖安装会先优雅停止服务再更新，Windows 卸载会移除程序和服务但保留数据。高级用户仍可使用 `gpt-load-windows-amd64.exe service start|stop|restart|status` 管理已安装服务。

</details>

### 环境配置

应用启动时读取当前目录的 `.env`，已有的进程环境变量优先。除特别说明外，修改后需要重启进程或容器；常用配置模板见 [`.env.example`](.env.example)。

<details>
<summary>查看全部环境变量</summary>

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Native 模式的监听地址，也是 Compose 主端口和 OAuth 回调端口的默认宿主机发布地址；Compose 容器内部固定监听 `0.0.0.0`。 |
| `PORT` | `3001` | HTTP 服务端口，必须为 `1–65535`；Compose 同时用于容器端口、宿主机发布端口和健康检查。 |
| `BIND_ADDRESS` | 空，继承 `HOST` | 仅用于 Compose，单独覆盖主服务端口的宿主机发布地址，不改变 OAuth 回调端口。 |
| `OAUTH_CALLBACK_BIND_ADDRESS` | 空，继承 `HOST` | 仅用于 Compose，单独覆盖 OAuth 固定回调端口 `1455`、`54545`、`51121` 的宿主机发布地址。 |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | 收到停止信号后等待请求结束的最长时间，正整数，单位秒。 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose 强制停止容器前的等待时间，使用 Docker duration，建议大于 `GRACEFUL_SHUTDOWN_TIMEOUT`。 |
| `READ_TIMEOUT` | `60` | HTTP 请求读取超时，正整数，单位秒。 |
| `IDLE_TIMEOUT` | `120` | HTTP keep-alive 空闲连接超时，正整数，单位秒。 |
| `DATA_DIR` | `./data` | 托管数据库、`auth.key`、`encryption.key` 和运行状态文件的目录；官方 Compose 固定为 `/app/data`，Windows Setup 服务固定为 `%ProgramData%\GPT-Load\data`。 |
| `DATABASE_DSN` | 空，使用 `${DATA_DIR}/gpt-load.db` | 空值使用应用托管的 SQLite；非空值支持 SQLite 路径或 URL、MySQL URL、PostgreSQL URL，并视为运维方管理的外部数据库。容器内文件路径必须位于已挂载目录。 |
| `DATABASE_MAX_OPEN_CONNECTIONS` | `10` | MySQL 和 PostgreSQL 的最大打开连接数，必须为正整数；SQLite 始终使用单连接。 |
| `DATABASE_MAX_IDLE_CONNECTIONS` | `5` | MySQL 和 PostgreSQL 的最大空闲连接数，必须为正整数且不大于 `DATABASE_MAX_OPEN_CONNECTIONS`；SQLite 始终使用单连接。 |
| `AUTH_KEY` | 空，读取或生成 `${DATA_DIR}/auth.key` | 管理界面和 `/api` 管理接口的 Bearer 密钥，不是数据面 AccessKey。 |
| `ENCRYPTION_KEY` | 空，读取或生成 `${DATA_DIR}/encryption.key` | 用于加密渠道凭据；更换或丢失后无法解密已有凭据，必须与数据库一起备份。 |
| `HTTP_PROXY` | 空 | HTTP 上游请求的环境代理。 |
| `HTTPS_PROXY` | 空 | HTTPS 上游请求的环境代理。 |
| `NO_PROXY` | 空 | 逗号分隔的不经过环境代理的主机、域名或 IP。 |
| `LOG_LEVEL` | `info` | 支持 `panic`、`fatal`、`error`、`warn`、`warning`、`info`、`debug`、`trace`；无效值会告警并回退到 `info`。 |
| `LOG_FORMAT` | `text` | 支持 `text`、`json`；其他值会导致启动失败。 |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | 未设置，初始默认 `true` | 未设置时使用管理界面的持久化设置；设置后强制开启或关闭 Models.dev 自动同步，并使管理界面中的同名选项变为只读。 |

环境代理仅在凭据、Group 和全局设置都未指定代理时生效。

</details>

## 生产使用注意事项

- 默认只监听 `127.0.0.1`。需要远程访问时，应通过受控网络或带 TLS 的反向代理暴露，并配置 ACL 与防火墙。
- 妥善管理 `AUTH_KEY` 与 `ENCRYPTION_KEY`，不要把真实密钥提交到仓库、日志、截图或公开 Issue。
- 2.0 按**单应用实例**设计，多个实例之间不共享状态，不支持直接横向扩容。
- 用量与成本是基于上游返回数据的**估算**，用于运行分析和资源评估，不等同于服务商账单或财务对账结果。
- 订阅渠道依赖上游 OAuth 与兼容协议，可能随上游变化调整。请只接入自己有权使用的账号，并遵守对应服务商条款。
- OpenAI Responses 中依赖 `previous_response_id`、`conversation` 或既有资源 ID 的有状态请求，只有在单凭据或上游跨凭据共享资源时才可靠。

## 从 1.x 切换

> [!WARNING]
> GPT-Load 2.0 是完整重写的新版本，**不能**打开、导入或原地迁移 1.x 数据。

部署 2.0 时请使用独立的数据库、`DATA_DIR`、端口和 Docker 卷，验证完成后再切换业务流量，并在回滚窗口关闭前保留原 1.x 部署。1.4.x 维护线文档见[官方文档](https://www.gpt-load.com/docs?lang=zh)。

## 开源依赖

GPT-Load 的部分能力构建在这些开源项目之上，在此致谢：

| 项目                                                        | 作用                                           | 许可证     |
| ----------------------------------------------------------- | ---------------------------------------------- | ---------- |
| [Bifrost Core](https://github.com/maximhq/bifrost)          | 各服务商的认证、请求响应转换、流式与用量归一化 | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | 订阅渠道的 OAuth 与执行适配                    | MIT        |
| [Lobe Icons](https://github.com/lobehub/lobe-icons)         | 管理界面中的渠道品牌图标                       | MIT        |

GPT-Load 自身负责凭据存储、账号选择、调度、重试、健康、亲和、日志与用量策略。第三方声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，许可证全文位于 [`LICENSES/`](LICENSES/)，每个 Release 另附覆盖 Go 依赖的 CycloneDX SBOM。

各渠道图标用于标识对应的上游服务商，其商标权归各自所有者；本项目与这些服务商没有从属或背书关系。

## 反馈与贡献

遇到问题或有功能建议，欢迎提交 [GitHub Issue](https://github.com/tbphp/gpt-load/issues)。安全漏洞请按 [SECURITY.md](SECURITY.md) 的流程报告。

社区交流与使用讨论可加入 [Telegram 群组](https://t.me/+GHpy5SwEllg3MTUx)。

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

如果 GPT-Load 对你有帮助，欢迎点个 Star。

## 许可证

GPT-Load 使用 [MIT License](LICENSE)。
