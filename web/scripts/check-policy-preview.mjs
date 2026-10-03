import {
  compileSfc,
  extract,
  loadTsModule,
  mountComponent,
  readSource,
  stripImports,
  Vue as vue,
} from './policy-test-utils.mjs'

// 1. Revision & Decimal Precision Tests
const ctx = loadTsModule(
  stripImports(readSource('web/src/frontends/modern/api/response.ts')) +
    extract(readSource('web/src/frontends/modern/api/group-detail.ts'), [
      'isValidCanonicalUint64',
      'readGroupPolicy',
      'readCredentialPolicy',
      'readPolicyPreviewResponse',
      'readPolicyDiscovery',
      'saveGroupPolicy',
      'saveCredentialPolicy',
      'previewGroupPolicy',
      'previewCredentialPolicy',
      'getPolicyDiscovery',
      'MAX_CANONICAL_UINT64',
    ]),
  { InvalidResponseError: class InvalidResponseError extends Error {} },
)

function mockPolicy(scope = 'group', rev = '1') {
  return {
    scope,
    id: 1,
    group_id: 1,
    credential_id: 1,
    revision_text: rev,
    config_text: '{"schema_version":1,"rules":[]}',
  }
}

// Verify full uint64 revision text parsing without safe integer limits
for (const fn of ['readGroupPolicy', 'readCredentialPolicy']) {
  for (const rev of ['0', '9007199254740993', '18446744073709551615']) {
    const raw = mockPolicy(fn === 'readGroupPolicy' ? 'group' : 'credential', rev)
    const res = ctx[fn](raw)
    if (res.revisionText !== rev) {
      console.error(`FAIL: ${fn} did not preserve revision_text ${rev}, got ${res.revisionText}`)
      process.exit(1)
    }
  }
}
console.log('PASS: Full uint64 revision_text parsed correctly')

// Verify binding readers reject malformed or overflowing revision_text
for (const fn of ['readGroupPolicy', 'readCredentialPolicy']) {
  for (const bad of ['00', '-1', '+1', '1e9', ' 1 ', '18446744073709551616', 'notanumber']) {
    const raw = mockPolicy(fn === 'readGroupPolicy' ? 'group' : 'credential', bad)
    let threw = false
    try {
      ctx[fn](raw)
    } catch (e) {
      threw = e instanceof ctx.InvalidResponseError
    }
    if (!threw) {
      console.error(`FAIL: ${fn} accepted malformed revision_text ${bad}`)
      process.exit(1)
    }
  }
}
console.log('PASS: Malformed and overflowing revision_text rejected by binding readers')

function mockPreviewResponse() {
  return {
    snapshot_revision_text: '18446744073709551615',
    server_time: '2026-10-03T13:45:13+08:00',
    server_time_zone_offset: '+08:00',
    caveat_codes: [],
    candidates: [
      {
        credential_id: 1,
        credential_name: 'Credential 1',
        credential_version: '18446744073709551615',
        identity_generation: '18446744073709551614',
        targets: [
          {
            upstream_model: 'upstream-gpt4',
            available: true,
            group_rules: [
              {
                rule_id: 'r',
                name_snapshot: 'R',
                domain: 'pricing',
                enabled: true,
                status: 'hit',
                binding_scope: 'group',
                provenance: 'saved',
                revision_text: '18446744073709551615',
                condition: { kind: 'param', fact: 'request.model', truth: 'true' },
                action: { type: 'multiply_price', factor: '2', multiplier: '2' },
              },
            ],
            credential_rules: [],
            scheduling: { excluded: false },
            pricing: {
              matches: [
                {
                  rule_id: 'r',
                  name_snapshot: 'R',
                  domain: 'pricing',
                  factor: '2',
                  multiplier: '2',
                  binding_scope: 'group',
                  provenance: 'saved',
                  revision_text: '18446744073709551615',
                },
              ],
              factors: ['2'],
              cumulative_multiplier: '2',
            },
          },
        ],
      },
    ],
  }
}

// Ensure valid base parses
const validParsed = ctx.readPolicyPreviewResponse(mockPreviewResponse())
if (
  !validParsed ||
  validParsed.candidates.length !== 1 ||
  validParsed.candidates[0].targets[0].group_rules[0].rule_id !== 'r'
) {
  console.error('FAIL: readPolicyPreviewResponse failed to parse valid base preview response')
  process.exit(1)
}

// Test corrupt non-object or non-list candidates
for (const bad of [null, 42, 'string', { candidates: 'not-a-list' }]) {
  let threw = false
  try {
    ctx.readPolicyPreviewResponse(bad)
  } catch {
    threw = true
  }
  if (!threw) {
    console.error('FAIL: readPolicyPreviewResponse accepted malformed payload')
    process.exit(1)
  }
}
console.log('PASS: readPolicyPreviewResponse strictly rejects malformed payloads')

