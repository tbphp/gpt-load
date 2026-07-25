# M3 S6 Core Management Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver GPT-Load M3 S6 T21–T24 as a responsive, authenticated Vue management frontend using only existing control APIs and real backend data.

**Architecture:** Keep the current Router/AuthGate/fetch/Vue Query foundation, add one injectable protected `AppShell`, and implement eight vertical review slices in dependency order. Server state remains in TanStack Vue Query, sensitive write workflows use feature-local actions, and sparse settings/config semantics are preserved exactly.

**Tech Stack:** Vue 3.5, TypeScript 6, Vue Router 5, TanStack Vue Query 5, Vue I18n 11, Vite 8, Tailwind/CSS variables, Reka UI, Lucide Vue, Vitest/happy-dom, one Chromium Playwright CSP smoke, Go 1.25/Gin embedded assets.

## Global Constraints

- Work only on S6 T21–T24; `/monitor`, full business Playwright E2E, release closure, and final M3 acceptance remain S7.
- Start implementation in an isolated worktree created with `superpowers:using-git-worktrees`; use one writer and fresh read-only reviewers.
- Do not add or change backend endpoints, Go dependencies, tables, migrations, schema versions, stores, or runtime publication semantics.
- Add only the approved frontend packages `reka-ui`, `lucide-vue-next`, and dev dependency `@playwright/test`, each pinned exactly in `web/package.json`/lockfile.
- Render only existing backend data; no usage, token, cost, price, trend, chart, success-rate, health-percentage, or synthetic model data.
- Preserve Product / zinc-gray visual direction, system fonts, semantic tokens, low shadow, clear borders, visible focus, and icon + text + color status.
- No feature/page hard-coded Hex values, external CDN, `v-html`, Axios, Pinia, Zod, VeeValidate, Storybook, generic CRUD framework, or generic schema framework.
- Locale (`zh-CN`, `en-US`, `ja-JP`) and theme (`system`, `light`, `dark`) are browser-local and never sent to `/api/settings`.
- AUTH_KEY, AccessKey plaintext, upstream keys, and HeaderRules values must never enter URLs, query keys, logs, generic errors, snapshots, or persistent query caches.
- Group/System settings remain sparse: edit `config`/`overrides`, display `effective_config`/`values`, send JSON `null` only for global Settings reset.
- Every feature/bug behavior follows RED → GREEN. Record the real failing assertion before writing production code.
- Each task ends with focused tests, the full web gate, relevant Go contract tests, an independent review, corrective re-review when needed, and one Conventional Commit with a Chinese summary.
- Do not push or merge until all eight slices, final review, full build/race verification, and user integration choice are complete.

---

## Exact API Contracts Used by the Plan

Create `web/src/api/control/types.ts` with these transport-facing shapes. Keep JSON field names exactly as returned by Go; do not rename them in the API layer.

```ts
export type GroupProtocol = "openai" | "anthropic" | "gemini";
export type AccessProtocol = GroupProtocol | "openai-response";
export type EnabledStatus = "active" | "disabled";
export type EffectiveKeyStatus =
  "available" | "cooldown" | "blacklisted" | "disabled";

export interface GroupModel {
  id: string;
  alias: string;
}

export interface HeaderRules {
  set: Record<string, string>;
  remove: string[];
}

export interface GroupRuntimeConfig {
  connect_timeout?: number;
  first_byte_timeout?: number;
  request_timeout?: number;
  stream_idle_timeout?: number;
  header_rules?: HeaderRules;
}

export interface GroupSummary {
  id: number;
  name: string;
  upstream_url: string;
  protocols: GroupProtocol[];
  models: GroupModel[];
  enabled: boolean;
  key_count: number;
}

export interface GroupEffectiveConfig {
  connect_timeout: number;
  first_byte_timeout: number;
  request_timeout: number;
  stream_idle_timeout: number;
  header_rules: HeaderRules;
}

export interface GroupDetail extends GroupSummary {
  validation_model: string | null;
  weight_manual: number | null;
  config: GroupRuntimeConfig;
  effective_config: GroupEffectiveConfig;
}

export interface KeyCounts {
  total: number;
  available: number;
  cooldown: number;
  blacklisted: number;
  disabled: number;
}

export interface HealthRecovery {
  automatic: boolean;
  mode: "cooldown_expiry" | "validation_probe";
  at: string | null;
}

export interface HealthProblemKey {
  key_id: number;
  group_id: number;
  group_name: string;
  cooldown_until?: string;
  failure_count: number;
  recent_success_count: number;
  recent_failure_count: number;
  consecutive_failure_count: number;
  weight_manual: number | null;
  weight_auto: number;
  recovery: HealthRecovery;
}

export interface RequestLogHealth {
  enqueued_total: number;
  persisted_total: number;
  dropped_not_running_total: number;
  dropped_queue_full_total: number;
  dropped_stopping_total: number;
  dropped_persist_failed_total: number;
  dropped_shutdown_total: number;
  dropped_total: number;
  write_failure_total: number;
  retention_delete_failure_total: number;
  queue_depth: number;
  queue_capacity: number;
  last_write_failure_at: string | null;
  last_retention_failure_at: string | null;
}

export interface RuntimeHealth {
  observed_at: string;
  snapshot_revision: number;
  stats_window_seconds: number;
  counts: KeyCounts;
  groups: Array<{
    id: number;
    name: string;
    enabled: boolean;
    counts: KeyCounts;
  }>;
  cooldown_keys: HealthProblemKey[];
  blacklisted_keys: HealthProblemKey[];
  request_log: RequestLogHealth;
}

export interface AccessKeyFilters {
  groups: number[];
  protocols: AccessProtocol[];
  models: string[];
}

export interface AccessKeyDto {
  id: number;
  name: string;
  key: string;
  status: EnabledStatus;
  filters: AccessKeyFilters;
  rpm_limit: number;
}

export interface UpstreamKeyDto {
  id: number;
  group_id: number;
  mask: string;
  status: EnabledStatus;
  effective_status: EffectiveKeyStatus;
  weight_manual: number | null;
  weight_auto: number;
  blacklisted: boolean;
  cooldown_until: string | null;
  failure_count: number;
}

export interface ModelDiscoveryResult {
  models: string[];
}

export interface GroupCreateResult {
  group_id: number;
  group_name: string;
  keys_added: number;
  keys_duplicated: number;
  models: GroupModel[];
}

export interface GroupKeyImportResult {
  group_id: number;
  keys_added: number;
  keys_duplicated: number;
}

export interface GroupUpdateResult {
  group: GroupDetail;
  model_rediscovery_recommended: boolean;
}

export interface UpstreamUrlConflictData {
  groups: Array<{ id: number; name: string }>;
}

export interface GroupInUseData {
  access_keys: Array<{ id: number; name: string }>;
}

export type RuntimeSettingKey =
  | "connect_timeout"
  | "first_byte_timeout"
  | "request_timeout"
  | "stream_idle_timeout"
  | "header_rules"
  | "request_log_retention_days";

export interface SettingsValues {
  connect_timeout: number;
  first_byte_timeout: number;
  request_timeout: number;
  stream_idle_timeout: number;
  header_rules: HeaderRules;
  request_log_retention_days: number;
}

export interface SettingsDto {
  revision: number;
  values: SettingsValues;
  overrides: RuntimeSettingKey[];
}

export type SettingsPatch = Partial<{
  connect_timeout: number | null;
  first_byte_timeout: number | null;
  request_timeout: number | null;
  stream_idle_timeout: number | null;
  header_rules: HeaderRules | null;
  request_log_retention_days: number | null;
}>;

export interface SecretSourceInfo {
  source: "environment" | "key_file";
  path: string | null;
}

export interface SystemInfoDto {
  version: string;
  deployment: {
    instance_mode: "single";
    database: "sqlite";
    distribution: "single_binary";
  };
  data_dir: string;
  auth_key: SecretSourceInfo;
  encryption: SecretSourceInfo & { enabled: true };
}
```

