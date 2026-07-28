# GPT-Load 2.0 Phase 5 外部门禁审计

审计时间：2026-07-28 21:43:43 UTC

本地审计 SHA：`e8b6fe1be3e5426034e1cfff2369bec64998170b`

远端 `v2` SHA：`3744cd5a4ebe8ec06f9c9a8f550f74737829a562`

## 结论

- 本地 Phase 3–5 分支没有 push，GitHub 无法识别该 SHA，因此精确 SHA CI 为 `NOT RUN`。
- 远端 `v2` 的 [V2 CI run 30382845830](https://github.com/tbphp/gpt-load/actions/runs/30382845830) 整体为 `FAIL`：Linux `test` job 通过，`windows-encryption-acl` job 失败。
- Windows 失败是 `TestAutoMigrateUpgradesV1AccessKeyWithBackups` 在 Windows 上得到文件 mode `0666`，却仍按 POSIX 语义要求 `0600`。更早的 [run 30329551379](https://github.com/tbphp/gpt-load/actions/runs/30329551379) 在 `6ec83da` 上通过，但已不是当前 SHA 证据。
- GitHub 没有 `v2.*` tag 或 2.x Release。GHCR 与 Docker Hub 的 `2.0.0`、`2` 标签均不存在。
- 因此五平台公开制品、双 registry 多架构 manifest、公开拉取/运行冒烟均为 `NOT RUN`。
- 真实 OpenAI/Anthropic/Gemini、迁移演练、人工 pixel 批准、Safari+VoiceOver、Windows+NVDA 仍为 `EXTERNAL`。

## 发布策略事实

仓库 `Release 2.x` workflow 只响应严格的 `v2.*` SemVer tag，并要求 tag commit 属于 `origin/v2` 历史。workflow 设计了五个原生制品、SHA256SUMS、Linux amd64/arm64 镜像、GHCR/Docker Hub 发布及发布后冒烟；这些均只是合同代码，不能替代实际执行证据。

## 状态矩阵

| 门禁                          | 状态     | 证据/原因                               |
| ----------------------------- | -------- | --------------------------------------- |
| 本地 feature 精确 SHA CI      | NOT RUN  | SHA 未 push，GitHub API 不存在该 commit |
| 当前远端 v2 CI                | FAIL     | Linux job PASS；Windows job FAIL        |
| 2.x tag / GitHub Release      | NOT RUN  | 当前公开 release 最新为 v1.4.9          |
| 五平台公开原生制品            | NOT RUN  | 无 2.x Release                          |
| GHCR 2.0.0 / 2 manifest       | NOT RUN  | public manifest not found               |
| Docker Hub 2.0.0 / 2 manifest | NOT RUN  | public manifest not found               |
| 公开镜像拉取与 runtime smoke  | NOT RUN  | 无 2.x public manifest                  |
| 迁移演练                      | EXTERNAL | 无精确 SHA operator rehearsal artifact  |
| 三家真实 Provider 流程        | EXTERNAL | 无精确 SHA live-provider evidence       |
| 人工 pixel 批准               | EXTERNAL | candidate 未人工批准，baseline disabled |
| VoiceOver / NVDA              | EXTERNAL | 无真人 AT session                       |

本次审计只读；未 push、未触发 workflow、未创建 tag/Release、未写入 registry。
