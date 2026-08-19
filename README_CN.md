<div align="center">

<!-- 【外部资源 1｜项目 Logo】
     建议：宽 120–160px 的方形或横版 logo，深浅色背景下都清晰。
     制作完成后放到 ./screenshot/logo.png（或 web/src/assets/logo.svg），替换下面这行。 -->
<img src="./screenshot/logo.png" alt="GPT-Load" width="120">

# GPT-Load

**面向多渠道、多凭据场景的自托管 AI 网关**

把 API Key、订阅账号、流量调度、故障处理、请求日志与用量统计，收进同一个入口。

[English](README.md) · 中文 · [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

</div>

---

应用只需要配置一个地址和一个 AccessKey。后面的服务商、账号、凭据、模型与路由策略，全部在管理界面里完成。

<!-- 【外部资源 2｜架构示意图】（推荐，替换下方 ASCII 图）
     建议内容：左侧「应用 / AI 客户端」→ 中间 GPT-Load（内含：凭据池管理、调度与重试、健康检查、日志与用量）→ 右侧多个上游（官方 API / 云平台 / 兼容中转 / 订阅账号）。
     要点：突出「一个 Base URL + 一个 AccessKey」进、「多渠道多凭据」出。建议宽 900px，深浅色背景各一版更佳。
     完成后放到 ./screenshot/architecture.png，并把下面的代码块替换成图片引用。 -->

```text
应用 / AI 客户端
        │
        │  一个 Base URL + 一个 AccessKey
        ▼
     GPT-Load
        ├─ API Key 与订阅账号统一管理
        ├─ 凭据调度、重试、健康检查与故障隔离
        ├─ 请求日志、用量汇总与成本估算
        └─ OpenAI / Anthropic / Gemini 原生协议入口
        │
        ▼
官方 API、云平台、兼容中转与订阅渠道
```

## 为什么选择 GPT-Load

- **统一接入多个上游** — 官方 API、云平台、常用模型服务和 OpenAI 兼容中转，都在同一个网关里管理。
- **API Key 与订阅账号同一套机制** — Codex、Claude、Antigravity、Grok 等订阅渠道，与 API Key 渠道共享同样的凭据管理、调度与健康体系。
- **把凭据池用满** — 多凭据调度、自动权重、重试、冷却、黑名单与会话亲和，降低单个凭据过载或失效对业务的影响。
- **保留客户端原生协议** — 客户端继续使用 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 或 Gemini 原生接口，不需要改造代码。
- **每一次调用都看得见** — 健康状态、路由检查、请求日志、用量汇总与模型成本估算，便于定位问题和评估消耗。
- **部署简单、数据自持** — 管理界面内嵌在单个 Go 二进制中；默认 SQLite，也可连接 MySQL 或 PostgreSQL；渠道凭据本地加密保存。

## 界面预览

<!-- 【外部资源 3｜管理界面截图】（推荐）
     建议 2–4 张，宽 1400px 左右，浅色主题，注意打码所有真实密钥、账号与域名：
       a) 仪表盘 / 首页概览（能看到调用量与健康状态）
       b) 渠道与凭据管理（体现「多凭据」和「加密保存」）
       c) 用量与成本页面（体现统计能力）
       d) 请求日志（可选）
     完成后放到 ./screenshot/ 下，替换下面的占位。若只准备一张，保留仪表盘即可。 -->

| 仪表盘 | 用量与成本 |
|---|---|
| <img src="./screenshot/dashboard.png" alt="仪表盘"> | <img src="./screenshot/usage.png" alt="用量与成本"> |

## 支持范围

### 客户端协议

| 协议 | 主要入口 |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `/v1/responses` 及其资源路径 |
| Anthropic Messages | `POST /v1/messages` |
| Gemini | `/v1beta/models/...` |

每个渠道会明确声明自己可执行的协议与能力。GPT-Load 在受支持的能力之间做转换，但不是任意协议、任意 JSON 的通用转换器。

### 内置渠道

<details>
<summary>展开查看全部 20 个内置渠道</summary>

- **官方与云平台**：OpenAI、Anthropic、Gemini、xAI、Azure OpenAI、AWS Bedrock、Google Vertex AI
- **常用模型服务**：DeepSeek、Moonshot AI、SiliconFlow、Zhipu AI、Alibaba、Volcengine、OpenRouter、Groq
- **订阅渠道**：Codex、Claude、Antigravity、Grok
- **自定义**：OpenAI Compatible（任意兼容中转）

</details>

## 快速开始

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

控制台里的配置关系是三层：

<!-- 【外部资源 4｜配置流程图】（可选，锦上添花）
     建议内容：三步流程「① 添加渠道（填入 API Key / 完成 OAuth 授权）→ ② 创建 Group（选渠道、配模型与策略）→ ③ 创建 AccessKey（选 Group 与协议，交给应用）」。
     建议横版、宽 900px。完成后放到 ./screenshot/setup-flow.png 并替换下面的代码块。 -->

```text
渠道 ──→ Group ──→ AccessKey
凭据池     模型与策略    给应用用的密钥
```