// Verify actual API preview promises smoke test
for (const fn of ['previewGroupPolicy', 'previewCredentialPolicy']) {
  const isGroup = fn === 'previewGroupPolicy'
  const client = { request: async () => mockPreviewResponse() }
  const res = isGroup
    ? await ctx.previewGroupPolicy(client, 1, 'gpt-4o', null, null, new AbortController().signal)
    : await ctx.previewCredentialPolicy(
        client,
        1,
        1,
        'gpt-4o',
        null,
        null,
        new AbortController().signal,
      )
  if (!res || res.candidates.length !== 1) {
    console.error(`FAIL: ${fn} promise smoke test failed`)
    process.exit(1)
  }
}
console.log('PASS: Actual API preview promises project core DTO shapes')

// Verify recursive condition tree with 2 children through actual API promise
const goodTreeResponse = {
  snapshot_revision_text: '2',
  server_time: '2026-10-03T14:07:10+08:00',
  server_time_zone_offset: '+08:00',
  caveat_codes: [],
  candidates: [
    {
      credential_id: 1,
      credential_name: 'Credential 1',
      credential_version: '1',
      identity_generation: '6261189100204378901',
      targets: [
        {
          upstream_model: 'upstream-gpt4',
          available: true,
          group_rules: [],
          credential_rules: [
            {
              rule_id: 's',
              name_snapshot: 'S',
              domain: 'scheduling',
              enabled: true,
              status: 'hit',
              binding_scope: 'credential',
              provenance: 'draft',
              revision_text: 'draft',
              condition: {
                kind: 'all',
                truth: 'true',
                children: [
                  { kind: 'param', fact: 'request.model', truth: 'true' },
                  { kind: 'param', fact: 'request.model', truth: 'true' },
                ],
              },
              action: { type: 'exclude_candidate' },
            },
          ],
          scheduling: {
            excluded: true,
            reason: { rule_id: 's', name_snapshot: 'S', domain: 'scheduling' },
          },
          pricing: { matches: [], factors: [], cumulative_multiplier: '1' },
        },
      ],
    },
  ],
}

const parsedTreeResult = await ctx.previewCredentialPolicy(
  { request: async () => goodTreeResponse },
  1,
  1,
  'gpt-4o',
  null,
  null,
  new AbortController().signal,
)
const condNode = parsedTreeResult.candidates[0].targets[0].credential_rules[0].condition
if (!condNode.children || condNode.children.length !== 2) {
  console.error('FAIL: Condition tree did not preserve 2 children, got:', condNode.children)
  process.exit(1)
}
console.log('PASS: Recursive condition tree preserves 2 children through actual API promise')

// Verify custom extension string and number descriptors admitted
const validCustomDiscovery = {
  parameters: [
    {
      key: 'provider.custom_string',
      type: 'string',
      label: 'custom.string.label',
      description: 'custom.string.desc',
      operators: ['eq', 'in'],
      domains: ['scheduling', 'pricing'],
      binding_scopes: ['group', 'credential'],
    },
    {
      key: 'provider.custom_number',
      type: 'number',
      unit: 'custom_ratio',
      label: 'custom.number.label',
      description: 'custom.number.desc',
      operators: ['eq', 'lt', 'lte', 'gt', 'gte'],
      domains: ['pricing'],
      binding_scopes: ['credential'],
      selector_constraint: {
        scope: 'account',
        window_seconds_required: true,
      },
      reducers: ['min'],
    },
  ],
  predicates: [],
  actions: [],
  capabilities: {
    account_wise: true,
    group_aggregation: false,
    fixed_recovery: 'unsupported',
    live_dynamic_pricing: false,
  },
}

const parsedExt = await ctx.getPolicyDiscovery(
  { request: async () => validCustomDiscovery },
  new AbortController().signal,
)
if (parsedExt.parameters.length !== 2 || parsedExt.parameters[0].key !== 'provider.custom_string') {
  console.error('FAIL: Custom extension descriptors rejected')
  process.exit(1)
}
console.log('PASS: getPolicyDiscovery projects descriptors and preserves extensions')

// Verify configText retains exact decimal formatting
const decimalRaw = {
  scope: 'group',
  id: 1,
  group_id: 1,
  revision_text: '1',
  config_text:
    '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}',
}
const parsedDecimal = ctx.readGroupPolicy(decimalRaw)
if (!parsedDecimal.configText.includes('0.10000000000000001')) {
  console.error('FAIL: Decimal literal was not preserved in configText')
  process.exit(1)
}
console.log('PASS: Exact raw decimal literal preserved in configText')

