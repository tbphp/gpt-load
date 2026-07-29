# GPT-Load 2.0 Phase 5 外部门禁审计

审计时间：2026-07-29 05:38:41 UTC

审计基线 SHA：`93f4f07bbbfd6c8430be4b28d0c8fed7f981a7c4`

最终 feature SHA：`c1874b80e0792d47bb04194348d144777077b0a7`

本地 `v2` merge SHA：`06ec29fddee966828bde604d9a989e31b98ffdb8`

整改生产 SHA：`93f4f07bbbfd6c8430be4b28d0c8fed7f981a7c4`

远端 `v2` SHA：`9e39723086a537edac4c13571bed4445affeb625`

## 结论

- Phase 3–5 feature 与 merge commit 均已出现在远端 `v2` 历史中，但 GitHub Actions
  没有以这两个中间 commit 为 head SHA 的独立 run，因此它们的 exact-SHA CI 仍为
  `NOT RUN`。
- 第一轮整改提交的 [V2 CI run 30424373580](https://github.com/tbphp/gpt-load/actions/runs/30424373580)
  整体为 `FAIL`：Windows ACL job 通过，主 job 的前端、依赖审计、vet、构建与 E2E
  通过，但 race 测试暴露 `App.Stop` 在 `Serve` 尚未登记 listener 时可能提前返回。
- 本地整改生产 SHA 已用确定性回归测试修复该生命周期竞态；在本次只读审计时尚未
  push，因此其远端 exact-SHA CI 为 `NOT RUN`。
- `3d74965` 的 Windows DACL 整改已由当前远端 Windows runner 覆盖并通过，不再是
  `EXTERNAL`。
- GitHub 没有 `v2.*` tag 或 2.x Release。GHCR 与 Docker Hub 的 `2.0.0`、`2` 标签均不存在。
- 因此五平台公开制品、双 registry 多架构 manifest、公开拉取/运行冒烟均为 `NOT RUN`。
- 真实 OpenAI/Anthropic/Gemini、迁移演练、人工 pixel 批准、Safari+VoiceOver、Windows+NVDA 仍为 `EXTERNAL`。

## 发布策略事实

仓库 `Release 2.x` workflow 只响应严格的 `v2.*` SemVer tag，并要求 tag commit 属于 `origin/v2` 历史。workflow 设计了五个原生制品、SHA256SUMS、Linux amd64/arm64 镜像、GHCR/Docker Hub 发布及发布后冒烟；这些均只是合同代码，不能替代实际执行证据。

## 状态矩阵

| 门禁                          | 状态     | 证据/原因                               |
| ----------------------------- | -------- | --------------------------------------- |
| feature 精确 SHA CI           | NOT RUN  | commit 已公开，但没有 exact-head run    |
| merge 精确 SHA CI             | NOT RUN  | commit 已公开，但没有 exact-head run    |
| 当前远端 v2 CI                | FAIL     | app listener 注册竞态；Windows job 通过 |
| 整改生产 SHA CI               | NOT RUN  | 修复提交在审计时尚未 push               |
| 2.x tag / GitHub Release      | NOT RUN  | 当前公开 release 最新为 v1.4.9          |
| 五平台公开原生制品            | NOT RUN  | 无 2.x Release                          |
| GHCR 2.0.0 / 2 manifest       | NOT RUN  | public manifest not found               |
| Docker Hub 2.0.0 / 2 manifest | NOT RUN  | public manifest not found               |
| 公开镜像拉取与 runtime smoke  | NOT RUN  | 无 2.x public manifest                  |
| 迁移演练                      | EXTERNAL | 无精确 SHA operator rehearsal artifact  |
| 三家真实 Provider 流程        | EXTERNAL | 无精确 SHA live-provider evidence       |
| 人工 pixel 批准               | EXTERNAL | candidate 未人工批准，baseline disabled |
| VoiceOver / NVDA              | EXTERNAL | 无真人 AT session                       |

本次审计快照本身只读；未触发 workflow、未创建 tag/Release、未写入 registry。