1. **添加渠道** — 选择上游服务，填入一个或多个 API Key；订阅渠道按界面提示完成 OAuth 授权或导入凭据。
2. **创建 Group** — 选择渠道，配置可用模型与运行策略。
3. **创建 AccessKey** — 设置允许访问的 Group 与客户端协议，把生成的 AccessKey 交给应用使用。

<details>
<summary>订阅渠道的 OAuth 回调端口</summary>

Codex、Claude、Antigravity 的 OAuth 客户端使用固定回调端口，Compose 默认把它们发布到本机的 `127.0.0.1:1455`、`127.0.0.1:54545`、`127.0.0.1:51121`。因为端口由上游客户端固定，同一台机器同一时刻只能运行一个默认 Compose 实例。

如果通过 SSH 或远程浏览器操作，浏览器的 `localhost` 可能到不了 GPT-Load —— 此时把完整回调 URL 复制到授权弹窗里即可完成流程。

</details>

### 3. 发送第一个请求

把 AccessKey 和模型 ID 换成控制台里的实际值：

```bash
export GPT_LOAD_ACCESS_KEY="your-access-key"

curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer ${GPT_LOAD_ACCESS_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model-id",
    "messages": [{ "role": "user", "content": "你好，请介绍一下你自己。" }]
  }'
```

## 接入现有客户端

对 OpenAI SDK 或任何 OpenAI 兼容客户端，通常只改两项：

```text
Base URL: http://127.0.0.1:3001/v1
API Key:  GPT-Load 中创建的 AccessKey
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3001/v1",
    api_key="your-access-key",
)

response = client.responses.create(
    model="your-model-id",
    input="你好",
    store=False,
)

print(response.output_text)
```

Anthropic 客户端使用 `/v1/messages`，Gemini 客户端使用 `/v1beta/models/...`。认证方式沿用各客户端习惯即可：`Authorization: Bearer`、`x-api-key`、`x-goog-api-key` 或 Gemini 的 `key` 查询参数。

## 部署与数据

Docker Compose 默认使用应用管理的 SQLite，数据存放在 `gpt-load-data` 具名卷中，包含数据库、`auth.key` 和 `encryption.key`。

> [!IMPORTANT]
> `encryption.key` 用于解密渠道凭据。备份或迁移时，数据库和密钥**必须一起保存**；密钥丢失或被替换后，已有加密凭据无法恢复，且当前版本不支持主密钥轮换。

需要外部数据库时，通过统一的 `DATABASE_DSN` 连接 SQLite、MySQL 或 PostgreSQL：

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

完整配置说明见 [`.env.example`](.env.example)。

常用运维命令：

```bash
docker compose logs -f      # 查看日志
docker compose pull && docker compose up -d   # 更新到最新 2.x 镜像
docker compose stop         # 停止服务
```

官方 2.x Compose 使用 `ghcr.io/tbphp/gpt-load:2`，不依赖 `latest` 标签。

<details>
<summary>使用原生二进制</summary>

从 [GitHub Releases](https://github.com/tbphp/gpt-load/releases) 下载对应平台的文件，建议先用随附的 `SHA256SUMS` 校验：

```bash
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

启动后访问 <http://127.0.0.1:3001>。提供 Linux、macOS（amd64 / arm64）与 Windows 共五个平台的构建。

</details>

## 使用前请注意

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

| 项目 | 作用 | 许可证 |
|---|---|---|
| [Bifrost Core](https://github.com/maximhq/bifrost) | 各服务商的认证、请求响应转换、流式与用量归一化 | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | 订阅渠道的 OAuth 与执行适配 | MIT |
| [Lobe Icons](https://github.com/lobehub/lobe-icons) | 管理界面中的渠道品牌图标 | MIT |

GPT-Load 自身负责凭据存储、账号选择、调度、重试、健康、亲和、日志与用量策略。第三方声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，许可证全文位于 [`LICENSES/`](LICENSES/)，每个 Release 另附覆盖 Go 依赖的 CycloneDX SBOM。

各渠道图标用于标识对应的上游服务商，其商标权归各自所有者；本项目与这些服务商没有从属或背书关系。

## 反馈与贡献

遇到问题或有功能建议，欢迎提交 [GitHub Issue](https://github.com/tbphp/gpt-load/issues)。安全漏洞请按 [SECURITY.md](SECURITY.md) 的流程报告。

如果 GPT-Load 对你有帮助，欢迎点个 Star。

## 赞助与支持

<table>
<tbody>
<tr>
<!-- 【外部资源 5｜赞助商 Logo】
     当前这里引用的是管理界面里的渠道图标（第三方重绘版本）。
     由于此处表达的是「赞助 / 合作关系」而非「识别渠道」，建议改用 OpenAI 官方品牌资产，
     放到 ./screenshot/sponsor-openai.svg 后替换下面的 img src。 -->
<td width="180"><a href="https://openai.com/"><img src="./web/src/assets/channels/openai.svg" alt="OpenAI" width="150" height="50"></a></td>
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

## 许可证

GPT-Load 使用 [MIT License](LICENSE)。
