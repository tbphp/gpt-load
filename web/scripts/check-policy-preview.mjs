import {
  compileSfc,
  extract,
  loadTsModule,
  mountComponent,
  parseSfc,
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

// 面板新增导入 policy-portability；用真实模块避免被 mock 成 undefined。
const portability = loadTsModule(
  stripImports(readSource('web/src/frontends/modern/features/groups/policy-portability.ts')),
  {
    TextEncoder,
    createUUID: () => 'generated-uuid-0000-0000-0000-000000000000',
  },
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

// 2. Editor Lifecycle & Invalidation Tests
class ApiError extends Error {
  constructor(status, data = null, code = '') {
    super(code || `Error ${status}`)
    this.status = status
    this.data = data
    this.code = code
  }
}

function createEditorEnv(isCredential = false, initialRev = '0') {
  const p = 'web/src/frontends/modern/features/groups/GroupPolicyPanel.vue'
  const desc = parseSfc(readSource(p)).descriptor
  const requests = []
  const saves = []
  let resolvePreview = () => {}
  let rejectPreview = () => {}
  const readFn = isCredential ? ctx.readCredentialPolicy : ctx.readGroupPolicy
  const query = {
    data: vue.ref(readFn(mockPolicy(isCredential ? 'credential' : 'group', initialRev))),
    isPending: vue.ref(false),
    isError: vue.ref(false),
    refetch: () => {},
  }
  const inheritedQuery = {
    data: vue.ref(ctx.readGroupPolicy(mockPolicy('group', '1'))),
    isPending: vue.ref(false),
    isError: vue.ref(false),
    refetch: () => {},
  }
  const props = vue.reactive({
    group: { id: 1, name: 'Group 1' },
    credential: isCredential ? { id: 1, label: 'Cred 1' } : undefined,
  })
  const env = {
    ...vue,
    console,
    exports: {},
    AbortController,
    defineProps: () => props,
    defineEmits: () => () => {},
    useI18n: () => ({ t: (x) => x, locale: vue.ref('en-US') }),
    useApiClient: () => ({}),
    useQueryClient: () => ({
      cancelQueries: async () => {},
      setQueryData: () => {},
      invalidateQueries: async () => {},
    }),
    useQuery: (opts) => {
      if (opts?.enabled?.value === true || opts?.enabled === true) return inheritedQuery
      return query
    },
    groupPolicyKey: () => ['g'],
    credentialPolicyKey: () => ['c'],
    policyDiscoveryKey: () => ['d'],
    getPolicyDiscovery: async () => undefined,
    ApiError,
    previewGroupPolicy: (...a) => {
      requests.push(a)
      return new Promise((r, j) => {
        resolvePreview = r
        rejectPreview = j
      })
    },
    previewCredentialPolicy: (...a) => {
      requests.push(a)
      return new Promise((r, j) => {
        resolvePreview = r
        rejectPreview = j
      })
    },
    saveGroupPolicy: async (...a) => {
      saves.push(a)
      if (env.conflict) throw new ApiError(409)
      const rawText = a[3] || '{"schema_version":1,"rules":[]}'
      return ctx.readGroupPolicy({
        ...mockPolicy('group', '2'),
        config_text: rawText,
      })
    },
    saveCredentialPolicy: async (...a) => {
      saves.push(a)
      if (env.conflict) throw new ApiError(409)
      const rawText = a[4] || '{"schema_version":1,"rules":[]}'
      return ctx.readCredentialPolicy({
        ...mockPolicy('credential', '2'),
        config_text: rawText,
      })
    },
  }
  const scope = vue.effectScope()
  scope.run(() =>
    loadTsModule(
      stripImports(desc.scriptSetup.content) +
        `;exports.st={draft,baseline,currentPolicy,isUnconfigured,serverError,previewRequestModel,previewSimulatedTime,previewData,previewError,previewLoading,runPreview,save};`,
      env,
    ),
  )
  return {
    st: env.exports.st,
    env,
    requests,
    saves,
    scope,
    resolvePreview: (v) => resolvePreview(v),
    rejectPreview: (e) => rejectPreview(e),
  }
}

for (const isCredential of [false, true]) {
  const modeName = isCredential ? 'Credential mode' : 'Group mode'

  // MaxUint64 must NOT be unconfigured
  const maxUintEnv = createEditorEnv(isCredential, '18446744073709551615')
  if (maxUintEnv.st.isUnconfigured.value !== false) {
    console.error(`FAIL: ${modeName} treated MaxUint64 as unconfigured`)
    process.exit(1)
  }
  maxUintEnv.scope.stop()

  // rev 0 MUST be unconfigured
  const zeroEnv = createEditorEnv(isCredential, '0')
  if (zeroEnv.st.isUnconfigured.value !== true) {
    console.error(`FAIL: ${modeName} failed to identify revision 0 as unconfigured`)
    process.exit(1)
  }
  zeroEnv.scope.stop()

  // 409 conflict retention on save
  const conflictEnv = createEditorEnv(isCredential, '1')
  const dirtyDraft = '{"schema_version":1,"rules":[]}\n  '
  conflictEnv.st.draft.value = dirtyDraft
  conflictEnv.env.conflict = true
  await conflictEnv.st.save()
  if (conflictEnv.st.draft.value !== dirtyDraft) {
    console.error(`FAIL: ${modeName} lost dirty draft text on 409 conflict`)
    process.exit(1)
  }
  if (!conflictEnv.st.serverError.value) {
    console.error(`FAIL: ${modeName} did not display conflict error message`)
    process.exit(1)
  }
  conflictEnv.scope.stop()

  // Pending change aborts in-flight request
  for (const field of ['draft', 'previewRequestModel', 'previewSimulatedTime']) {
    const pendingEnv = createEditorEnv(isCredential, '1')
    pendingEnv.st.previewRequestModel.value = 'gpt-4o'
    await vue.nextTick()
    const p = pendingEnv.st.runPreview()
    pendingEnv.st[field].value += ' '
    await vue.nextTick()
    pendingEnv.resolvePreview({ candidates: [], server_time: 'stale' })
    await p
    if (pendingEnv.st.previewData.value !== null) {
      console.error(`FAIL: ${modeName} accepted stale response after modifying ${field}`)
      process.exit(1)
    }
    if (!pendingEnv.requests[0]?.at(-1)?.aborted) {
      console.error(`FAIL: ${modeName} did not abort signal when ${field} changed`)
      process.exit(1)
    }
    pendingEnv.scope.stop()
  }

  // Completed preview invalidation
  for (const field of ['draft', 'previewRequestModel', 'previewSimulatedTime']) {
    const compEnv = createEditorEnv(isCredential, '1')
    compEnv.st.previewRequestModel.value = 'gpt-4o'
    await vue.nextTick()
    const p = compEnv.st.runPreview()
    compEnv.resolvePreview({ candidates: [], server_time: 'completed' })
    await p
    if (!compEnv.st.previewData.value) {
      console.error(`FAIL: ${modeName} failed to populate completed preview data`)
      process.exit(1)
    }
    compEnv.st[field].value += ' '
    await vue.nextTick()
    if (compEnv.st.previewData.value !== null) {
      console.error(`FAIL: ${modeName} did not invalidate completed preview when ${field} changed`)
      process.exit(1)
    }
    compEnv.scope.stop()
  }

  // ABA draft change resistance: A -> B -> A with slow B response
  const abaEnv = createEditorEnv(isCredential, '1')
  abaEnv.st.previewRequestModel.value = 'gpt-4o'
  const draftA = '{"schema_version":1,"rules":[]}'
  const draftB = '{"schema_version":1,"rules":[{"id":"b"}]}'
  abaEnv.st.draft.value = draftA
  await vue.nextTick()

  abaEnv.st.draft.value = draftB
  await vue.nextTick()
  const pB = abaEnv.st.runPreview()

  abaEnv.st.draft.value = draftA
  await vue.nextTick()
  abaEnv.resolvePreview({ candidates: [{ credential_id: 99 }], server_time: 'from-B' })
  await pB
  if (abaEnv.st.previewData.value !== null) {
    console.error(`FAIL: ${modeName} accepted preview from draft B after returning to draft A`)
    process.exit(1)
  }
  abaEnv.scope.stop()

  // Blank JSON stops preview without dispatching HTTP
  const blankEnv = createEditorEnv(isCredential, '1')
  blankEnv.st.previewRequestModel.value = 'gpt-4o'
  blankEnv.st.draft.value = '   '
  await vue.nextTick()
  await blankEnv.st.runPreview()
  if (blankEnv.requests.length !== 0) {
    console.error(`FAIL: ${modeName} dispatched preview HTTP request for blank JSON`)
    process.exit(1)
  }
  if (!blankEnv.st.previewError.value) {
    console.error(`FAIL: ${modeName} did not set previewError for blank JSON`)
    process.exit(1)
  }
  blankEnv.scope.stop()

  // Invalid JSON stops save without dispatching HTTP
  const invalidSaveEnv = createEditorEnv(isCredential, '1')
  invalidSaveEnv.st.draft.value = '{ invalid json'
  await vue.nextTick()
  await invalidSaveEnv.st.save()
  if (invalidSaveEnv.saves.length !== 0) {
    console.error(`FAIL: ${modeName} dispatched save HTTP request for invalid JSON`)
    process.exit(1)
  }
  if (!invalidSaveEnv.st.serverError.value) {
    console.error(`FAIL: ${modeName} did not set serverError for invalid JSON on save`)
    process.exit(1)
  }
  invalidSaveEnv.scope.stop()
}
console.log('PASS: Editor lifecycle, ABA resistance, and blank/invalid halts verified')

// 3. SFC Rendering Tests
const discoveryFixture = {
  parameters: [
    {
      key: 'fact.credential.quota.remaining_ratio',
      type: 'number',
      label: 'fact.credential.quota.remaining_ratio.label',
      description: 'fact.credential.quota.remaining_ratio.desc',
      operators: ['eq', 'lt', 'lte', 'gt', 'gte'],
      domains: ['pricing'],
      binding_scopes: ['credential'],
    },
  ],
  predicates: [
    {
      name: 'time_window',
      label: 'predicate.time_window.label',
      description: 'predicate.time_window.desc',
      domains: ['scheduling', 'pricing'],
    },
  ],
  actions: [
    {
      type: 'multiply_price',
      domain: 'pricing',
      label: 'action.multiply_price.label',
      description: 'action.multiply_price.desc',
      fields: ['type', 'factor'],
    },
  ],
  capabilities: {
    account_wise: true,
    group_aggregation: false,
    fixed_recovery: 'unsupported',
    live_dynamic_pricing: false,
  },
}

for (const isCredential of [false, true]) {
  const modeName = isCredential ? 'Credential mode' : 'Group mode'
  const p = 'web/src/frontends/modern/features/groups/GroupPolicyPanel.vue'
  let resolve
  const response = {
    snapshot_revision_text: '18446744073709551615',
    server_time: '2026-10-03T13:45:13+08:00',
    server_time_zone_offset: '+08:00',
    caveat_codes: ['policy.preview.caveat.no_session_affinity'],
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
            credential_rules: [
              {
                rule_id: 'r',
                name_snapshot: 'R',
                domain: 'pricing',
                enabled: true,
                status: 'hit',
                binding_scope: 'credential',
                provenance: 'saved',
                revision_text: '18446744073709551614',
                condition: { kind: 'param', fact: 'request.model', truth: 'true' },
                action: { type: 'multiply_price', factor: '2', multiplier: '2' },
              },
            ],
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
                {
                  rule_id: 'r',
                  name_snapshot: 'R',
                  domain: 'pricing',
                  factor: '2',
                  multiplier: '2',
                  binding_scope: 'credential',
                  provenance: 'saved',
                  revision_text: '18446744073709551614',
                },
              ],
              factors: ['2', '2'],
              cumulative_multiplier: '4',
            },
          },
        ],
      },
    ],
  }
  const passthrough = vue.defineComponent({
    setup:
      (props, { slots }) =>
      () =>
        slots.default?.(),
  })
  const mocks = {
    getGroupPolicy: () => {},
    getCredentialPolicy: () => {},
    getPolicyDiscovery: () => {},
    groupPolicyKey: () => ['g'],
    credentialPolicyKey: () => ['c'],
    policyDiscoveryKey: () => ['d'],
    previewGroupPolicy: () => new Promise((r) => (resolve = r)),
    previewCredentialPolicy: () => new Promise((r) => (resolve = r)),
  }
  const cache = {
    cancelQueries: async () => {},
    setQueryData: () => {},
    invalidateQueries: async () => {},
  }
  const requireFn = (name) => {
    if (name === 'vue') return vue
    if (name === '@tanstack/vue-query')
      return {
        useQuery: (opts) => {
          const key = vue.unref(opts.queryKey)
          const isDisco = Array.isArray(key) && key[0] === 'd'
          const isInherited = opts?.enabled?.value === true || opts?.enabled === true
          let polData
          if (isInherited) {
            polData = ctx.readGroupPolicy(mockPolicy('group'))
          } else if (isCredential) {
            polData = ctx.readCredentialPolicy(mockPolicy('credential'))
          } else {
            polData = ctx.readGroupPolicy(mockPolicy('group'))
          }
          return {
            data: vue.ref(isDisco ? discoveryFixture : polData),
            isPending: vue.ref(false),
            isError: vue.ref(false),
          }
        },
        useQueryClient: () => cache,
      }
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (x) => x, locale: vue.ref('en-US') }) }
    if (name === '@modern/api/group-detail') return mocks
    if (name === './policy-portability') return portability
    if (name === '@shared/http/client-context') return { useApiClient: () => ({}) }
    if (name === '@shared/http/errors') return { ApiError: class extends Error {} }
    if (name === '@modern/components/ui') return new Proxy({}, { get: () => passthrough })
    return { __esModule: true, default: passthrough }
  }

  const ctxRender = compileSfc(p, readSource(p), requireFn, {
    transform: (code) =>
      code.replace(
        'return (_ctx: any,_cache: any) =>',
        'globalThis.__state={draft,previewRequestModel,previewData,runPreview}; return (_ctx: any,_cache: any) =>',
      ),
    extra: { AbortController, console },
  })
  const { root, errors } = mountComponent(ctxRender.exports.default, {
    group: { id: 1, name: 'test-group' },
    credential: isCredential ? { id: 1, label: 'test-cred' } : undefined,
  })

  const state = ctxRender.__state
  state.previewRequestModel.value = 'gpt-4o'
  await vue.nextTick()
  const runP = state.runPreview()
  resolve(response)
  await runP
  await vue.nextTick()

  if (errors.length) {
    console.error(`FAIL: ${modeName} render produced errors:`, errors)
    process.exit(1)
  }

  const findText = (n, pat) => {
    if (typeof n.text === 'string' && n.text.includes(pat)) return true
    return n.children.some((c) => findText(c, pat))
  }

  // Factor badge: x2
  if (!findText(root, '×2')) {
    console.error(`FAIL: ${modeName} did not render rule factor badge (x2)`)
    process.exit(1)
  }
  // Cumulative multiplier badge: x4
  if (!findText(root, '×4')) {
    console.error(`FAIL: ${modeName} did not render cumulative multiplier badge (x4)`)
    process.exit(1)
  }
  // Provenance rev badge
  if (!findText(root, 'rev 18446744073709551615')) {
    console.error(`FAIL: ${modeName} did not render exact full uint64 revision text badge`)
    process.exit(1)
  }

  // Translated metadata check
  if (!findText(root, 'Credential Quota Remaining Ratio')) {
    console.error(`FAIL: ${modeName} did not render translated descriptor label`)
    process.exit(1)
  }
}
console.log('PASS: SFC rendering and discovery metadata translation verified')
