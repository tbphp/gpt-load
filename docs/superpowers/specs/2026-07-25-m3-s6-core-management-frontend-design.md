# M3 S6 Core Management Frontend Design

**Status:** Approved

**Scope:** M3 S6 T21–T24 only. Monitor, full Playwright business E2E, release packaging, and final M3 closure remain S7.

**Canonical product sources:**

- [GPT-Load 2.0 交互设计文档](https://app.notion.com/p/3a55e49ce6ae813eba04eac597a0e7c1)
- [GPT-Load 2.0 M3 前端视觉规范](https://app.notion.com/p/3a55e49ce6ae8120833dc1c7434b7aa3)
- [GPT-Load 2.0 M3 实施方案：管理面与前端](https://app.notion.com/p/3a55e49ce6ae81eca3ccf9d850fe958e)
- [GPT-Load 2.0 配置体系与运行时快照设计方案](https://app.notion.com/p/3a75e49ce6ae812c9d2dee36cdbd4457)

`AGENTS.md` defines `tmp/m3-frontend-reference/` as the complete local visual reference. When that directory is absent, the Notion visual specification and its embedded baseline remain authoritative. The currently available `tmp/gpt-load-m3-visual-reference.html` is a supplemental comparison artifact only and cannot override either source.

## Goal

Deliver the first complete management frontend over the existing authenticated control API:

- T21: operational home, Group cards, connection configuration, and first-run empty state;
- T22: new-Group discovery/create flow and existing-Group keys-only import flow;
- T23: Group detail with upstream keys, models/aliases, and settings tabs;
- T24: AccessKey management and Settings/SystemInfo;
- one responsive Product / zinc-gray application shell shared by all protected routes;
- real backend data only, with no fabricated usage, cost, price, trend, success-rate, or health-percentage metrics.

The Vue application remains embedded through `go:embed` in the single Go binary. Production does not gain a Node runtime or a separate frontend service.

## Non-goals

This stage does not implement:

- `/monitor` request-log and route-inspector functionality;
- the complete five-flow Playwright suite planned for S7;
- release workflows, deployment changes, or final M3 closure;
- M4 usage, token, price, or cost functionality;
- arbitrary system-setting or Group-config editors;
- backend persistence for locale or theme;
- manual upstream validation, aggressive retry, debug-response-header, SSE keepalive, or other unimplemented controls;
- new Go dependencies, tables, migrations, schema versions, stores, or backend endpoints.

The `/monitor` route remains an explicit S7 placeholder and must not display synthetic data.

## Current baseline

The repository already provides:

- Vue 3, Vue Router, TanStack Vue Query, Vue I18n, Tailwind/Vite, Vitest, and happy-dom;
- explicit routes for `/`, `/login`, `/import`, `/groups/:id`, `/access-keys`, `/monitor`, and `/settings`;
- `AuthGate`, an AUTH_KEY session state machine, strict same-origin `/api/*` transport, locale headers, and one-shot global 401 handling;
- semantic tokens for system/light/dark themes, basic UI components, and the S2 login experience;
- Vite output to `internal/webui/dist` and Go CSP/static-route integration;
- all control endpoints required by T21–T24.

The protected application is still a skeleton: navigation is local to `HomeView`, most routes use `PlaceholderView`, `ApiClient` and `AppI18n` are not injectable by feature modules, and auth clearing removes only the auth query rather than future authenticated resource caches.

## Design principles

1. **Real data over decorative dashboards.** Render only values returned by current APIs or deterministic state derived from them.
2. **One shared shell, vertical feature slices.** Build only the shared foundation with immediate consumers, then deliver T21 → T22 → T23 → T24.
3. **Feature-local state over a global store.** Vue state, route query, and TanStack Query are sufficient; do not add Pinia.
4. **Server error codes are control flow.** Never parse localized `message` text to choose behavior.
5. **Sparse settings stay sparse.** `config` and `overrides` drive edit semantics; `effective_config` and `values` are display values.
6. **Secrets have explicit lifetimes.** No secret may enter URLs, query keys, logs, persistent query caches, or generic diagnostics.
7. **Accessibility is structural.** Semantic elements, focus management, keyboard flow, labels, and text/icon/color status cues are required, not polish.
8. **The formal visual specification wins.** External design-system suggestions that conflict with Product / zinc-gray, system fonts, or local assets are ignored.

## Delivery architecture

### Application tree

```text
App
├── public route: LoginView
└── AuthGate
    └── AppShell
        └── protected RouterView
```

`App.vue` continues to select public versus protected routes. Every protected route is wrapped by `AppShell`; the login route remains independent.

### Shared shell

`AppShell` owns exactly one copy of:

- skip-to-main-content link;
- desktop navigation;
- responsive mobile navigation drawer;
- primary “Import upstream keys” action;
- locale control for `zh-CN`, `en-US`, and `ja-JP`;
- theme control for `system`, `light`, and `dark`;
- current-route styling and accessible page landmark;
- shared content width and responsive padding.

Primary navigation contains Home, Access Keys, Monitor, and Settings. Group detail is reached contextually from Group cards. Import is the primary action rather than a persistent navigation category.

At narrow widths, navigation must never disappear without a replacement. The mobile drawer uses the same route set and returns focus to its trigger when closed.

### Dependency injection

`main.ts` creates and provides:

- `AuthSession`;
- `ApiClient`;
- `AppI18n`;
- `ThemeController`;
- the existing `QueryClient` through `VueQueryPlugin`.

Add focused injection modules:

```text
src/api/client-context.ts
src/i18n/context.ts
src/features/preferences/theme.ts
```

`useApiClient()`, `useAppI18n()`, and `useTheme()` fail fast when used without a provider, enabling simple fake injection in component tests.

### Feature and API boundaries

```text
src/app/
  AppShell.vue
  query-keys.ts
src/api/control/
  groups.ts
  health.ts
  access-keys.ts
  settings.ts
  system-info.ts
src/features/
  home/
  import/
  groups/
  access-keys/
  settings/
  preferences/
src/components/ui/
  shared primitives with at least two consumers
```

`api/control/*` modules contain endpoint DTOs, exact paths/methods/bodies, and narrowly scoped request functions. They do not own page state or cache policy.

Feature query composables receive the injected client and pass TanStack Query's `AbortSignal` to `ApiClient.request`.

Do not introduce a generic repository, service locator, CRUD framework, form framework, or runtime schema framework. Add small type guards only for structured error data that changes UI flow, such as:

- `UPSTREAM_URL_CONFLICT`;
- `UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED`;
- `GROUP_IN_USE`;
- `NO_ACTIVE_UPSTREAM_KEY`.

## Query and cache model

### Query keys

Use a stable factory rooted at `['control']`:

```text
control.all
control.groups.list
control.groups.detail(id)
control.groups.keys(id)
control.health
control.accessKeys.list
control.settings
control.systemInfo
```

Keys may contain normalized numeric resource IDs and non-sensitive filters only. They must never contain:

- AUTH_KEY or AccessKey plaintext;
- upstream keys;
- HeaderRules names or values;
- complete request bodies;
- discovery payloads.

### Session isolation

A credential change is a data-security boundary. On logout, successful login with a different candidate, or global 401 handling:

1. cancel in-flight authenticated queries;
2. clear authenticated query and mutation caches;
3. update/remove the credential;
4. seed or revalidate the auth-session query;
5. continue to the login or requested route.

This happens before any new authenticated page can render stale data from the prior credential.

No TanStack persistence plugin is introduced. Queries containing AccessKey plaintext or HeaderRules values use `gcTime: 0`, are removed as soon as their last observer unmounts, and are always removed on session change. There is no exception for these sensitive DTOs.

### Mutation and invalidation rules

Use resource-specific invalidation rather than global refetch:

- Group base update → Group detail and Group list; health if enabled/weight changed;
- model replace → Group detail and Group list;
- upstream-key import/update/delete → Group keys, Group detail/list key count, and health;
- AccessKey create/update/delete → AccessKey list;
- Settings update → Settings and every loaded Group detail because `effective_config` can change.

Successful mutations use the returned DTO immediately when its shape is authoritative, then invalidate dependent resources.

Requests containing upstream-key plaintext do not use long-lived TanStack mutation variables. AccessKey create/update requests also avoid TanStack mutation state because their successful DTOs contain plaintext. These operations run through feature-local async actions with explicit pending/error state and an `AbortController`; closing the workflow clears returned plaintext. AccessKey delete may use an ordinary mutation because its response contains no credential.

## Local preferences

### Locale

The existing locale controller remains authoritative:

- storage key: `gpt-load.locale`;
- browser-language fallback;
- `document.documentElement.lang` synchronization;
- storage failures degrade to in-memory behavior;
- locale affects `Accept-Language` but is never sent to `/api/settings`.

### Theme

Add `ThemeController` with:

```ts
type AppTheme = "system" | "light" | "dark";
```

Rules:

- storage key: `gpt-load.theme`;
- `system` removes the explicit theme attribute and follows media preference;
- `light` and `dark` set `document.documentElement.dataset.theme`;
- storage and `matchMedia` failures degrade safely;
- no server persistence or Settings field.

AppShell exposes compact controls; Settings also presents these values under a clearly browser-local “Appearance” section.

## Shared UI primitives

Retain and extend the existing `AppButton`, `FormField`, `InlineFeedback`, and `SurfaceCard`. Introduce components only when the current slice has a real consumer:

- `PageHeader`;
- `IconButton`;
- `StatusBadge`;
- `QueryFeedback` / `EmptyState`;
- `CopyButton` / `SecretValue`;
- `AppSelect`;
- `AppDialog` and right-side `AppDrawer`;
- route-backed `AppTabs`;
- responsive `DataTable` wrapper.

Feature-local components remain local until a second consumer proves reuse, including `KeyTextarea`, `ModelDraftEditor`, and `HeaderRulesEditor`.

Reka UI supplies accessible Select, Dialog/Drawer, Tabs, and related behavior. Lucide supplies all interface icons. Dependencies are added only with their first T21 consumers. Emoji are not used as UI icons.

Do not add Axios, Pinia, Zod, VeeValidate, Storybook, or a generic component framework.

## Visual system

The authoritative direction is calm technical minimalism with zinc-gray surfaces:

- system font stack; no external font CDN;
- restrained neutral surfaces and semantic status colors;
- comfortable density on Home and Group cards;
- locally denser keys and AccessKey tables;
- modest radius and shadow, no glass, neon, marketing gradient, heavy glow, or inflated pill styling;
- status always uses icon + localized text + semantic color;
- no hard-coded Hex values in feature/page components;
- tokens own theme colors, radii, shadows, spacing, focus rings, overlays, and z-index tiers.

The UI Pro design search recommended an OLED default, green CTA, glow, and external Fira fonts. Those recommendations conflict with the formal visual specification and are explicitly rejected. Only its general accessibility and responsive guidance is retained.

Component styles live with reusable components or feature SFCs and consume semantic tokens. `base.css` remains reset/shell-level rather than becoming a second page-specific design system.

## Responsive and accessibility contract

Test and design for 375, 768, 1024, and 1440 pixel widths.

- body text remains at least 16px on mobile form surfaces;
- primary touch targets are at least 44×44px;
- keyboard tab order follows visual order;
- every icon-only button has an accessible name;
- focus rings are visible in both themes;
- Dialog/Drawer traps focus, closes with Escape, and restores focus;
- route-backed tabs expose proper tab semantics and preserve browser back behavior;
- tables include headers/captions and use contained horizontal scrolling on narrow screens;
- cards and tables never depend on color alone;
- asynchronous feedback uses an appropriate live region without repeatedly announcing polling updates;
- reduced-motion preferences disable nonessential transitions;
- fixed/sticky shell elements never cover focused or first-page content.

Desktop AccessKey and Group-key tables stay dense. On narrow screens, action clusters wrap and the table scroll container remains inside the viewport. Drawers become full-screen panels on small devices.

## T21: Home

### Data sources

Home performs independent queries for:

- `GET /api/groups`;
- `GET /api/health`;
- `GET /api/access-keys` for the connection card.

They are not combined into one `Promise.all` query. Each section owns loading, error, stale-data, and empty states so a failure in one resource does not erase successful data from another.

Groups and health are joined by numeric Group ID. Temporary revision skew is tolerated; the page must not claim an atomic dashboard snapshot.

### Operational summary

Render only real values:

- network reachability separately from health availability;
- snapshot revision and observed timestamp when health succeeds;
- total, available, cooldown, blacklisted, and disabled key counts.

A network failure means the management service is unreachable. An authenticated HTTP error means the service responded but health is unavailable. Only a successful health response is labeled online; HTTP failures are never mislabeled as network-offline.

Request-log queue and failure diagnostics remain on the S7 Monitor page; Home does not add a second request-log surface.

Do not derive success percentages, health percentages, throughput, tokens, usage, cost, or trends.

“Online” means the authenticated health request succeeded, not an invented secondary process metric.

### Group cards

Each card shows:

- name and normalized upstream host;
- protocol labels;
- enabled/disabled state;
- model summary from real `models`;
- total key count from Group list;
- available/cooldown/blacklisted/disabled counts from health when available;
- actions for details and existing-Group key import.

Serviceability is a deterministic UI label:

```text
enabled && models.length > 0 && health.counts.available > 0
```

If health data is unavailable, show “status unknown” rather than “unavailable.” No percentage is computed.

### First-run state

When no Groups exist, replace the Group grid with an explanatory empty state and primary action to `/import`. If no AccessKeys exist, the connection card links to `/access-keys` rather than inventing a credential.

### Connection configuration

Use `window.location.origin` as the current browser-reachable Base URL.

When AccessKeys exist:

- show a selector;
- initially select the first active key in backend ID order, otherwise the first key;
- treat this as an example selection only, not a server-side default;
- keep selection in component memory, not persistent storage;
- mask the key by default;
- reveal and copy only through explicit actions;
- show clear clipboard success/failure feedback.

Client snippets use the real Base URL and reference an environment variable such as `GPT_LOAD_API_KEY`; the selected plaintext key is copied through its separate explicit control rather than embedded into a permanently visible code block. Model values are either chosen from real Group models or shown as an explicit `<MODEL_ID>` placeholder; no fake model is presented as configured data.

## T22: Import

Import mode is route-backed:

- `/import` and `/import?mode=new` enter new-Group mode;
- `/import?mode=existing` enters existing-Group keys-only mode with a real Group selector;
- `/import?mode=existing&group_id=<positive-id>` enters existing mode with that Group preselected.

Unknown modes normalize to `new`; invalid `group_id` values do not issue a Group request. Changing mode updates route history so browser back/forward restores the workflow choice.

### New-Group flow

Use an explicit three-step state machine:

1. **Connection:** optional name, upstream URL, one or more supported protocols, and newline-delimited upstream keys.
2. **Discovery and models:** call `POST /api/models/discover`; display returned model IDs; allow selection and local aliases.
3. **Review and create:** call `POST /api/groups` once with connection fields, complete selected models, aliases, and original keys.

Discovery is read-only. A successful discovery never displays as saved configuration.

Changing URL, protocols, keys, or any discovery-relevant field invalidates previous candidates and returns the flow to a rediscovery-required state.

The frontend enforces obvious limits such as at most 1,000 non-empty key lines, but server validation remains authoritative. Duplicate counts shown after creation come from `keys_duplicated`, not an inferred database comparison.

### Same-URL conflict

There is no preflight endpoint. On `UPSTREAM_URL_CONFLICT`, render the structured conflicting Groups and offer:

- import the keys into one existing Group without changing its models;
- explicitly confirm creation of a separate Group and resubmit with `confirm_same_upstream_url: true`;
- return to edit.

Do not perform a trial create as a preflight.

### Existing-Group flow

Existing mode contains only:

- selected Group identity;
- key textarea;
- import review;
- `POST /api/groups/:id/keys/import`.

It does not discover, merge, or replace models and does not edit Group protocols/settings.

### Dirty navigation and 401 recovery

Unsaved user input activates both route-leave confirmation and `beforeunload`.

Upstream keys stay in component memory during ordinary editing. To satisfy authenticated recovery without routinely persisting secrets:

- the import feature registers its active draft with a small recovery service;
- immediately before global 401 clears the session, the service synchronously writes one versioned reauthentication draft under `gpt-load.import-reauth-draft` in `sessionStorage`;
- the record contains the current import fields, an absolute `expires_at`, and a fixed 15-minute TTL;
- a timer removes the record at expiry while the tab remains open;
- application startup and successful login sweep expired or incompatible records; explicit logout always removes the record; successful login also removes a valid record when the redirect target is not Import;
- Import performs read-and-delete before parsing or hydrating a valid record, so exceptions cannot leave a consumed plaintext copy in storage;
- success, explicit discard, cancellation, expiry, and incompatible-version detection remove it;
- the draft never enters localStorage, URL, query key, logs, or TanStack caches.

The 401 redirect bypasses the normal dirty-leave prompt only after recovery succeeds or fails safely; inability to access storage must not block authentication cleanup.

## T23: Group detail

`/groups/:id` validates a positive integer before issuing requests. Invalid IDs show a local not-found/error state without calling the API.

The active tab is route-backed through:

```text
?tab=keys|models|settings
```

The default is `keys`. Browser back/forward restores tab state.

The page loads Group detail and tab-specific resources independently. A stable header shows Group identity, upstream host, protocol labels, enabled state, and navigation back to Home.

### Keys tab

Load `GET /api/groups/:id/keys` and show:

- mask only, never upstream-key plaintext;
- configured status and effective status;
- manual and automatic weights;
- cooldown deadline and failure count where relevant;
- explicit status text/icon/color;
- edit, disable/enable, and delete actions.

Manual weight is exposed as Auto or 1–100. Value 0 remains an internal disable semantic and is not a normal UI weight choice; status controls user-visible disabling.

Updating a key sends only changed `status` and/or `weight_manual`. Deleting uses a confirmation dialog. Import routes to `/import?mode=existing&group_id=<id>`.

### Models and aliases tab

The persisted Group model array is the authoritative saved list. Editing happens in a local draft.

`POST /api/groups/:id/models/discover` returns candidates only. The UI computes a reviewable diff:

- existing discovered models retain their aliases;
- new candidates are proposed as additions;
- persisted models not returned are marked as not rediscovered, not silently deleted;
- users explicitly choose the final list;
- alias changes remain local until Save.

Save sends the complete final `{id, alias}[]` through `PUT /api/groups/:id/models`. Empty replacement requires a destructive confirmation because it makes the Group unserviceable. Removing models does not modify AccessKey model filters; the UI states this limitation without fabricating dependency data.

### Settings tab

The tab edits both Group base fields and supported runtime overrides.

Base fields:

- name;
- enabled;
- upstream URL;
- protocols;
- validation model;
- manual Group weight as Auto or 1–100.

A real upstream URL change first sends the normal update. On `UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED`, show a hard confirmation and resubmit with `confirm_upstream_url_change: true`. `UPSTREAM_URL_CONFLICT` remains a separate blocking branch. A response with `model_rediscovery_recommended` presents a clear follow-up action but never automatically changes models.

Runtime fields are exactly:

- `connect_timeout`;
- `first_byte_timeout`;
- `request_timeout`;
- `stream_idle_timeout`;
- `header_rules`.

For each field, `config` determines whether a Group override exists and `effective_config` supplies the read-only effective value. Disabling an override removes the key from the sparse config. Saving sends the complete resulting sparse `config` object and never sends `effective_config`.

Enabling a HeaderRules override seeds the editor from the current effective rules for continuity, then clearly warns that the Group owns a full replacement and future global HeaderRules changes will not merge into it.

Group deletion requires a hard confirmation using the Group name. On `GROUP_IN_USE`, render the returned AccessKey references and link to AccessKey management; do not reduce the error to a generic toast.

## T24: AccessKey management

`/access-keys` uses the authenticated list endpoint as the source of truth. The backend intentionally returns AccessKey plaintext, so the page applies stricter presentation and cache rules.

### List

Show:

- name;
- masked key with explicit reveal/copy;
- active/disabled state;
- Group, protocol, and model filter summaries;
- RPM limit, where 0 means unlimited;
- edit and delete actions.

Empty filter arrays are displayed as “all,” not as “none.”

### Create/edit drawer

Use one Drawer with mode-specific behavior:

- create: name, filters, and RPM limit; status is omitted because the backend creates active keys;
- edit: name, status, filters, and RPM limit;
- Group options come from real Groups;
- protocol options use supported control-plane values;
- model filters use known model suggestions while preserving valid existing/free-entry strings;
- submit is disabled while pending and errors remain in the Drawer with user input intact.

AccessKey create and update use feature-local async actions rather than TanStack mutation state because both responses contain plaintext. On success, display the returned plaintext with clear copy affordance only while the relevant Drawer/result surface remains open; closing it clears that result from component state. The action result is not inserted directly into query or mutation caches, the route, storage, toast payload, or error log. List invalidation may refetch the dedicated `gcTime: 0` AccessKey query while the page is still observing it.

Deleting any AccessKey requires confirmation. Deleting the last AccessKey adds an explicit warning that data-plane access will no longer have an issued credential, while still honoring backend behavior.

## T24: Settings and SystemInfo

`/settings` is divided by effect domain.

### Browser-local appearance

A compact Appearance section edits locale and theme through local controllers only. Its copy explicitly states that these preferences apply only to the current browser.

### Request and forwarding

Expose only:

- four global timeout fields in seconds;
- structured global HeaderRules.

GET `values` supplies current effective values; `overrides` determines explicit persistence.

Each timeout supports:

- current effective value;
- default/override badge;
- enabling an explicit override seeded from the current value;
- reset to code default by sending JSON `null`.

HeaderRules is an advanced disclosure that is collapsed by default. It opens automatically when an override exists, the editor is dirty, validation fails, or a save error targets HeaderRules.

Its structured rows contain:

- action: Set or Remove;
- canonicalizable header name;
- value for Set only;
- case-insensitive duplicate detection;
- every Set value masked by default with explicit reveal, including unknown/custom header names.

The section warns that global rules affect every Group without a HeaderRules override and that a Group override replaces the complete global object.

### Logs and maintenance

Expose only `request_log_retention_days`, range 1–365, with the same override/reset semantics.

### Save semantics

Each server-backed section submits only dirty keys:

```json
{
  "settings": {
    "request_timeout": 900,
    "header_rules": null
  }
}
```

A reset uses `null`; unchanged effective values are not written. A successful response replaces the local form base with returned `values`, `overrides`, and `revision`.

The API has last-write-wins semantics and no expected-revision precondition. The UI does not claim optimistic concurrency protection.

### Environment information

`GET /api/system/info` renders read-only definition rows for:

- version;
- instance mode, database, and distribution;
- data directory;
- AUTH_KEY source and optional keyfile path;
- encryption enabled/source and optional keyfile path.

Copy is allowed for non-secret paths. The UI never attempts to read files and never infers secret values, DSN, hashes, or ciphertext.

## Error and feedback model

- Loading, empty, stale-data, network-error, validation-error, and success states are distinct.
- Query sections retain last successful data when a background refetch fails and label it stale.
- `RequestCancelledError` is silent unless cancellation changes an explicit workflow state.
- Structured 409/404 data is rendered near the decision it affects.
- Field errors appear beside fields; page-level failures use `InlineFeedback`/`QueryFeedback`.
- Buttons disable during pending actions and prevent duplicate submission.
- 429 displays the provided retry interval.
- 401 uses the global session flow and does not leave feature caches visible.
- Backend localized `message` is a fallback explanation, never a branch condition.

Success feedback is concise and does not serialize response payloads.

## Internationalization

All new user-visible strings live in the three locale dictionaries and are grouped by domain:

```text
shell
common
home
import
groups
accessKeys
settings
```

Route metadata stores stable translation keys rather than hard-coded Chinese titles. AppShell updates the localized document title when either route or locale changes.

Dictionary structure tests require identical keys across zh-CN, en-US, and ja-JP. Components do not hard-code Chinese or use UI-autocomplete shortcuts. Dates and numbers use locale-aware formatting.

## Security model

### AUTH_KEY

- remains in sessionStorage through the existing auth model;
- is never a query key or error payload;
- successful credential replacement clears the prior authenticated cache first.

### AccessKey

- may be returned in plaintext only because the existing backend contract requires re-display;
- is masked by default;
- is copied/revealed only by explicit action;
- is never persisted by frontend preference/draft/query persistence;
- list-query data uses `gcTime: 0` and is removed after the last observer unmounts;
- create/update results stay only in local workflow state and are cleared when that workflow closes;
- all remaining authenticated cache state is removed on session reset.

### Upstream keys

- plaintext exists only in input memory, outbound request serialization, and the narrowly scoped expiring reauthentication draft;
- is never included in TanStack mutation caches, routes, localStorage, console output, analytics, screenshots, or diagnostic objects;
- is cleared on success/discard and restored drafts are removed from storage immediately.

### HeaderRules

- names and values never enter query keys or logs;
- every Set value uses a masked secret-aware input by default, regardless of header name;
- queries containing values use `gcTime: 0` and session-change cleanup;
- error output never serializes the full rules object.

Tests use canary values and assert their absence from route, storage outside the explicit 401 draft, query keys, error text, and snapshots.

## Dependency and CSP policy

Add only approved frontend dependencies with immediate consumers:

- `reka-ui` for accessible Select/Dialog/Drawer/Tabs behavior;
- `lucide-vue-next` for icons;
- `@playwright/test` as a development dependency for one production-build CSP smoke test.

The smoke test is introduced with the first T21 Reka consumers. It:

1. builds the Vue assets;
2. starts the embedded Go server with isolated temporary data and test credentials;
3. authenticates;
4. opens/closes the mobile navigation Drawer;
5. operates the locale Select and theme control;
6. asserts no CSP violation or uncaught browser error.

Add a dedicated `test:csp` script and run this single Chromium smoke after the production build in the existing web CI action. This is validation infrastructure, not the complete S7 business E2E suite. S7 still owns the full workflows, multi-viewport coverage, and release-grade browser matrix.

No inline script/eval allowance is added to CSP to make a primitive work. If a dependency requires weakening current CSP, stop and redesign rather than broadening policy.

## TDD and verification strategy

Every behavior change begins with a focused failing test and observed RED state.

### Foundation tests

- ApiClient/AppI18n/Theme injection and missing-provider failures;
- theme storage/system behavior and storage failure fallback;
- AppShell protected/public layout and mobile navigation;
- logout, 401, and credential replacement cancel/clear all authenticated caches;
- query keys contain no secret canaries;
- initial Reka production CSP smoke.

### T21 tests

- independent loading/error/empty/success states;
- Group/health join by ID;
- serviceability and unknown-state rules;
- no fabricated metric labels;
- first-run CTA;
- AccessKey selection, mask/reveal/copy, and absent-key state;
- real origin and explicit model placeholder behavior.

### T22 tests

- new versus existing route state;
- discovery invalidation;
- exact discover/create/import bodies;
- URL conflict decisions and confirmed resubmission;
- dirty route/browser leave behavior;
- secret-free query/mutation cache;
- 401 recovery draft lifecycle, expiry, version rejection, restore, and cleanup;
- cancellation and duplicate-submit prevention.

### T23 tests

- invalid ID issues no request;
- query-backed tab history;
- upstream-key effective states and mutations;
- model diff preserves aliases and never auto-deletes;
- replace-all model body;
- URL-change confirmation versus URL conflict;
- sparse config/effective config separation;
- HeaderRules replacement warning and override lifecycle;
- GROUP_IN_USE rendering and destructive confirmations.

### T24 tests

- AccessKey CRUD bodies and exact invalidation;
- plaintext absent from route/storage/query keys/query cache/mutation cache/errors after its owning surface closes;
- empty filters mean all;
- create versus edit status behavior;
- Settings dirty-only patch and JSON null reset;
- timeout/retention boundaries;
- HeaderRules case-insensitive validation and masked values;
- SystemInfo allowlist rendering and secret canaries;
- local preferences do not call `/api/settings`.

### Per-slice gates

```bash
cd web
pnpm run format
pnpm run lint
pnpm run type-check
pnpm run test
pnpm run build

go test -race ./internal/control ./internal/webui

git diff --check
```

Run only commands relevant to the current RED/GREEN loop during implementation, then the full per-slice gate before review.

### Stage exit gates

```bash
go build -o gpt-load .
go test -race . ./internal/...
```

Also run the CSP smoke and manual visual/accessibility checks at 375, 768, 1024, and 1440 widths in system/light/dark themes and all three locales. Manual checks must cover keyboard-only navigation, focus restoration, table overflow, long translations, clipboard success/failure, and partial API failure states.

## Implementation and review slices

Use one isolated feature worktree and one writer. Parallel agents are read-only reviewers or validators.

Recommended commits:

1. **T21 foundation and Home** — AppShell, injection, theme, cache isolation, Reka/Lucide, CSP smoke, real Home.
2. **T22 new-Group import** — discovery, model draft, create, conflict handling, 401 draft recovery.
3. **T22 existing-Group import** — keys-only flow and shared import hardening.
4. **T23 Group keys** — detail shell, route tabs, upstream-key list/mutations/import entry.
5. **T23 Group models** — discovery diff, aliases, replace-all save.
6. **T23 Group settings** — base fields, sparse runtime overrides, URL/delete confirmations.
7. **T24 AccessKeys** — list, Drawer CRUD, filters, secret behavior.
8. **T24 Settings/SystemInfo** — local appearance, six runtime fields, SystemInfo.

Each commit receives focused tests, the relevant gates, independent fresh-context review, and corrective re-review before the next dependent slice. A final full-branch review checks T21–T24 integration and scope exclusions.

## Documentation synchronization

After implementation and verified integration:

- update the Notion M3 implementation plan with T21–T24 actual completion and validation evidence;
- update canonical interaction/visual documents only if implementation exposed a genuine product or visual-contract change;
- do not duplicate formal product documentation into repository Markdown;
- keep this Superpowers design and the later implementation plan under `docs/superpowers/` as local workflow artifacts.

## Residual constraints

- Groups and health are independent reads and can briefly represent different revisions.
- Settings and Group updates are last-write-wins because the backend has no optimistic concurrency precondition.
- AccessKey list responses contain plaintext by current backend contract; the frontend can reduce but not eliminate exposure in process memory.
- The S6 CSP smoke cannot replace S7's complete browser E2E and accessibility verification.
- Monitor remains intentionally incomplete until S7.
