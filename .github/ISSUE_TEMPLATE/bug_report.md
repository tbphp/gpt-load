---
name: 报告问题 (Bug Report)
about: 使用简练详细的语言描述你遇到的问题 (Describe the bug in detail)
title: ""
labels: bug
assignees: ""
---

**例行检查 / Checklist**
<!-- 方框内填 'x' / Put an 'x' in all the boxes that apply -->
- [ ] 我已确认目前没有类似 issue (I have checked for similar issues)
- [ ] 我已升级到当前主版本线的最新补丁版本 (I am using the latest patch release in my current major line)
- [ ] 我已查看对应版本线的 README 和发布说明 (I have read the README and release notes for my release line)
- [ ] 我已移除 `AUTH_KEY`、`ENCRYPTION_KEY`、AccessKey、上游密钥和令牌等敏感信息 (I have removed all credentials, keys, and tokens)
- [ ] 我理解并愿意跟进此 issue，协助测试和提供反馈 (I am willing to follow up on this issue, assist with testing, and provide feedback)

---

**问题描述 / Bug Description**
<!-- 请清晰、简洁地描述问题。 / A clear and concise description of what the bug is. -->

**GPT-Load 版本 / Version**
<!-- 请填写精确版本或 commit，不要只写 latest。 / Provide the exact version or commit, not only "latest". -->

**版本线 / Release Line**
<!-- 请填写 1.x 或 2.x；二者不兼容且数据不互通。 / Specify 1.x or 2.x; they are incompatible and do not share data. -->

**部署环境 / Environment**
<!--
- 部署方式（Docker、原生二进制或源码）/ Deployment method
- 操作系统与架构 / Operating system and architecture
- 数据库及版本（SQLite、MySQL 或 PostgreSQL）/ Database and version
- 浏览器或客户端 SDK 及版本 / Browser or client SDK and version
-->

**渠道与客户端协议 / Channel and Client Protocol**
<!-- 例如 OpenAI 官方渠道 + openai-responses；不适用时填写 N/A。请勿粘贴凭据。 -->
<!-- Example: OpenAI official + openai-responses. Use N/A when not applicable. Never paste credentials. -->

**最小脱敏配置 / Minimal Sanitized Configuration**
<!-- 仅提供复现所需配置；不得包含密钥、令牌、Cookie 或完整请求头。 -->
<!-- Include only what is needed to reproduce; never include secrets, tokens, cookies, or complete authorization headers. -->

**复现步骤 / Steps to Reproduce**
<!-- 请提供复现问题的具体步骤。 / Steps to reproduce the behavior. -->
1.
2.
3.

**实际结果及脱敏日志 / Actual Result and Sanitized Logs**
<!-- 提供完整错误信息或最小日志片段，并确认已经脱敏。 / Include the exact error or minimal logs after removing sensitive data. -->

**预期结果 / Expected Behavior**
<!-- 请描述你预期的行为。 / A clear and concise description of what you expected to happen. -->


**相关截图 / Screenshots**
<!-- 如果可以，请提供相关截图以帮助说明问题。 / If applicable, add screenshots to help explain your problem. -->