Request types are separate and encode nullable/optional semantics exactly:

```ts
export interface CreateGroupInput {
  name?: string;
  upstream_url: string;
  protocols: GroupProtocol[];
  models: GroupModel[];
  config: GroupRuntimeConfig;
  keys: string;
  confirm_same_upstream_url?: boolean;
}

export type UpdateGroupInput = Partial<{
  name: string;
  enabled: boolean;
  upstream_url: string;
  protocols: GroupProtocol[];
  validation_model: string | null;
  weight_manual: number | null;
  config: GroupRuntimeConfig;
  confirm_upstream_url_change: true;
}>;

export interface CreateAccessKeyInput {
  name: string;
  filters: AccessKeyFilters;
  rpm_limit: number;
}

export type UpdateAccessKeyInput = Partial<{
  name: string;
  status: EnabledStatus;
  filters: AccessKeyFilters;
  rpm_limit: number;
}>;

export type UpdateUpstreamKeyInput = Partial<{
  status: EnabledStatus;
  weight_manual: number | null;
}>;
```

## Planned File Boundaries

```text
web/src/app/
  AppShell.vue                 protected shell only
  query-keys.ts                stable non-secret query keys/invalidation helpers
web/src/api/
  client-context.ts            ApiClient InjectionKey
  control/types.ts             exact DTO/request types above
  control/groups.ts            Group list/detail/create/update/delete/import
  control/health.ts            RuntimeHealth read
  control/access-keys.ts       AccessKey CRUD
  control/upstream-keys.ts     Group key list/update/delete
  control/settings.ts          Settings GET/PUT
  control/system-info.ts       SystemInfo GET
web/src/components/ui/
  accessible visual primitives with cross-feature consumers
web/src/components/config/
  HeaderRulesEditor.vue        structured set/remove editor
  ModelDraftEditor.vue         promoted when T23 becomes second consumer
web/src/features/home/         T21 only
web/src/features/import/       T22 new/existing state machines and recovery
web/src/features/groups/       T23 detail shell and three tabs
web/src/features/access-keys/  T24 AccessKey management
web/src/features/settings/     T24 local appearance/server settings/system info
web/src/features/preferences/  browser-local theme controller
web/src/test/                  shared fake ApiClient/mount/storage helpers
web/e2e/                       single S6 CSP smoke and server launcher
```

## Execution Preflight (No Commit)

- [ ] Read `superpowers:using-git-worktrees`, create `feature/m3-s6-core-management-frontend` from the current `v2` HEAD, and run all implementation commands inside that worktree.
- [ ] Confirm the worktree starts clean with `git status --short --branch` and `git log --oneline -6` contains `docs(web): 设计 S6 核心管理前端`, `docs(web): 完善 S6 交互与安全约束`, and `docs(web): 制定 S6 核心前端实施计划`.
- [ ] Run the fresh baseline and record actual output:

```bash
cd web
pnpm run format
pnpm run lint
pnpm run type-check
pnpm run test
pnpm run build
cd ..
go test -race ./internal/control ./internal/webui
```

- [ ] Stop before Task 1 if the clean baseline fails; debug the baseline rather than mixing unrelated fixes into S6.

## Task 1: T21 Shared Shell, Security Foundation, and Home

**Files:**

- Create: `web/src/api/client-context.ts`
- Create: `web/src/api/control/types.ts`
- Create: `web/src/api/control/groups.ts`
- Create: `web/src/api/control/health.ts`
- Create: `web/src/api/control/access-keys.ts`
- Create: `web/src/i18n/context.ts`
- Create: `web/src/features/preferences/theme.ts`
- Create: `web/src/app/query-keys.ts`
- Create: `web/src/app/AppShell.vue`
- Create: `web/src/components/ui/PageHeader.vue`
- Create: `web/src/components/ui/IconButton.vue`
- Create: `web/src/components/ui/StatusBadge.vue`
- Create: `web/src/components/ui/QueryFeedback.vue`
- Create: `web/src/components/ui/EmptyState.vue`
- Create: `web/src/components/ui/CopyButton.vue`
- Create: `web/src/components/ui/SecretValue.vue`
- Create: `web/src/components/ui/AppSelect.vue`
- Create: `web/src/components/ui/AppDrawer.vue`
- Create: `web/src/features/home/HomeView.vue`
- Create: `web/src/features/home/GroupCard.vue`
- Create: `web/src/features/home/ConnectionCard.vue`
- Create: `web/src/features/home/home-model.ts`
- Create: `web/src/test/fake-api.ts`
- Create: `web/src/test/mount-app.ts`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/start-csp-server.mjs`
- Create: `web/e2e/csp-smoke.spec.ts`
- Modify: `web/package.json`, `web/pnpm-lock.yaml`, `.gitignore`, `.github/workflows/ci.yml`
- Modify: `web/src/main.ts`, `web/src/App.vue`, `web/src/app/router.ts`, `web/src/app/query.ts`
- Modify: `web/src/features/auth/auth-session.ts`
- Modify: `web/src/i18n/index.ts`, `web/src/env.d.ts`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`
- Modify: `web/src/styles/tokens.css`, `web/src/styles/base.css`, `web/src/test/setup.ts`, `web/src/App.test.ts`
- Remove after router migration: `web/src/views/HomeView.vue`

**Interfaces produced:**

```ts
export const apiClientKey: InjectionKey<ApiClient>;
export function useApiClient(): ApiClient;

export const appI18nKey: InjectionKey<AppI18n>;
export function useAppI18n(): AppI18n;

export type AppTheme = "system" | "light" | "dark";
export interface ThemeController {
  readonly theme: Readonly<Ref<AppTheme>>;
  setTheme(theme: AppTheme): void;
  dispose(): void;
}
export function createThemeController(
  deps: ThemeControllerDependencies,
): ThemeController;
export const themeControllerKey: InjectionKey<ThemeController>;
export function useTheme(): ThemeController;

export const controlQueryKeys: {
  all: readonly ["control"];
  groups: {
    all: readonly ["control", "groups"];
    list: () => readonly ["control", "groups", "list"];
    details: () => readonly ["control", "groups", "detail"];
    detail: (id: number) => readonly ["control", "groups", "detail", number];
    keyLists: () => readonly ["control", "groups", "keys"];
    keys: (id: number) => readonly ["control", "groups", "keys", number];
  };
  health: () => readonly ["control", "health"];
  accessKeys: { list: () => readonly ["control", "access-keys", "list"] };
  settings: () => readonly ["control", "settings"];
  systemInfo: () => readonly ["control", "system-info"];
};
```

