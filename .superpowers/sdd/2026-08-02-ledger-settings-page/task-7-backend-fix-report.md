# Task 7 Header Rules 后端重复身份修复报告

日期：2026-08-02

工作树：`/Users/tbphp/www/gpt-load/.worktrees/ledger-settings-page`

分支：`codex/ledger-settings-page`

实施基线：`884c85877782f46040d469bf4d41290ce38ac33f`

## 根因

`parseHeaderRules` 原先只在解析 `set` 时创建局部、ASCII 大小写不敏感的 `seen` 集合。该集合没有覆盖 `remove`，而 `remove` 分支也没有登记或检查名称身份，因此后端会接受两类无效配置：

- `remove` 内仅大小写不同的重复名称；
- `set` 与 `remove` 之间仅大小写不同的同一名称。

## RED

先在 `internal/state/runtime_settings_test.go` 新增表驱动测试 `TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities`，通过真实的 `ValidateRuntimeSetting(SettingHeaderRules, ...)` 边界覆盖上述两类输入。

执行：

```text
go test -count=1 ./internal/state -run '^TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities$'
```

- 退出码：`1`。
- 两个子用例都按预期失败：
  - `duplicate_remove_names_ignore_ASCII_case`：`ValidateRuntimeSetting() accepted duplicate Header Rule identity`；
  - `set_and_remove_names_share_one_identity`：`ValidateRuntimeSetting() accepted duplicate Header Rule identity`。
- 失败来自当前生产实现接受无效规则，不是测试编译、fixture 或环境错误。

## GREEN 与最小修复

`internal/state/runtime_settings.go` 只做以下调整：

- 将既有 `seen` 提升到 `set/remove` 两个集合共享的解析作用域；
- 保留 `set` 的名称、禁止项、重复、值和凭据模板校验顺序及原错误；
- `remove` 继续先校验类型和 Header 名称，再按 ASCII 大小写不敏感身份拒绝重复，最后保留原 canonical Header 名称输出。

首次 GREEN：

```text
go test -count=1 ./internal/state -run '^TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities$'
```

- 退出码：`0`；输出 `ok gpt-load/internal/state`。

现有 Header Rules 聚焦回归最初使用了一个过严的 `ParseHeaderRules` 精确正则；命令退出 `0`，但没有覆盖带后缀的 `TestParseHeaderRules*`，因此没有把它作为覆盖证据。随后立即修正为：

```text
go test -count=1 ./internal/state -run '^Test(ParseHeaderRules.*|ResolveRuntimeSettingsRejectsPresentNullHeaderRules|ResolveRuntimeSettingsAppliesSystemOverrides|ResolveGroupRuntimeSettingsUsesGroupPrecedence|ResolveGroupRuntimeSettingsRejectsPresentNullHeaderRules|RuntimeSettingsOwnHeaderRuleCopies|ResolveGroupRuntimeSettingsOwnsSystemHeaderRuleCopy|ResolvedGroupSettingsOwnsHeaderRuleCopies)$'
```

- 修正后的命令退出码：`0`；输出 `ok gpt-load/internal/state`。

## 格式化与最终静态证据

在最终 Go 树上依次重新执行：

1. `gofmt -w internal/state/runtime_settings.go internal/state/runtime_settings_test.go`
   - 退出码：`0`。
2. `go test -count=1 ./internal/state -run '^TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities$'`
   - 退出码：`0`；输出 `ok gpt-load/internal/state 0.311s`。
3. `go test -count=1 ./internal/state -run '^Test(ParseHeaderRules.*|ResolveRuntimeSettingsRejectsPresentNullHeaderRules|ResolveRuntimeSettingsAppliesSystemOverrides|ResolveGroupRuntimeSettingsUsesGroupPrecedence|ResolveGroupRuntimeSettingsRejectsPresentNullHeaderRules|RuntimeSettingsOwnHeaderRuleCopies|ResolveGroupRuntimeSettingsOwnsSystemHeaderRuleCopy|ResolvedGroupSettingsOwnsHeaderRuleCopies)$'`
   - 退出码：`0`；输出 `ok gpt-load/internal/state 0.219s`。
4. `git diff --check`
   - 退出码：`0`，无输出。
5. `git diff --cached --check`
   - 报告精确暂存后退出码：`0`，无输出。

## 文件与边界

- 修改 `internal/state/runtime_settings.go`。
- 修改 `internal/state/runtime_settings_test.go`。
- 新增本报告。
- 未修改前端、API shape、持久层、依赖、文档或其它运行时行为。
- 未运行前端测试、race、E2E、browser/visual 或 `make check`；符合本修复 brief 的明确边界。