// 2. 真实编译面板冒烟：模式切换与空白态提示（不再包含 preview/discovery UI）
const panelPath = 'web/src/frontends/modern/features/groups/GroupPolicyPanel.vue'
const blankPolicy = ctx.readGroupPolicy(mockPolicy('group', '0'))
const uiStub = vue.defineComponent({
  inheritAttrs: false,
  setup:
    (_, { attrs, slots }) =>
    () =>
      vue.h('ui-mock', attrs, slots.default?.() ?? []),
})
const uiProxy = new Proxy({}, { get: () => uiStub })
const queryCache = {
  cancelQueries: async () => {},
  setQueryData: () => {},
  invalidateQueries: async () => {},
}
const panelMocks = {
  groupPolicyKey: () => ['g'],
  credentialPolicyKey: () => ['c'],
  getGroupPolicy: () => {},
  getCredentialPolicy: () => {},
  saveGroupPolicy: async () => blankPolicy,
  saveCredentialPolicy: async () => blankPolicy,
}
// 真实 clipboard helper（HTTP 回退路径）：不用 jsdom，只做最小 fake DOM。
const copied = []
let lastTextarea = null
const fakeTextarea = () => ({
  style: {},
  value: '',
  isConnected: true,
  focus() {},
  select() {},
  remove() {},
})
const clipboard = loadTsModule(
  stripImports(readSource('web/src/frontends/modern/components/ui/clipboard.ts')),
  {
    shallowRef: vue.shallowRef,
    isSecureContext: false,
    navigator: {},
    HTMLElement: class {},
    document: {
      activeElement: null,
      body: { append() {} },
      createElement: () => (lastTextarea = fakeTextarea()),
      execCommand: () => {
        copied.push(lastTextarea?.value ?? '')
        return true
      },
    },
  },
)
const model = loadTsModule(
  stripImports(
    readSource('web/src/frontends/modern/features/groups/policy-editor/policy-model.ts'),
  ),
)
const panelRequire = (name) => {
  if (name === 'vue') return vue
  if (name === 'vue-i18n') return { useI18n: () => ({ t: (key) => key }) }
  if (name === '@tanstack/vue-query')
    return {
      useQuery: () => ({
        data: vue.ref(blankPolicy),
        isPending: vue.ref(false),
        isError: vue.ref(false),
      }),
      useQueryClient: () => queryCache,
    }
  if (name === '@modern/api/group-detail') return panelMocks
  if (name === './policy-editor/policy-model') return model
  if (name === '@modern/components/ui/clipboard') return clipboard
  if (name === '@shared/http/client-context') return { useApiClient: () => ({}) }
  if (name === '@shared/http/errors') return { ApiError: class extends Error {} }
  if (name === '@modern/components/ui') return uiProxy
  return { __esModule: true, default: uiStub }
}
const panelCtx = compileSfc(panelPath, readSource(panelPath), panelRequire, {
  transform: (code) =>
    code.replace(
      'return (_ctx: any,_cache: any) =>',
      'globalThis.__state={editorMode, draft}; return (_ctx: any,_cache: any) =>',
    ),
  extra: { AbortController, console },
})
const { root, errors } = mountComponent(panelCtx.exports.default, { group: { id: 1, name: 'g' } })
if (errors.length) {
  console.error('FAIL: panel render produced errors:', errors)
  process.exit(1)
}
const contains = (node, pattern) => {
  if (typeof node.text === 'string' && node.text.includes(pattern)) return true
  return (node.children ?? []).some((child) => contains(child, pattern))
}
const pick = (node, predicate) => {
  if (predicate(node)) return node
  for (const child of node.children ?? []) {
    const hit = pick(child, predicate)
    if (hit) return hit
  }
  return null
}
if (!contains(root, 'groupDetail.policy.emptyState')) {
  console.error('FAIL: panel did not render the unconfigured blank state')
  process.exit(1)
}
const jsonButton = pick(
  root,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.modeJson'),
)
if (!jsonButton) {
  console.error('FAIL: panel did not render the JSON mode button')
  process.exit(1)
}
jsonButton.events.onClick()
await vue.nextTick()
if (panelCtx.__state.editorMode.value !== 'json') {
  console.error('FAIL: mode click did not switch the editor to JSON')
  process.exit(1)
}

// HTTP 回退：compiled 面板复制按钮直接复制当前显示的 pretty 源文本（不做业务校验）。
const copyButton = pick(
  root,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.copyJson'),
)
if (!copyButton) {
  console.error('FAIL: panel did not render the copy JSON button')
  process.exit(1)
}
copyButton.events.onClick()
await vue.nextTick()
const displayed = panelCtx.__state.draft.value
if (!displayed.includes('\n')) {
  console.error('FAIL: panel draft is not the pretty source text')
  process.exit(1)
}
if (copied.length !== 1 || copied[0] !== displayed) {
  console.error('FAIL: copy button did not copy the displayed pretty source text')
  process.exit(1)
}
if (contains(root, 'groupDetail.policy.clipboardUnavailable')) {
  console.error('FAIL: copy reported the legacy clipboardUnavailable error on success')
  process.exit(1)
}
console.log('PASS: Panel copy uses the real clipboard fallback on HTTP')
console.log('PASS: Panel mode click and blank ghost verified on compiled SFC')