- [ ] **Step 1: Add only the approved dependencies and scripts**

Run from repository root:

```bash
cd web
pnpm add --save-exact reka-ui lucide-vue-next
pnpm add --save-dev --save-exact @playwright/test
```

Add scripts:

```json
{
  "test:csp": "playwright test --config playwright.config.ts"
}
```

Add `web/test-results/` and `web/playwright-report/` to `.gitignore`. Do not install a browser in release workflows.

- [ ] **Step 2: Write failing context, theme, query-key, and cache-isolation tests**

Create adjacent tests with assertions equivalent to:

```ts
it("fails fast when ApiClient is not provided", () => {
  expect(() =>
    mount({ setup: () => useApiClient(), template: "<div />" }),
  ).toThrow("API_CLIENT_NOT_PROVIDED");
});

it.each(["localhost", "api.localhost", "127.0.0.1", "::1", "[::1]"])(
  "detects loopback hostname %s",
  (hostname) => expect(isLoopbackHostname(hostname)).toBe(true),
);

it("clears control queries and mutation cache on logout", () => {
  queryClient.setQueryData(controlQueryKeys.accessKeys.list(), [
    { id: 1, key: "ACCESS_KEY_CANARY" },
  ]);
  session.clear();
  expect(
    queryClient.getQueryData(controlQueryKeys.accessKeys.list()),
  ).toBeUndefined();
  expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
});
```

Theme tests must cover `system` removing `data-theme`, explicit themes setting it, storage denial, `matchMedia` failure, and listener disposal.

- [ ] **Step 3: Run the focused tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/client-context.test.ts \
  src/i18n/context.test.ts \
  src/features/preferences/theme.test.ts \
  src/app/query-keys.test.ts \
  src/features/auth/auth-session.test.ts \
  src/features/home/home-model.test.ts
```

Expected RED: missing context/theme/query-key/home modules and the existing auth session leaving non-authenticated caches intact.

- [ ] **Step 4: Implement injection, theme, exact query keys, and credential-boundary cleanup**

Use fail-fast providers:

```ts
export const apiClientKey: InjectionKey<ApiClient> = Symbol("api-client");
export function useApiClient(): ApiClient {
  const client = inject(apiClientKey);
  if (!client) throw new Error("API_CLIENT_NOT_PROVIDED");
  return client;
}
```

Implement `clearAuthenticatedClientState(queryClient)` by canceling/removing `controlQueryKeys.all`, removing the auth query, and clearing the mutation cache. Invoke it on explicit logout, global 401, and immediately before a successfully validated candidate becomes the active credential. Re-seed `['auth', 'session']` only after cleanup.

- [ ] **Step 5: Run the foundation tests and obtain GREEN**

Run the Step 3 command. Expected: all listed tests pass, including stale in-flight result suppression and secret-free query keys.

- [ ] **Step 6: Write failing AppShell, API-module, Home, and production CSP tests**

Use a provided fake `ApiClient` and three independent pending requests:

```ts
it("keeps Groups visible when Health fails", async () => {
  api.when("/api/groups").resolve([groupFixture]);
  api
    .when("/api/health")
    .reject(new ApiError(500, "INTERNAL_SERVER_ERROR", "failed"));
  api.when("/api/access-keys").resolve([]);
  const wrapper = await mountHome(api);
  expect(wrapper.get('[data-group-id="1"]').text()).toContain("Example");
  expect(wrapper.get('[data-group-id="1"]').text()).toContain("状态未知");
});

