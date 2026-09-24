# 贡献指南 / Contributing

感谢你愿意为 GPT-Load 出一份力。本文只覆盖人类贡献者需要知道的部分；仓库内的 `AGENTS.md` / `CLAUDE.md` 是给 coding agent 的详细工程约定，不需要通读。

Thanks for helping improve GPT-Load. This document covers what a human contributor needs. The `AGENTS.md` / `CLAUDE.md` files in the repository are detailed engineering rules for coding agents — you do not need to read them.

## 开始之前 / Before you start

- **安全漏洞不要提 issue**，请使用 [私有漏洞报告](https://github.com/tbphp/gpt-load/security/advisories/new)，详见 [SECURITY.md](SECURITY.md)。
- 使用类问题请先查阅[官方文档](https://www.gpt-load.com/docs)或到 [Telegram 群](https://t.me/+GHpy5SwEllg3MTUx)交流，不要开 issue。
- 较大的改动请先开 issue 讨论方向，避免完成后才发现方案不合适。

- **Never open a public issue for a vulnerability** — use [private vulnerability reporting](https://github.com/tbphp/gpt-load/security/advisories/new); see [SECURITY.md](SECURITY.md).
- For usage questions, check the [official docs](https://www.gpt-load.com/docs) or the [Telegram group](https://t.me/+GHpy5SwEllg3MTUx) first.
- For substantial changes, open an issue to discuss the approach before writing code.

## 版本线 / Release lines

`2.x` 与 `1.x` 是两套不兼容的实现，数据不互通。

- **2.x 开发基于 `main` 分支**，PR 也提交到 `main`。
- `1.x` 处于维护状态，只接受安全和严重缺陷修复。

`2.x` and `1.x` are incompatible implementations and do not share data. Base 2.x work on the **`main` branch** and target your PR at `main`. The `1.x` line is in maintenance and only takes security and critical bug fixes.

## 本地开发 / Local development

需要 Go（版本以 `go.mod` 为准）和 Node.js + pnpm（通过 corepack 调用）。

Requires Go (see `go.mod`) and Node.js with pnpm (invoked through corepack).

```bash
make dev     # 构建管理 UI 并以 race 检测运行 / build the web UI and run with race detection
make build   # 构建 UI 与二进制 / build the UI and the binary
make test    # 运行 Go 单元测试 / run Go unit tests
make check   # 完整验收门禁 / the full acceptance gate
```

`make check` 覆盖 gofmt、`go mod tidy -diff`、`go vet`、前端 lint / format / build、Go 构建与全量单元测试。发版工具的 Python 测试可单独运行 `python3 -m unittest discover -s scripts -p 'test_release_*.py'`；浏览器 E2E 仅在手动发版验收及其模拟模式运行。

`make check` covers gofmt, `go mod tidy -diff`, `go vet`, web lint / format / build, and the Go build and unit tests. Run the release tool's Python tests separately with `python3 -m unittest discover -s scripts -p 'test_release_*.py'`. Browser E2E runs only through manual release acceptance or its simulation mode.

`third_party/cpaembedded` 是独立 Go module，**不在 `make check` 覆盖范围内**。改动该目录时请额外执行：

`third_party/cpaembedded` is a separate Go module and is **not covered by `make check`**. When changing it, also run:

```bash
cd third_party/cpaembedded
go mod tidy -diff
go vet ./...
```

该 module 的 race 测试由 CI 负责，按仓库约定不在本地运行。

Race tests for that module run in CI; per repository convention they are not run locally.

## 维护者发版验收 / Maintainer release acceptance

本地 `make release` 是维护者在 PR 合并后使用的交互式发版入口。它从最新 `origin/main` 建立隔离工作区，执行三数据库新装与旧版升级、Compose、流式请求、新旧管理界面主要页面，以及 `gpt-load/mini` 隔离副本和一次真实上游请求。全部通过后才建议 tag；输入 `yes` 确认后推送，现有 Release workflow 随 tag 按原配置运行，手动工具不等待其完成，也不重复执行 `make check`。调用者工作区和本地 `main` 不会被切换或改写；若本地发版工具代码已落后于远端，则先停止并提示更新工具。

在 PR 合并前，可从已提交且干净的当前分支运行 `make release-simulate`。它以当前 `HEAD` 运行同一套验收（包含 mini 真实上游请求），保存报告和截图，结束时不进入 tag 输入或推送。未提交的改动不会进入隔离工作区，因此模拟模式会直接拒绝脏工作区。Playwright 依赖和测试独立放在 `scripts/release-e2e/`，不改变常规前端安装与 CI/Release 门禁。

该命令需要 Docker、GitHub CLI、Go、Node/corepack，以及维护者本机已配置的 DBX `gpt-load/mini`、`gpt-load/pg` 和 `gpt-load-mini-test-db` 辅助脚本。真实数据仅复制到带归属标记的临时 PostgreSQL 库；本机报告位于 `~/.cache/gpt-load/release-acceptance/`，不得上传其中的日志或浏览器 trace。失败时先核对报告与远端 tag 状态，不要强制改写 tag。

`make release` is an interactive maintainer command used after PRs have merged. It accepts the latest `origin/main` in an isolated worktree and checks three database drivers, upgrades from the previous release, Compose, streaming, both management interfaces, and an isolated copy of the maintainer's `gpt-load/mini` data with one live upstream request. It suggests a tag only after all checks pass; pushing requires an explicit `yes`. The existing Release workflow runs after the tag push; this tool neither waits for it nor repeats `make check`. The caller's checkout and local `main` are left untouched.

Before merging the PR, run `make release-simulate` from a clean branch with a committed `HEAD`. It runs the same acceptance checks, including the live upstream request, and saves the report and screenshots without prompting for or pushing a tag. It rejects uncommitted changes because the isolated worktree cannot include them. Playwright dependencies and tests live independently under `scripts/release-e2e/`, leaving regular web installs and CI/Release gates unchanged.

The command requires Docker, GitHub CLI, Go, Node/corepack, and the maintainer's configured DBX connections and `gpt-load-mini-test-db` helper. Local reports under `~/.cache/gpt-load/release-acceptance/` can contain sensitive diagnostics and browser traces; do not upload them. If it fails, inspect the report and remote tag state before retrying.

## 提交 PR / Submitting a pull request

1. 从 `main` 切出分支，保持改动聚焦，不要夹带无关重构或格式化。
2. 修复缺陷或改变行为时，优先补一个能复现问题的测试。
3. 提交前跑一次 `make check`；无法运行时在 PR 里说明原因和未验证范围。
4. 按 `.github/pull_request_template.md` 填写，如实勾选自查清单。
5. 改动涉及用户可见能力时，**三份 README（`README.md`、`README_CN.md`、`README_JP.md`）必须同步更新**。

1. Branch from `main`, keep the change focused, and avoid unrelated refactors or reformatting.
2. For bug fixes and behavior changes, add a test that reproduces the problem first.
3. Run `make check` before submitting; if you cannot, say why and what remains unverified.
4. Fill in `.github/pull_request_template.md` honestly, including the checklist.
5. When a change affects user-visible capabilities, **all three READMEs (`README.md`, `README_CN.md`, `README_JP.md`) must be updated together**.

## Commit 规范 / Commit convention

格式为 `<type>(scope): <summary>`，`scope` 可选。常用 type：`feat`、`fix`、`refactor`、`docs`、`test`、`chore`。

Format: `<type>(scope): <summary>`, with an optional scope. Common types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`.

```text
fix(subscription): 避免临时刷新失败锁死凭据
feat(gateway): 支持 OpenAI Embeddings API
```

## 代码风格 / Code style

- UTF-8、LF 换行、文件末尾保留换行；`.go` 与 `Makefile` 用 Tab，其余用 2 空格。权威配置见 `.editorconfig`。
- Go 使用 gofmt 格式；import 分组顺序为 stdlib → 第三方 → `gpt-load/internal/...`。
- 代码标识符使用英文，注释优先使用简体中文。
- 对外错误消息走 `internal/platform/i18n`，不要在 handler 里硬编码用户可见文案。

- UTF-8, LF endings, trailing newline; Tabs in `.go` and `Makefile`, 2 spaces elsewhere. `.editorconfig` is authoritative.
- Go code is gofmt-formatted; imports are grouped stdlib → third-party → `gpt-load/internal/...`.
- Identifiers are in English; comments are written in Simplified Chinese.
- User-facing error messages go through `internal/platform/i18n`, never hardcoded in handlers.

## 依赖与安全 / Dependencies and security

- **新增依赖需要先在 issue 或 PR 中说明理由**，并注意其许可证是否兼容。
- 提交、日志、测试数据和截图中不得包含 `AUTH_KEY`、`ENCRYPTION_KEY`、上游密钥或任何真实凭据。

- **New dependencies need justification** in an issue or PR, along with license compatibility.
- Never include `AUTH_KEY`, `ENCRYPTION_KEY`, upstream keys, or any real credential in commits, logs, fixtures, or screenshots.

## 行为准则 / Code of conduct

参与本项目即表示你同意遵守 [行为准则](CODE_OF_CONDUCT.md)。

By participating, you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).
