# S6 final review corrective wave 1 report

Accepted findings implemented:

1. Direct Settings, Group Settings, Group Models, and new Group Import actions now gate late resolve/reject effects on the captured current controller and clear controllers on unmount/invalidation.
2. Pending direct saves disable mutable Settings, Group Settings, HeaderRules, and Group Models editors.
3. `GroupProtocol` and `AccessProtocol` are distinct; `openai-response` is retained only by AccessKey filters and recovery rejects it for Group Import drafts.
4. Group Settings watches refreshed Group props: clean drafts fully rebase and dirty drafts retain local ownership while accepting the refreshed saved base; own save rebases from its returned Group.
5. Structured conflict/in-use guards require nonempty arrays, safe positive IDs, and nonblank names.
6. Mobile text-like controls use shared 1rem form typography without resizing checkbox/radio controls.
7. Query retry and AppSelect options use 44px minimum touch targets.
8. Programmatic AppDialog renders no trigger when absent; Group Settings captures Save focus and restores it after URL confirmation close.
9. Import protocol options use `var(--radius-tag)` rather than `999px`.
10. QueryFeedback disables spinner animation under reduced-motion preference.

Rejected item confirmed: no generic runtime DTO projections or schema framework were added; Settings/SystemInfo remain the intentional allowlist boundaries.

## RED/GREEN evidence

- `final-wave-1-guards-red.log`: structured unsafe-ID guard failed before production tightening.
- `final-wave-1-guards-green.log`: Group control API guards passed after tightening.
- `final-wave-1-pending-red.log`: pending Settings mutable-control assertion failed before disabled propagation.
- `final-wave-1-focused-green.log`: 47 focused Settings/Group/Import/API tests passed.
- `final-wave-1-a11y-red.log` / `final-wave-1-a11y-green.log`: shared mobile/touch/reduced-motion source assertions went RED then GREEN.
- `final-wave-1-web-full.log`: full format/lint/type-check/test/build/CSP Chromium gate passed (366 tests, two Chromium smoke tests).

## Files and tests

Key implementation: `web/src/api/control/types.ts`, `groups.ts`; Settings/Group/Import feature components; shared editors/dialog/select/feedback; `web/src/styles/base.css`.

Updated tests: `groups.test.ts`, `RequestForwardingSection.test.ts`, AccessKey fixture tests, and new `accessibility-styles.test.ts`.

## Validation

Passed:

- focused web tests (47 tests)
- `pnpm format`, `pnpm lint`, `pnpm type-check`, `pnpm test` (366 tests), `pnpm build`, `pnpm test:csp` (2 Chromium tests)
- `go test ./internal/control ./internal/webui`
- `go build -o gpt-load .`
- `go test -race . ./internal/...`
- `go vet ./...`
- `git diff --check`
- diff secret-canary grep

## Self-review and residual manual needs

No backend, dependency, schema, Monitor, S7, or M4 scope was added. Manual follow-up remains advisable for mobile visual coverage at 375/768/1024/1440 and keyboard confirmation focus in a real browser. The final commit is recorded in the repository history.