it("warns for loopback origins without blocking copy", async () => {
  const wrapper = await mountHome(api, { origin: "http://127.0.0.1:3001" });
  expect(wrapper.get('[role="note"]').text()).toContain("仅当前机器");
  expect(
    wrapper.get('[aria-label="复制 Base URL"]').attributes(),
  ).not.toHaveProperty("disabled");
});
```

API tests must assert exact method/path and AbortSignal forwarding for `GET /api/groups`, `GET /api/health`, and `GET /api/access-keys`.

Before implementing `AppShell`, also create `web/playwright.config.ts`, `web/e2e/start-csp-server.mjs`, and this failing smoke:

```ts
test("Reka overlays work under production CSP", async ({ page }) => {
  const errors: string[] = [];
  page.on(
    "console",
    (message) => message.type() === "error" && errors.push(message.text()),
  );
  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript(() =>
    localStorage.setItem("gpt-load.locale", "en-US"),
  );
  await page.goto("/login");
  await page.getByLabel("AUTH_KEY").fill("csp-auth-key");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.setViewportSize({ width: 375, height: 812 });
  const navigationTrigger = page.getByRole("button", {
    name: "Open navigation",
  });
  await navigationTrigger.click();
  await page.getByRole("button", { name: "Close navigation" }).click();
  await expect(navigationTrigger).toBeFocused();
  await page.getByLabel("Language").click();
  await page.getByRole("option", { name: "English" }).click();
  await page.getByRole("button", { name: "Use dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(errors).toEqual([]);
});
```

The launcher starts `../gpt-load` with `HOST=127.0.0.1`, a fixed test port, explicit test AUTH_KEY, and an OS temporary `DATA_DIR`, then cleans up on termination. Collect `securitypolicyviolation` through `addInitScript`.

- [ ] **Step 7: Run unit and browser tests and record RED before AppShell/Reka implementation**

```bash
cd web
pnpm exec vitest run \
  src/App.test.ts \
  src/app/AppShell.test.ts \
  src/app/router.test.ts \
  src/api/control/groups.test.ts \
  src/api/control/health.test.ts \
  src/api/control/access-keys.test.ts \
  src/features/home/HomeView.test.ts \
  src/features/home/home-model.test.ts
pnpm run build
cd ..
go build -o gpt-load .
pnpm --dir web exec playwright install --with-deps chromium
pnpm --dir web run test:csp
```

Expected unit RED: current navigation is local to the skeleton Home, mobile navigation disappears, routes use Chinese metadata/placeholders, and Home performs no real query. Expected browser RED: the authenticated page has no operable mobile Drawer, locale Select, or theme control. A CSP policy change is not an acceptable way to turn this GREEN.

- [ ] **Step 8: Implement AppShell, primitives, API modules, and real Home**

`App.vue` becomes public Login versus protected `AuthGate -> AppShell -> component`. `AppShell` owns skip link, desktop nav, Reka Drawer, Reka locale Select, theme control, import CTA, logout, localized document title, and `<main id="main-content">`.

Home helpers must implement:

```ts
export function isGroupServiceable(
  group: GroupSummary,
  counts?: KeyCounts,
): boolean | undefined {
  if (!counts) return undefined;
  return group.enabled && group.models.length > 0 && counts.available > 0;
}

export function selectInitialAccessKey(
  keys: AccessKeyDto[],
): number | undefined {
  return keys.find((key) => key.status === "active")?.id ?? keys[0]?.id;
}
```

Use three independent `useQuery` calls; AccessKey list uses `gcTime: 0`. Distinguish network-unreachable, HTTP health-unavailable, and successful/online. Do not render request-log metrics or fabricated dashboard values.

- [ ] **Step 9: Rerun the production CSP smoke to GREEN and wire CI**

Run `pnpm --dir web run test:csp`; expect Drawer, Select, theme, focus restoration, console/page errors, and `securitypolicyviolation` assertions to pass without relaxing CSP. Modify `.github/workflows/ci.yml` only after this local GREEN so CI installs Chromium and runs the smoke after shared web validation, Go setup, and binary build. Keep the reusable web action and release workflows browser-free.

- [ ] **Step 10: Run Task 1 focused and full verification**

```bash
cd web
pnpm run format
pnpm run lint
pnpm run type-check
pnpm run test
pnpm run build
cd ..
go build -o gpt-load .
pnpm --dir web exec playwright install --with-deps chromium
pnpm --dir web run test:csp
go test -race ./internal/control ./internal/webui
git diff --check
```

Modify `.github/workflows/ci.yml` to install Chromium and run `pnpm --dir web run test:csp` after Go setup/build. Leave `.github/actions/web-ci/action.yml` and release workflows browser-free.

- [ ] **Step 11: Request Task 1 review and correct findings**

Review scope: application tree, cache isolation, secret query keys, exact three-query independence, loopback variants, no fabricated metrics, Reka focus restoration, CSP, and 375/768/1024/1440 shell behavior. Rerun Step 10 after fixes.

- [ ] **Step 12: Commit Task 1**

```bash
git add web .github/workflows/ci.yml .gitignore
git commit -m "feat(web): 实现首页与共享应用壳"
```

## Task 2: T22 New-Group Import and 401 Recovery

**Files:**

- Create: `web/src/features/import/channel-presets.ts`
- Create: `web/src/features/import/key-analysis.ts`
- Create: `web/src/features/import/model-draft.ts`
- Create: `web/src/features/import/import-recovery.ts`
- Create: `web/src/features/import/use-dirty-navigation.ts`
- Create: `web/src/features/import/ImportView.vue`
- Create: `web/src/features/import/NewGroupImport.vue`
- Create: `web/src/features/import/KeyTextarea.vue`
- Create: `web/src/features/import/ModelDraftEditor.vue`
- Create: `web/src/components/config/HeaderRulesEditor.vue`
- Modify: `web/src/api/control/groups.ts`, `web/src/main.ts`, `web/src/app/router.ts`
- Modify: `web/src/features/auth/LoginView.vue`, `web/src/app/AuthGate.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`
- Modify: `web/src/i18n/locales/en-US.ts`
- Modify: `web/src/i18n/locales/ja-JP.ts`
- Modify: `web/src/test/fake-api.ts`, `web/src/test/mount-app.ts`

**Interfaces produced:**

```ts
export interface ChannelPreset {
  id: "openai" | "anthropic" | "gemini" | "custom";
  labelKey: string;
  upstream_url: string;
  protocols: GroupProtocol[];
}

export interface ImportRecoveryDependencies {
  storage?: Storage;
  now(): number;
  setTimer(
    callback: () => void,
    delayMs: number,
  ): ReturnType<typeof setTimeout>;
  clearTimer(timer: ReturnType<typeof setTimeout>): void;
}

export interface ImportRecoveryService {
  register(getDraft: () => ImportDraft | null): () => void;
  captureForUnauthorized():
    "stored" | "no-active-draft" | "storage-unavailable";
  consume(): ImportDraft | null;
  clear(): void;
  sweep(): void;
  dispose(): void;
}

export function createImportRecoveryService(
  deps: ImportRecoveryDependencies,
): ImportRecoveryService;
export const importRecoveryKey: InjectionKey<ImportRecoveryService>;
export function useImportRecovery(): ImportRecoveryService;
```

- [ ] **Step 1: Write pure failing tests for presets, key analysis, model draft, and recovery**

Lock preset values:

```ts
expect(channelPresets).toEqual([
  {
    id: "openai",
    labelKey: "import.presets.openai",
    upstream_url: "https://api.openai.com",
    protocols: ["openai"],
  },
  {
    id: "anthropic",
    labelKey: "import.presets.anthropic",
    upstream_url: "https://api.anthropic.com",
    protocols: ["anthropic"],
  },
  {
    id: "gemini",
    labelKey: "import.presets.gemini",
    upstream_url: "https://generativelanguage.googleapis.com",
    protocols: ["gemini"],
  },
  {
    id: "custom",
    labelKey: "import.presets.custom",
    upstream_url: "",
    protocols: [],
  },
]);
```

Key analysis must count empty lines and duplicates, flag `sk-gl-` lines without blocking, preserve raw input, and mark more than 1,000 non-empty lines as blocking.

Recovery tests must assert exact key `gpt-load.import-reauth-draft`, version 1, 15-minute TTL, timer cleanup, storage denial fallback, and `getItem -> removeItem -> confirm absent -> parse` ordering. Add a separate storage fake where initial `getItem` succeeds but `removeItem` throws; assert `consume()` returns `null`, does not call the parser/hydrator, and leaves authentication cleanup free to continue.

- [ ] **Step 2: Run pure tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/features/import/channel-presets.test.ts \
  src/features/import/key-analysis.test.ts \
  src/features/import/model-draft.test.ts \
  src/features/import/import-recovery.test.ts
```

Expected RED: all modules are absent.

- [ ] **Step 3: Implement pure import state and recovery primitives**

Recovery constants and consume order:

```ts
export const importRecoveryStorageKey = "gpt-load.import-reauth-draft";
export const importRecoveryTtlMs = 15 * 60 * 1000;

function removeAndConfirm(storage: Storage, key: string): boolean {
  try {
    storage.removeItem(key);
    return storage.getItem(key) === null;
  } catch {
    return false;
  }
}

function consume(): ImportDraft | null {
  const raw = safeGet(storage, importRecoveryStorageKey);
  if (!raw) return null;
  if (!storage || !removeAndConfirm(storage, importRecoveryStorageKey)) {
    return null;
  }
  const parsed = parseRecoveryRecord(raw);
  return parsed && parsed.expires_at > now() ? parsed.draft : null;
}
```

Register only the active Import draft. Do not autosave during ordinary editing.

- [ ] **Step 4: Write failing API and component tests for the three-step flow**

Assert exact calls:

```ts
expect(api.request).toHaveBeenCalledWith("/api/models/discover", {
  method: "POST",
  json: {
    upstream_url: "https://api.example.com",
    protocols: ["openai"],
    keys: "raw-key\nraw-key",
    config: { header_rules: { set: { "X-Test": "secret" }, remove: [] } },
  },
  signal: expect.any(AbortSignal),
});
```

Cover preset/Custom transitions, discovery invalidation on URL/protocol/key/HeaderRules edits, non-blocking `sk-gl-` warning, discovery failure manual models, create exact body, and all `UPSTREAM_URL_CONFLICT` choices. The “append to existing Group” branch must assert exact `POST /api/groups/:id/keys/import` body and must not call Group update or model endpoints. Before implementation, QueryClient spies must assert create success invalidates Group list/health, while conflict-append success invalidates selected Group keys/detail plus Group list/health.

Add integration RED cases for `/import`, `/import?mode=new`, and `/import?mode=existing&group_id=7`: valid recovery is preserved based on resolved route name `import`, not exact full-path equality. A non-Import safe redirect clears recovery. For both successful storage capture and denied sessionStorage, global 401 must bypass the dirty prompt, clear credential/control caches, and navigate to Login; explicit logout always clears recovery.

- [ ] **Step 5: Run component/API tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/control/groups.test.ts \
  src/components/config/HeaderRulesEditor.test.ts \
  src/features/import/ImportView.test.ts \
  src/features/import/NewGroupImport.test.ts \
  src/features/import/use-dirty-navigation.test.ts \
  src/features/auth/LoginView.test.ts
```

Expected RED: `/import` still renders a placeholder and global 401 has no pre-clear recovery hook.

- [ ] **Step 6: Implement new-Group flow and option (a) recovery wiring**

Implement `/import` and `?mode=new` as new mode. Discovery/create use feature-local `AbortController` state, never `useMutation`. On conflict, guard data:

```ts
export function isUpstreamUrlConflictData(
  value: unknown,
): value is UpstreamUrlConflictData {
  return (
    isRecord(value) &&
    Array.isArray(value.groups) &&
    value.groups.every(isIdName)
  );
}
```

Global unauthorized order is: `captureForUnauthorized()` synchronously → clear authenticated state/credential → redirect to Login. Explicit logout clears recovery. Successful Login preserves a valid recovery record when `router.resolve(safeRedirect(...)).name === 'import'`, including Import query variants; Import consumes it immediately. Removal failure returns no draft and never blocks credential/cache cleanup.

Implement `importGroupKeys()` in the Group API module during this task so the `UPSTREAM_URL_CONFLICT` action can immediately append the raw keys to a selected existing Group. Task 3 reuses that exact function for the general existing-mode page; Task 2 must not leave a visible conflict action pointing to an unimplemented destination. Create success invalidates Group list and health before routing to the new Group; conflict-append success invalidates the selected Group's keys/detail plus Group list and health.

Do not add an in-place reauthentication Dialog or new auth phase.

- [ ] **Step 7: Run Task 2 focused verification**

```bash
cd web
pnpm exec vitest run \
  src/features/import/channel-presets.test.ts \
  src/features/import/key-analysis.test.ts \
  src/features/import/model-draft.test.ts \
  src/features/import/import-recovery.test.ts \
  src/features/import/use-dirty-navigation.test.ts \
  src/components/config/HeaderRulesEditor.test.ts \
  src/features/import/ImportView.test.ts \
  src/features/import/NewGroupImport.test.ts \
  src/features/auth/LoginView.test.ts \
  src/features/auth/auth-session.test.ts \
  src/api/client.test.ts
go test -race ./internal/control -run 'Test.*(Discover|CreateGroup|URLConflict|Import.*Key)'
```

Security canary assertion: raw upstream key may appear in sessionStorage only inside the explicit simulated-401 test and is absent after confirmed consume/expiry/discard/success. If removal cannot be confirmed, no draft is parsed or hydrated.

- [ ] **Step 8: Run the full per-task gate, review, and corrections**

```bash
cd web && pnpm run format && pnpm run lint && pnpm run type-check && pnpm run test && pnpm run build
cd .. && go test -race ./internal/control ./internal/webui && git diff --check
```

Review raw key fidelity, manual fallback, conflict code/data branching, recovery lifecycle/order, dirty-navigation bypass, and no mutation cache. Rerun after corrections.

- [ ] **Step 9: Commit Task 2**

```bash
git add web
git commit -m "feat(web): 实现新分组导入流程"
```

## Task 3: T22 Existing-Group Keys-Only Import

**Files:**

- Create: `web/src/features/import/ExistingGroupImport.vue`
- Modify: `web/src/features/import/ImportView.vue`, `web/src/features/import/KeyTextarea.vue`
- Modify: `web/src/features/import/import-recovery.ts`, `web/src/features/import/key-analysis.ts`
- Modify: `web/src/api/control/groups.ts`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing existing-mode and exact-request tests**

```ts
it("imports raw keys without discovery or Group mutation", async () => {
  await mountExisting("/import?mode=existing&group_id=7");
  await fillKeys("one\ntwo");
  await submit();
  expect(api.request).toHaveBeenCalledWith("/api/groups/7/keys/import", {
    method: "POST",
    json: { keys: "one\ntwo" },
    signal: expect.any(AbortSignal),
  });
  expect(api.paths()).not.toContain("/api/models/discover");
});
```

Also cover `/import?mode=existing` selector, invalid/non-positive IDs issuing no detail request, browser back mode restoration, server `keys_added/keys_duplicated`, and recovery union restore. Before implementation, spy on `queryClient.invalidateQueries` and assert successful import requests exactly `groups.keys(7)`, `groups.detail(7)`, `groups.list()`, and `health()`—no global `control.all` invalidation.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/features/import/ExistingGroupImport.test.ts \
  src/features/import/ImportView.test.ts \
  src/features/import/import-recovery.test.ts
```

Expected RED: the general existing-mode page/component is absent even though Task 2 already introduced the exact import API for conflict handoff.

- [ ] **Step 3: Implement exact keys-only mode**

Reuse and retain the Task 2 API function without changing its request shape:

```ts
export function importGroupKeys(
  client: ApiClient,
  groupId: number,
  keys: string,
  signal?: AbortSignal,
): Promise<GroupKeyImportResult> {
  return client.request(`/api/groups/${groupId}/keys/import`, {
    method: "POST",
    json: { keys },
    signal,
  });
}
```

Reuse raw-key warnings and 1,000-line limit. Existing mode never sends URL, protocol, config, or models. New-mode URL conflict handoff must preserve raw text and select existing mode. On success, invalidate exactly Group keys/detail/list and health for the selected Group.

- [ ] **Step 4: Run focused GREEN and backend contract tests**

```bash
cd web
pnpm exec vitest run \
  src/features/import/ExistingGroupImport.test.ts \
  src/features/import/ImportView.test.ts \
  src/features/import/import-recovery.test.ts \
  src/features/import/key-analysis.test.ts
go test -race ./internal/control -run 'Test.*(Import.*Key|GroupKeys)'
```

- [ ] **Step 5: Run full gate, review, and corrections**

Use the standard web/full control-webui gate. Review zero discovery/model/settings calls, exact invalidations (`groups.keys/detail/list`, `health`), raw-key cache absence, and route history.

- [ ] **Step 6: Commit Task 3**

```bash
git add web
git commit -m "feat(web): 实现已有分组密钥导入"
```

## Task 4: T23 Group Detail Shell and Keys Tab

**Files:**

- Create: `web/src/api/control/upstream-keys.ts`
- Create: `web/src/features/groups/group-route.ts`
- Create: `web/src/features/groups/GroupDetailView.vue`
- Create: `web/src/features/groups/GroupHeader.vue`
- Create: `web/src/features/groups/GroupTabs.vue`
- Create: `web/src/features/groups/keys/key-patch.ts`
- Create: `web/src/features/groups/keys/GroupKeysTab.vue`
- Create: `web/src/components/ui/AppTabs.vue`
- Create: `web/src/components/ui/AppDialog.vue`
- Create: `web/src/components/ui/DataTable.vue`
- Modify: `web/src/app/router.ts`, `web/src/api/control/groups.ts`
- Modify: `web/src/features/home/GroupCard.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing route, tab, API, patch, and component tests**

```ts
expect(parsePositiveId("7")).toBe(7);
expect(parsePositiveId("0")).toBeUndefined();
expect(normalizeGroupTab("unknown")).toBe("keys");

expect(buildUpstreamKeyPatch(base, base)).toEqual({});
expect(buildUpstreamKeyPatch(base, { ...base, weight_manual: null })).toEqual({
  weight_manual: null,
});
```

Assert invalid IDs issue zero API calls, tab clicks push `?tab=...`, masks only render, statuses contain icon/text/tone, and no-op Save is disabled plus transport-guarded. Add RED spies proving both key update and key delete invalidate exactly `groups.keys(id)`, `groups.detail(id)`, `groups.list()`, and `health()`.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/control/upstream-keys.test.ts \
  src/features/groups/group-route.test.ts \
  src/features/groups/GroupDetailView.test.ts \
  src/features/groups/GroupTabs.test.ts \
  src/features/groups/keys/key-patch.test.ts \
  src/features/groups/keys/GroupKeysTab.test.ts
```

Expected RED: route is a placeholder and all key-management modules are absent.

- [ ] **Step 3: Implement detail shell, route-backed tabs, and key operations**

Upstream-key API methods use exact paths and partial bodies. UI exposes Auto or 1–100, never 0. Delete uses Reka Dialog and import link is `/import?mode=existing&group_id=<id>`.

The Group-detail query contains `config` and `effective_config` HeaderRules values, so configure it with `gcTime: 0` and rely on session-change cleanup.

Use invalidation:

```ts
await Promise.all([
  queryClient.invalidateQueries({
    queryKey: controlQueryKeys.groups.keys(groupId),
  }),
  queryClient.invalidateQueries({
    queryKey: controlQueryKeys.groups.detail(groupId),
  }),
  queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.list() }),
  queryClient.invalidateQueries({ queryKey: controlQueryKeys.health() }),
]);
```

- [ ] **Step 4: Run focused GREEN and Go contracts**

```bash
cd web
pnpm exec vitest run \
  src/api/control/upstream-keys.test.ts \
  src/features/groups/group-route.test.ts \
  src/features/groups/GroupDetailView.test.ts \
  src/features/groups/GroupTabs.test.ts \
  src/features/groups/keys/key-patch.test.ts \
  src/features/groups/keys/GroupKeysTab.test.ts
go test -race ./internal/control -run 'Test.*(GroupDetail|GroupKeys|UpstreamKey)'
```

- [ ] **Step 5: Run full gate, review, and corrections**

Review invalid-ID zero requests, history, focus restoration, dense mobile-contained table, status non-color cues, no plaintext control, no-op update suppression, and exact invalidation.

- [ ] **Step 6: Commit Task 4**

```bash
git add web
git commit -m "feat(web): 实现分组密钥管理"
```

## Task 5: T23 Group Models and Aliases

**Files:**

- Create: `web/src/features/groups/models/model-diff.ts`
- Create: `web/src/features/groups/models/GroupModelsTab.vue`
- Promote: `web/src/features/import/ModelDraftEditor.vue` → `web/src/components/config/ModelDraftEditor.vue`
- Modify: `web/src/api/control/groups.ts`
- Modify: `web/src/features/import/NewGroupImport.vue`
- Modify: `web/src/features/groups/GroupDetailView.vue`, `web/src/features/groups/GroupTabs.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing model-diff and exact API tests**

```ts
expect(
  buildModelDiff([{ id: "old", alias: "public" }], ["old", "new"]),
).toEqual([
  {
    id: "old",
    alias: "public",
    origin: "persisted",
    rediscovered: true,
    selected: true,
  },
  {
    id: "new",
    alias: "",
    origin: "discovered",
    rediscovered: true,
    selected: true,
  },
]);
```

Add a saved-but-not-returned case that remains selected and marked not rediscovered, manual model after failed discovery, alias preservation, empty-list confirmation, and normalized no-op zero PUT. Add a pre-implementation QueryClient spy asserting successful replace rebases/invalidates Group detail and invalidates Group list, but does not invalidate health.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/control/groups.test.ts \
  src/features/groups/models/model-diff.test.ts \
  src/features/groups/models/GroupModelsTab.test.ts \
  src/components/config/ModelDraftEditor.test.ts
```

Expected RED: no models tab/diff/shared editor or exact discover/replace API.

- [ ] **Step 3: Implement candidate-only discovery and replace-all save**

API calls:

```ts
POST /api/groups/:id/models/discover  // body omitted or {}
PUT  /api/groups/:id/models           // { models: GroupModel[] }
```

Discovery never mutates saved state. `NO_ACTIVE_UPSTREAM_KEY` renders a structured action to the Keys tab or existing-Group import; `BAD_GATEWAY` preserves the draft and manual-model path. Save sends the complete normalized list and uses a feature-local async action rather than TanStack mutation state because returned `GroupDetail` can contain HeaderRules values. Rebase the `gcTime: 0` Group-detail query directly, then invalidate Group list. Empty list requires hard confirmation; model removals display the static AccessKey-filter limitation.

- [ ] **Step 4: Run focused GREEN and Go contracts**

```bash
cd web
pnpm exec vitest run \
  src/features/groups/models/model-diff.test.ts \
  src/features/groups/models/GroupModelsTab.test.ts \
  src/components/config/ModelDraftEditor.test.ts
go test -race ./internal/control -run 'Test.*(GroupModels|DiscoverGroup)'
```

Invalidate Group detail/list only. Runtime health contains key state, not model state; Home recomputes serviceability from the refreshed Group list plus the existing health counts.

- [ ] **Step 5: Run full gate, review, and corrections**

Review alias preservation, no silent deletion, manual fallback, `NO_ACTIVE_UPSTREAM_KEY` guidance back to Keys/import, full-body replacement, no-op suppression, empty confirmation, and no invented AccessKey dependencies.

- [ ] **Step 6: Commit Task 5**

```bash
git add web
git commit -m "feat(web): 实现分组模型管理"
```

## Task 6: T23 Group Settings and Deletion

**Files:**

- Create: `web/src/features/groups/settings/group-settings-patch.ts`
- Create: `web/src/features/groups/settings/GroupSettingsTab.vue`
- Create: `web/src/features/groups/settings/GroupDeleteDialog.vue`
- Create: `web/src/features/groups/settings/GroupInUseFeedback.vue`
- Modify: `web/src/api/control/groups.ts`
- Modify: `web/src/features/groups/GroupDetailView.vue`, `web/src/features/groups/GroupTabs.vue`
- Modify: `web/src/components/config/HeaderRulesEditor.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing sparse-patch and structured-error tests**

```ts
expect(buildGroupSettingsPatch(base, unchangedDraft)).toEqual({});
expect(
  buildGroupSettingsPatch(base, { ...draft, validation_model: null }),
).toEqual({
  validation_model: null,
});
expect(enableHeaderRulesOverride(base.effective_config.header_rules)).toEqual(
  structuredClone(base.effective_config.header_rules),
);
```

Cover exact five Group runtime keys, inherited/effective display, override removal, URL confirmation versus conflict, rediscovery recommendation, typed-name deletion, and `GROUP_IN_USE` references. Add RED invalidation assertions: every successful base update rebases detail and invalidates list; only name/enabled/weight changes invalidate health. Delete success removes detail/keys queries, invalidates list/health, and navigates to Home; `GROUP_IN_USE` performs none of those success effects.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/features/groups/settings/group-settings-patch.test.ts \
  src/features/groups/settings/GroupSettingsTab.test.ts \
  src/features/groups/settings/GroupDeleteDialog.test.ts \
  src/features/groups/settings/GroupInUseFeedback.test.ts \
  src/components/config/HeaderRulesEditor.test.ts
```

Expected RED: settings tab, sparse patching, and structured destructive branches are absent.

- [ ] **Step 3: Implement base fields, sparse overrides, and confirmations**

First URL change sends ordinary update. Only `UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED` opens confirmation and resubmits with `confirm_upstream_url_change: true`. `UPSTREAM_URL_CONFLICT` is blocking and never receives that flag.

Config save sends the complete resulting sparse `config`, never `effective_config`. Group update uses a feature-local async action instead of TanStack mutation state because `GroupUpdateResult.group` can contain HeaderRules values; rebase the `gcTime: 0` detail query directly and invalidate Group list. Invalidate health only when name, enabled, or weight changes: the actual health DTO carries `groups[].name` and problem-key `group_name`, so a rename otherwise leaves that shared resource stale. HeaderRules enable seeds a deep copy and displays full-replacement warning. No-op `{}` returns before transport.

On delete success, remove `groups.detail(id)` and `groups.keys(id)`, invalidate Group list and health, then replace the route with Home. On `GROUP_IN_USE`, retain the page/cache/form and render the returned AccessKey references.

- [ ] **Step 4: Run focused GREEN and Go contracts**

```bash
cd web
pnpm exec vitest run \
  src/features/groups/settings/group-settings-patch.test.ts \
  src/features/groups/settings/GroupSettingsTab.test.ts \
  src/features/groups/settings/GroupDeleteDialog.test.ts \
  src/features/groups/settings/GroupInUseFeedback.test.ts \
  src/components/config/HeaderRulesEditor.test.ts
go test -race ./internal/control -run 'Test.*(GroupUpdate|GroupDelete|URLChange|GroupInUse)'
```

- [ ] **Step 5: Run full gate, review, and corrections**

Review sparse ownership, HeaderRules replacement/secret handling, no-op submission, URL branch separation, rediscovery action, typed delete, structured AccessKey references, and conditional invalidation.

- [ ] **Step 6: Commit Task 6**

```bash
git add web
git commit -m "feat(web): 实现分组设置管理"
```

## Task 7: T24 AccessKey Management

**Files:**

- Create: `web/src/features/access-keys/access-key-patch.ts`
- Create: `web/src/features/access-keys/access-key-options.ts`
- Create: `web/src/features/access-keys/AccessKeysView.vue`
- Create: `web/src/features/access-keys/AccessKeyTable.vue`
- Create: `web/src/features/access-keys/AccessKeyDrawer.vue`
- Create: `web/src/features/access-keys/AccessKeyDeleteDialog.vue`
- Modify: `web/src/api/control/access-keys.ts`, `web/src/app/router.ts`
- Modify: `web/src/components/ui/AppDrawer.vue`, `web/src/components/ui/DataTable.vue`, `web/src/components/ui/SecretValue.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing API, patch, Drawer, list, and secret-lifetime tests**

```ts
expect(buildCreateAccessKeyInput(draft)).toEqual({
  name: "client",
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
});
expect(buildAccessKeyUpdatePatch(base, unchangedDraft)).toEqual({});
expect(createPayload).not.toHaveProperty("status");
```

Cover empty filters = all, protocol `openai-response`, known/free-entry model preservation, Number.isSafeInteger non-negative RPM, last-key warning, mask/reveal/copy, and plaintext absent after Drawer closes. Add a RED QueryClient spy proving create/update/delete invalidate only `accessKeys.list()`.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/control/access-keys.test.ts \
  src/features/access-keys/access-key-patch.test.ts \
  src/features/access-keys/access-key-options.test.ts \
  src/features/access-keys/AccessKeysView.test.ts \
  src/features/access-keys/AccessKeyDrawer.test.ts \
  src/features/access-keys/AccessKeyDeleteDialog.test.ts
```

Expected RED: route remains placeholder; CRUD/drawer/secret lifecycle is absent.

- [ ] **Step 3: Implement AccessKey list and local sensitive actions**

List query uses `gcTime: 0`. Create/update call ApiClient directly with AbortController; do not use TanStack mutation state because responses contain plaintext. Closing/unmounting clears local response/draft. Invalidate/refetch list after success. Delete may use `useMutation` because response has no credential.

Create omits status. Update sends only dirty fields. Empty filters remain exact empty arrays and display “all.”

- [ ] **Step 4: Run focused GREEN and Go contracts**

```bash
cd web
pnpm exec vitest run \
  src/api/control/access-keys.test.ts \
  src/features/access-keys/access-key-patch.test.ts \
  src/features/access-keys/access-key-options.test.ts \
  src/features/access-keys/AccessKeysView.test.ts \
  src/features/access-keys/AccessKeyDrawer.test.ts \
  src/features/access-keys/AccessKeyDeleteDialog.test.ts
go test -race ./internal/control -run 'Test.*AccessKey'
```

Canary assertions cover route, session/local storage, query key, query cache after unmount, mutation cache, error output, snapshots, and DOM after Drawer close.

- [ ] **Step 5: Run full gate, review, and corrections**

Review exact bodies, filter semantics, free-entry models, plaintext lifetime, `gcTime: 0`, no-op PUT suppression, Drawer focus/full-screen mobile behavior, and last-key warning.

- [ ] **Step 6: Commit Task 7**

```bash
git add web
git commit -m "feat(web): 实现访问密钥管理"
```

## Task 8: T24 Settings, Browser Preferences, and SystemInfo

**Files:**

- Create: `web/src/api/control/settings.ts`
- Create: `web/src/api/control/system-info.ts`
- Create: `web/src/features/settings/settings-patch.ts`
- Create: `web/src/features/settings/SettingsView.vue`
- Create: `web/src/features/settings/AppearanceSection.vue`
- Create: `web/src/features/settings/RequestForwardingSection.vue`
- Create: `web/src/features/settings/LogsMaintenanceSection.vue`
- Create: `web/src/features/settings/SystemInfoSection.vue`
- Create: `web/src/features/settings/SettingOverrideField.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/features/preferences/theme.ts`, `web/src/i18n/context.ts`
- Modify: `web/src/components/config/HeaderRulesEditor.vue`
- Modify: `web/src/i18n/locales/zh-CN.ts`, `web/src/i18n/locales/en-US.ts`, `web/src/i18n/locales/ja-JP.ts`

- [ ] **Step 1: Write failing API, patch, section, and allowlist tests**

```ts
expect(buildSettingsPatch(base, draft, "request-forwarding")).toEqual({
  request_timeout: 900,
  header_rules: null,
});
expect(buildSettingsPatch(base, unchanged, "logs-maintenance")).toEqual({});
```

Cover exact six keys, `overrides` as a string array, null reset, independent section save, 1–365 retention, positive integer timeouts, advanced HeaderRules disclosure states, all Set values masked, local appearance zero API calls, Settings query `gcTime: 0`, and SystemInfo extra secret-like field rejection. Seed two loaded Group-detail queries and add a RED spy proving Settings success rebases/invalidate Settings plus `groups.details()` only, without invalidating Group list, key lists, health, AccessKeys, or SystemInfo.

- [ ] **Step 2: Run tests and record RED**

```bash
cd web
pnpm exec vitest run \
  src/api/control/settings.test.ts \
  src/api/control/system-info.test.ts \
  src/features/settings/settings-patch.test.ts \
  src/features/settings/AppearanceSection.test.ts \
  src/features/settings/RequestForwardingSection.test.ts \
  src/features/settings/LogsMaintenanceSection.test.ts \
  src/features/settings/SystemInfoSection.test.ts \
  src/features/settings/SettingsView.test.ts
```

Expected RED: route is placeholder and Settings/SystemInfo modules do not exist.

- [ ] **Step 3: Implement dirty-only Settings and allowlisted SystemInfo**

Settings/SystemInfo API paths and the Settings update body are exact:

```ts
export function getSettings(client: ApiClient, signal?: AbortSignal) {
  return client.request<SettingsDto>("/api/settings", { signal });
}

export function getSystemInfo(client: ApiClient, signal?: AbortSignal) {
  return client.request<SystemInfoDto>("/api/system/info", { signal });
}

return client.request<SettingsDto>("/api/settings", {
  method: "PUT",
  json: { settings: patch },
  signal,
});
```

`values` supplies effective values; `new Set(overrides)` supplies explicit state. Enabling an override seeds current value. Reset sends `null`. Each section disables Save and returns early for `{}`. Settings GET uses `gcTime: 0`; Settings PUT uses a feature-local async action rather than TanStack mutation state because its response contains HeaderRules values. A successful response directly rebases the Settings query and becomes the new form base.

HeaderRules is collapsed unless override exists, dirty/error state requires opening, or save error targets it. All Set values are password-style by default.

SystemInfo maps only the exact allowlisted DTO fields. Copy only non-null paths.

- [ ] **Step 4: Run focused GREEN and Go contracts**

```bash
cd web
pnpm exec vitest run \
  src/api/control/settings.test.ts \
  src/api/control/system-info.test.ts \
  src/features/settings/settings-patch.test.ts \
  src/features/settings/AppearanceSection.test.ts \
  src/features/settings/RequestForwardingSection.test.ts \
  src/features/settings/LogsMaintenanceSection.test.ts \
  src/features/settings/SystemInfoSection.test.ts \
  src/features/settings/SettingsView.test.ts
go test -race ./internal/control -run 'Test.*Settings'
go test -race ./internal/control -run 'Test.*SystemInfo'
```

Rebase `controlQueryKeys.settings()` with the returned DTO, then invalidate the `controlQueryKeys.groups.details()` prefix so every currently loaded Group detail refreshes effective config. Do not invalidate Group list, key lists, health, AccessKeys, or SystemInfo. Locale/theme changes must produce zero `/api/settings` calls.

- [ ] **Step 5: Run full gate, review, and corrections**

Review six-field allowlist, dirty-only/null reset, advanced disclosure, HeaderRules canary protection, SystemInfo allowlist, browser-local preference isolation, last-write-wins wording, and no-op suppression.

- [ ] **Step 6: Commit Task 8**

```bash
git add web
git commit -m "feat(web): 实现运行设置与系统信息"
```

## Final Stage Gate and Documentation

- [ ] **Step 1: Run the complete frontend gate from a clean test cache**

```bash
cd web
pnpm run format
pnpm run lint
pnpm run type-check
pnpm run test
pnpm run build
cd ..
```

Expected: zero formatting/lint/type/test/build failures.

- [ ] **Step 2: Run production CSP smoke against a fresh binary**

```bash
go build -o gpt-load .
pnpm --dir web exec playwright install --with-deps chromium
pnpm --dir web run test:csp
```

Expected: Drawer/Select/theme interactions succeed with zero CSP violation, console error, or page error.

- [ ] **Step 3: Run the complete repository gate**

```bash
go test -race . ./internal/...
go vet ./...
git diff --check
git status --short
```

Expected: all commands exit 0 and `git status --short` is empty.

- [ ] **Step 4: Perform manual visual/accessibility matrix**

Record evidence for 375/768/1024/1440 widths; system/light/dark; zh-CN/en-US/ja-JP; keyboard-only navigation; Drawer/Dialog focus restoration; tabs/back; table overflow; long strings; clipboard success/failure; reduced motion; Home partial failures; import 401 recovery/expiry; no-op saves; Settings reset; masked HeaderRules.

- [ ] **Step 5: Request final full-branch review**

Review `base..HEAD` for T21–T24 scope, API contract accuracy, secret leakage, query invalidation, Reka/CSP, responsiveness, accessibility, and absence of S7/M4 work. Correct findings through the original task owner and rerun Steps 1–3.

- [ ] **Step 6: Synchronize Notion after verified integration**

Update the M3 implementation plan with actual T21–T24 commits, verification commands, CI run, and remaining S7 scope. The S5.5 T24 dependency and interaction §4.7 corrections are already complete; update interaction/visual documents only if implementation changed a formal product or visual contract.

- [ ] **Step 7: Use `superpowers:finishing-a-development-branch`**

Present merge/PR/keep/discard options only after all evidence is current. Do not push unless the user selects an integration path requiring it.
