import assert from 'node:assert/strict'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { enUS, jaJP, zhCN } from '../src/frontends/modern/i18n/locales/group-detail.ts'

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
      'readPolicy',
      'getPolicy',
      'readPolicyDiscovery',
      'savePolicy',
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
    const res = ctx.readPolicy(raw, 1, fn === 'readCredentialPolicy' ? 1 : undefined)
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
      ctx.readPolicy(raw, 1, fn === 'readCredentialPolicy' ? 1 : undefined)
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

// Scoped discovery：面板只发一个隐藏 query 取真实 quota 窗口。
let discoveryPath = ''
const scopedExt = await ctx.getPolicyDiscovery(
  {
    request: async (path) => {
      discoveryPath = path
      return { quota_windows: [604800, 18000] }
    },
  },
  new AbortController().signal,
  7,
  9,
)
if (discoveryPath !== '/api/policy/discovery?group_id=7&credential_id=9') {
  console.error(`FAIL: scoped discovery path was ${discoveryPath}`)
  process.exit(1)
}
if (JSON.stringify(scopedExt.quota_windows) !== JSON.stringify([604800, 18000])) {
  console.error('FAIL: scoped discovery dropped quota_windows')
  process.exit(1)
}
console.log('PASS: Scoped discovery query carries group/credential and real quota windows')

// Verify configText retains exact decimal formatting for both scopes
const decimalRaw = {
  scope: 'group',
  id: 1,
  group_id: 1,
  credential_id: 1,
  revision_text: '1',
  config_text:
    '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}',
}
for (const fn of ['readGroupPolicy', 'readCredentialPolicy']) {
  const scope = fn === 'readGroupPolicy' ? 'group' : 'credential'
  if (
    !ctx
      .readPolicy({ ...decimalRaw, scope }, 1, scope === 'credential' ? 1 : undefined)
      .configText.includes('0.10000000000000001')
  ) {
    console.error(`FAIL: ${fn} dropped the exact decimal literal in configText`)
    process.exit(1)
  }
}
console.log('PASS: Exact raw decimal literal preserved in configText')

// 2. 真实编译面板冒烟：模式切换与空白态提示（不再包含 preview/discovery UI）
const panelPath = 'web/src/frontends/modern/features/groups/GroupPolicyPanel.vue'
const blankPolicy = ctx.readPolicy(mockPolicy('group', '0'), 1)
const uiStub = vue.defineComponent({
  inheritAttrs: false,
  setup:
    (_, { attrs, slots }) =>
    () =>
      vue.h(
        'ui-mock',
        attrs,
        Object.values(slots).flatMap((slot) => (slot ? slot() : [])),
      ),
})
const uiProxy = new Proxy({}, { get: () => uiStub })
const invalidatedQueries = []
const queryCache = {
  cancelQueries: async () => {},
  setQueryData: () => {},
  invalidateQueries: async (opts) => {
    invalidatedQueries.push(opts?.queryKey)
  },
}
class MockApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}
let saveCredentialHandler = async () => blankPolicy
let getCredentialHandler = async () => blankPolicy
const panelMocks = {
  groupPolicyKey: (groupId) => ['modern', 'group-policy', groupId],
  credentialPolicyKey: (groupId, credentialId) => [
    'modern',
    'credential-policy',
    groupId,
    credentialId,
  ],
  policyDiscoveryKey: (groupId, credentialId) => [
    'modern',
    'policy-discovery',
    groupId,
    credentialId,
  ],
  getPolicy: (_client, _gid, _signal, cid) =>
    cid === undefined ? undefined : getCredentialHandler(_client, _gid, cid, _signal),
  getPolicyDiscovery: () => {},
  savePolicy: (client, gid, revision, text, signal, cid) =>
    cid === undefined
      ? blankPolicy
      : saveCredentialHandler(client, gid, cid, revision, text, signal),
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
const policyData = vue.ref(blankPolicy)
const groupPolicyData = vue.ref(blankPolicy)
// 面板隐藏的 scoped discovery query 返回真实数据（只用于取 quota_windows，不渲染 reference UI）。
const discoveryCalls = []
const discoveryData = vue.ref({ quota_windows: [18000, 604800] })
const model = loadTsModule(
  stripImports(
    readSource('web/src/frontends/modern/features/groups/policy-editor/policy-model.ts'),
  ),
)
let panelTranslate = (key) => key
const panelRequire = (name) => {
  if (name === 'vue') return vue
  if (name === 'vue-i18n') return { useI18n: () => ({ t: panelTranslate }) }
  if (name === '@tanstack/vue-query')
    return {
      useQuery: (options) => {
        const rawKey = options?.queryKey
        const key = typeof rawKey === 'function' ? rawKey() : (rawKey?.value ?? rawKey)
        const keyList = Array.isArray(key) ? key : []
        if (keyList.includes('policy-discovery')) {
          discoveryCalls.push(keyList)
          return {
            data: discoveryData,
            isPending: vue.ref(false),
            isError: vue.ref(false),
          }
        }
        if (keyList.includes('group-policy')) {
          return {
            data: groupPolicyData,
            isPending: vue.ref(false),
            isError: vue.ref(false),
          }
        }
        return {
          data: policyData,
          isPending: vue.ref(false),
          isError: vue.ref(false),
        }
      },
      useQueryClient: () => queryCache,
    }
  if (name === '@modern/api/group-detail') return panelMocks
  if (name === './policy-editor/policy-model') return model
  if (name === '@modern/components/ui/clipboard') return clipboard
  if (name === '@shared/http/client-context') return { useApiClient: () => ({}) }
  if (name === '@shared/http/errors') return { ApiError: MockApiError }
  if (name === '@modern/components/ui') return uiProxy
  return { __esModule: true, default: uiStub }
}
const panelCtx = compileSfc(panelPath, readSource(panelPath), panelRequire, {
  transform: (code) =>
    code.replace(
      'return (_ctx: any,_cache: any) =>',
      'globalThis.__state={editorMode, draft, dirty, serverError, switchEditorMode}; return (_ctx: any,_cache: any) =>',
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
// 分组 scope 直接渲染编辑器（无 credential 继承/覆盖分支），仅验证模式 tab 与隐藏 discovery。
if (!contains(root, 'groupDetail.policy.modeVisual')) {
  console.error('FAIL: panel did not render the visual mode tab')
  process.exit(1)
}
// 唯一的隐藏 scoped discovery query；面板自身不渲染 reference UI。
if (discoveryCalls.length !== 1 || !discoveryCalls[0].includes('policy-discovery')) {
  console.error('FAIL: panel must issue exactly one hidden scoped discovery query')
  process.exit(1)
}
if (discoveryCalls[0][2] !== 1) {
  console.error('FAIL: discovery query is not scoped to the group id')
  process.exit(1)
}
const editorNode = pick(root, (node) => Array.isArray(node.props?.['quota-windows']))
if (!editorNode || editorNode.props['quota-windows'].join(',') !== '18000,604800') {
  console.error('FAIL: panel did not pass the discovered quota windows to the editor')
  process.exit(1)
}
console.log('PASS: Panel issues one hidden scoped discovery query without reference UI')

const jsonButton = pick(
  root,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.modeJson'),
)
if (!jsonButton) {
  console.error('FAIL: panel did not render the JSON mode button')
  process.exit(1)
}
// JSON 模式切换必须格式化当前正文，同时保留原始数值精度。
const compactDecimal =
  '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}'
panelCtx.__state.draft.value = compactDecimal
jsonButton.events.onClick()
await vue.nextTick()
if (panelCtx.__state.editorMode.value !== 'json') {
  console.error('FAIL: mode click did not switch the editor to JSON')
  process.exit(1)
}
const formatted = panelCtx.__state.draft.value
if (!formatted.includes('\n') || !formatted.includes('0.10000000000000001')) {
  console.error('FAIL: JSON mode watch did not pretty-print while preserving decimal precision')
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
if (copied.length !== 1 || copied[0] !== formatted) {
  console.error('FAIL: copy button did not copy the displayed pretty source text')
  process.exit(1)
}
if (contains(root, 'groupDetail.policy.clipboardUnavailable')) {
  console.error('FAIL: copy reported the legacy clipboardUnavailable error on success')
  process.exit(1)
}
console.log('PASS: Panel copy uses the real clipboard fallback on HTTP')
console.log('PASS: Panel mode click and blank ghost verified on compiled SFC')

// 2b. Credential 覆盖模式：分段字段与粘贴自动补 mode（保留 raw token）。
policyData.value = ctx.readPolicy(
  {
    ...mockPolicy('credential', '3'),
    config_text: '{"schema_version":1,"group_policy":"override","rules":[]}',
  },
  1,
  1,
)
const credPanel = mountComponent(panelCtx.exports.default, {
  group: { id: 1, name: 'g' },
  credential: { id: 5, label: 'cred' },
})
if (credPanel.errors.length) {
  console.error('FAIL: credential panel render produced errors:', credPanel.errors)
  process.exit(1)
}
const credRoot = credPanel.root
const modeField = pick(credRoot, (node) =>
  Array.isArray(node.props?.options)
    ? node.props.options.some((option) => option.value === 'override')
    : false,
)
if (!modeField || modeField.props.modelValue !== 'override') {
  console.error('FAIL: credential panel did not select the override mode')
  process.exit(1)
}
const credJsonTab = pick(
  credRoot,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.modeJson'),
)
credJsonTab.events.onClick()
await vue.nextTick()
const area = pick(credRoot, (node) => node.tag === 'ui-mock' && node.events?.onPaste)
const pasted =
  '{"schema_version":1,"rules":[{"id":"g","name":"模型","domain":"scheduling","enabled":true,' +
  '"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},' +
  '"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}},' +
  '{"id":"h","name":"H","domain":"scheduling","enabled":true,' +
  '"when":{"fact":"request.model","op":"eq","value":9007199254740993},"then":{"type":"exclude_candidate"}}]}'
const draft = panelCtx.__state.draft.value
area.events.onPaste({
  clipboardData: { getData: () => pasted },
  target: { selectionStart: 0, selectionEnd: draft.length },
  preventDefault() {},
})
await vue.nextTick()
const pastedText = panelCtx.__state.draft.value
for (const token of [
  '"group_policy": "override"',
  '0.10000000000000001',
  '9007199254740993',
  '模型',
]) {
  if (!pastedText.includes(token)) {
    console.error(`FAIL: credential paste lost ${token}`)
    process.exit(1)
  }
}
console.log('PASS: Credential override paste keeps mode intent and raw tokens')

// 2c. Credential 继承模式：展示只读分组策略（若有规则）同时始终提供自身规则编辑器
groupPolicyData.value = ctx.readPolicy(
  {
    ...mockPolicy('group', '1'),
    config_text:
      '{"schema_version":1,"rules":[{"id":"g-sched","name":"分组调度规则","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"m"},"then":{"type":"exclude_candidate"}}]}',
  },
  1,
)
policyData.value = ctx.readPolicy(
  {
    ...mockPolicy('credential', '4'),
    config_text:
      '{"schema_version":1,"group_policy":"inherit","rules":[{"id":"c-price","name":"账号计价规则","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"p"},"then":{"type":"multiply_price","factor":"1.5"}}]}',
  },
  1,
  1,
)
const inheritPanel = mountComponent(panelCtx.exports.default, {
  group: { id: 1, name: 'g' },
  credential: { id: 6, label: 'cred-inherit' },
})
if (inheritPanel.errors.length) {
  console.error('FAIL: credential inherit panel produced errors:', inheritPanel.errors)
  process.exit(1)
}
const inheritRoot = inheritPanel.root
const collectAll = (node, predicate, out = []) => {
  if (predicate(node)) out.push(node)
  for (const child of node.children ?? []) collectAll(child, predicate, out)
  return out
}
const editors = collectAll(
  inheritRoot,
  (n) => n.tag === 'ui-mock' && Array.isArray(n.props?.['quota-windows']),
)
if (editors.length !== 2) {
  console.error(
    `FAIL: credential inherit mode must render 2 editors (1 readonly group + 1 own), got ${editors.length}`,
  )
  process.exit(1)
}
const readonlyEditor = editors.find((e) => e.props.disabled === true || e.props.disabled === '')
const editableEditor = editors.find((e) => e.props.disabled === false)
if (!readonlyEditor || !editableEditor) {
  console.error('FAIL: could not identify readonly group editor and editable own editor')
  process.exit(1)
}
const readonlyProp = readonlyEditor.props.modelValue ?? readonlyEditor.props['model-value']
if (!readonlyProp?.includes('分组调度规则')) {
  console.error(
    'FAIL: inherited group policy editor did not receive distinct group fixture, got:',
    readonlyProp,
  )
  process.exit(1)
}
if (!panelCtx.__state.draft.value?.includes('账号计价规则')) {
  console.error(
    'FAIL: editable credential policy editor did not receive distinct credential fixture',
  )
  process.exit(1)
}
console.log('PASS: Credential inherit renders readonly group editor and editable own editor')

// 2d. 子组件 invalid draft 阻止切换为 JSON 模式，防止旧值保存丢草稿
editableEditor.events['onDraftStatus']?.({ valid: false, pending: false, hasInvalid: true })
await vue.nextTick()
assert.equal(panelCtx.__state.dirty.value, true)
const beforeRefresh = panelCtx.__state.draft.value
const savedPolicyData = policyData.value
policyData.value = {
  ...savedPolicyData,
  configText: savedPolicyData.configText.replace('1.5', '5'),
}
await vue.nextTick()
assert.equal(panelCtx.__state.draft.value, beforeRefresh)
policyData.value = savedPolicyData
const jsonTab = pick(
  inheritRoot,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.modeJson'),
)
if (!jsonTab) {
  console.error('FAIL: could not find JSON mode tab in inherit panel')
  process.exit(1)
}
jsonTab.events.onClick()
await vue.nextTick()
if (panelCtx.__state.editorMode.value === 'json') {
  console.error('FAIL: panel allowed switching to JSON while child draft is invalid')
  process.exit(1)
}
if (!contains(inheritRoot, 'groupDetail.policy.invalidDraftBlockJson')) {
  console.error('FAIL: panel did not display invalidDraftBlockJson notice')
  process.exit(1)
}
// 修复草稿后允许切换为 JSON 模式
editableEditor.events['onDraftStatus']?.({ valid: true, pending: false, hasInvalid: false })
await vue.nextTick()
jsonTab.events.onClick()
await vue.nextTick()
if (panelCtx.__state.editorMode.value !== 'json') {
  console.error('FAIL: panel did not allow switching to JSON after child draft was resolved')
  process.exit(1)
}
console.log('PASS: Child invalid draft blocks switching to JSON mode')

// 2e. 409 冲突简化恢复路径回归：不丢 draft/revision，不发第二 fetch，不自动覆盖，不关闭；
// 引导用户复制已提交正文、确认取消并重开获取最新 revision 后完成第二次 CAS。
panelCtx.__state.draft.value = panelCtx.__state.draft.value.replace('"1.5"', '"2.5"')
const userDraftBefore409 = panelCtx.__state.draft.value
const remoteNewerPolicy = ctx.readPolicy(
  {
    ...mockPolicy('credential', '99'),
    id: 6,
    credential_id: 6,
    config_text:
      '{"schema_version":1,"rules":[{"id":"c-newer","name":"远端新规则","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"p"},"then":{"type":"multiply_price","factor":"3"}}]}',
  },
  1,
  6,
)
let remoteReads = 0
getCredentialHandler = async () => {
  remoteReads++
  return remoteNewerPolicy
}
saveCredentialHandler = async () => {
  throw new MockApiError(409, 'conflict')
}

// 触发 save
const workspacePanel = pick(inheritRoot, (node) => node.tag === 'ui-mock' && node.events?.onSave)
if (!workspacePanel) {
  console.error('FAIL: could not find workspace panel to trigger save')
  process.exit(1)
}
invalidatedQueries.length = 0
await workspacePanel.events.onSave()
await vue.nextTick()

assert.equal(panelCtx.__state.serverError.value, 'groupDetail.policy.conflict')
assert.equal(panelCtx.__state.draft.value, userDraftBefore409, '409 retains user draft')
assert.equal(panelCtx.__state.dirty.value, true, 'panel remains dirty on 409')
assert.equal(remoteReads, 0, '409 must not issue a second fetch')
assert.equal(invalidatedQueries.length, 1, '409 must invalidate existing query')

// 复制功能：只能复制当前已格式化的合法正文（未提交/非法的局部草稿已被阻止入正文）
const copyButtonInherit = pick(
  inheritRoot,
  (node) =>
    node.tag === 'ui-mock' && node.events?.onClick && contains(node, 'groupDetail.policy.copyJson'),
)
if (!copyButtonInherit) {
  console.error('FAIL: could not find copyJson button in inherit panel')
  process.exit(1)
}
copyButtonInherit.events.onClick()
await vue.nextTick()
const copiedDraft = copied[copied.length - 1]
assert.ok(copiedDraft.includes('"2.5"'), 'copyJson copies the submitted valid draft text')

// 取消使用真实 GroupWorkspacePanel.close -> AppDraftGuard.confirm
let confirmDialogState = null
const guardSource = readSource('web/src/frontends/modern/components/AppDraftGuard.vue')
const guardCtx = compileSfc(
  'web/src/frontends/modern/components/AppDraftGuard.vue',
  guardSource,
  (name) => {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (k) => k }) }
    if (name === 'vue-router')
      return { onBeforeRouteLeave: () => {}, onBeforeRouteUpdate: () => {} }
    if (name === '@modern/components/ui')
      return {
        AppConfirmDialog: vue.defineComponent({
          props: ['open'],
          emits: ['confirm', 'cancel'],
          setup(props, { emit }) {
            confirmDialogState = { props, emit }
            return () => vue.h('confirm-dialog-mock', { open: props.open })
          },
        }),
      }
    return {
      __esModule: true,
      default: vue.defineComponent({ inheritAttrs: false, setup: () => () => null }),
    }
  },
  { extra: { window: { addEventListener: () => {}, removeEventListener: () => {} } } },
)
const workspaceSource = readSource(
  'web/src/frontends/modern/features/groups/GroupWorkspacePanel.vue',
)
const workspaceCtx = compileSfc(
  'web/src/frontends/modern/features/groups/GroupWorkspacePanel.vue',
  workspaceSource,
  (name) => {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (k) => k }) }
    if (name === '@modern/components/ui/loading') return { useLoadingActivity: () => {} }
    if (name === '@modern/components/AppDraftGuard.vue') return guardCtx.exports
    if (name === './GroupEditorSurface.vue')
      return {
        __esModule: true,
        default: vue.defineComponent({
          props: ['title', 'description'],
          emits: ['close'],
          setup:
            (props, { slots }) =>
            () =>
              vue.h('surface-mock', [slots.default ? slots.default() : null]),
        }),
      }
    if (name === '@modern/components/ui')
      return {
        AppButton: vue.defineComponent({
          props: ['disabled', 'loading'],
          emits: ['click'],
          setup:
            (props, { emit, slots }) =>
            () =>
              vue.h(
                'button',
                { class: 'app-btn', onClick: () => emit('click') },
                slots.default ? slots.default() : null,
              ),
        }),
      }
    return {
      __esModule: true,
      default: vue.defineComponent({ inheritAttrs: false, setup: () => () => null }),
    }
  },
)

let panelClosedCount = 0
const workspaceHarness = mountComponent(
  vue.defineComponent({
    setup() {
      return () =>
        vue.h(workspaceCtx.exports.default, {
          title: 'Policy',
          description: 'Desc',
          dirty: panelCtx.__state.dirty.value,
          onClose: () => {
            panelClosedCount++
          },
        })
    },
  }),
)
await vue.nextTick()
const cancelBtn = pick(workspaceHarness.root, (n) => n.tag === 'button' && contains(n, 'ui.cancel'))
assert.ok(cancelBtn, 'workspace panel must render cancel button')

// 点击取消：dirty 时弹出 AppDraftGuard 确认对话框
cancelBtn.events.onClick()
await vue.nextTick()
assert.equal(confirmDialogState.props.open, true, 'draft guard dialog opened on cancel')

// 用户放弃关闭：保留草稿，不触发 onClose
confirmDialogState.emit('cancel')
await new Promise(setImmediate)
await vue.nextTick()
assert.equal(panelClosedCount, 0, 'canceling discard keeps panel open')

// 用户确认放弃：触发 onClose 关闭面板
cancelBtn.events.onClick()
await vue.nextTick()
confirmDialogState.emit('confirm')
await new Promise(setImmediate)
await vue.nextTick()
assert.equal(panelClosedCount, 1, 'confirming discard closes the panel')

// 重开获取最新 revision（rev 99），验证不自动覆盖，粘帖后以最新 revision 执行第二次 CAS
policyData.value = remoteNewerPolicy
const reopenedPanel = mountComponent(panelCtx.exports.default, {
  group: { id: 1, name: 'g' },
  credential: { id: 6, label: 'cred-reopened' },
})
await vue.nextTick()
assert.ok(
  panelCtx.__state.draft.value.includes('远端新规则'),
  'reopened panel loads remote revision without clobbering',
)

// 粘贴之前复制的正文，验证第二次 CAS 保存
panelCtx.__state.draft.value = copiedDraft
let secondCasRevision = ''
let secondCasText = ''
saveCredentialHandler = async (_client, _gid, _cid, expectedRevision, configText) => {
  secondCasRevision = expectedRevision
  secondCasText = configText
  return { ...remoteNewerPolicy, revisionText: '100' }
}
const reopenedWorkspace = pick(
  reopenedPanel.root,
  (node) => node.tag === 'ui-mock' && node.events?.onSave,
)
await reopenedWorkspace.events.onSave()
await vue.nextTick()
assert.equal(secondCasRevision, '99', 'second CAS uses the newest remote revision')
assert.ok(secondCasText.includes('"2.5"'), 'second CAS persists the user merged draft')
console.log(
  'PASS: 409 recovery retains draft/revision, invalidates query, confirms cancel via draft guard, and completes second CAS on reopen',
)

for (const [locale, groupDetail] of Object.entries({
  'zh-CN': zhCN,
  'en-US': enUS,
  'ja-JP': jaJP,
})) {
  const i18n = createI18n({ legacy: false, locale, messages: { [locale]: { groupDetail } } })
  panelTranslate = i18n.global.t
  policyData.value = blankPolicy
  const panel = mountComponent(panelCtx.exports.default, { group: { id: 1, name: 'g' } })
  panelCtx.__state.draft.value = ''
  panelCtx.__state.switchEditorMode('json')
  await vue.nextTick()
  assert.deepEqual(panel.errors, [], locale)
  const ghost = pick(panel.root, (node) => node.props.class === 'modern-policy-ghost-pre')
  assert.ok(ghost, locale)
  assert.deepEqual(JSON.parse(ghost.text), { schema_version: 1, rules: [] }, locale)
}
console.log('PASS: Blank JSON guidance renders with real i18n in all three locales')

const navigationRouter = createRouter({
  history: createMemoryHistory(),
  routes: [{ path: '/groups/:id', component: {} }],
})
await navigationRouter.push('/groups/1?credential=7&q=retained')
let navigationGuardCalls = 0
const removeNavigationGuard = navigationRouter.beforeEach(() => {
  navigationGuardCalls++
  return false
})
const credentialSource = readSource('web/src/frontends/modern/features/groups/GroupCredentials.vue')
const navigation = loadTsModule(extract(credentialSource, ['openCredentialPolicy']), {
  router: navigationRouter,
  route: navigationRouter.currentRoute.value,
})
const credentialTemplate = parseSfc(credentialSource).descriptor.template.ast
let policyHandler
function findPolicyHandler(node) {
  if (node.tag === 'CredentialDetailPanel')
    policyHandler = node.props.find((p) => p.name === 'on' && p.arg?.content === 'policy')?.exp
      .content
  for (const child of node.children ?? []) findPolicyHandler(child)
}
findPolicyHandler(credentialTemplate)
assert.ok(policyHandler)
const openPolicy = new Function('openCredentialPolicy', 'credentialView', policyHandler)
openPolicy(navigation.openCredentialPolicy, { id: 7, mode: 'details' })
await new Promise(setImmediate)
assert.equal(navigationGuardCalls, 1)
assert.equal(navigationRouter.currentRoute.value.query.credential_view, undefined)
removeNavigationGuard()
openPolicy(navigation.openCredentialPolicy, { id: 7, mode: 'details' })
await new Promise(setImmediate)
assert.equal(navigationRouter.currentRoute.value.query.credential_view, 'policy')
assert.equal(navigationRouter.currentRoute.value.query.q, 'retained')
console.log('PASS: Credential policy entry waits for route guards and preserves query state')

// Scoped API smoke: no metadata projection; identity validation, raw body, signal remain.
for (const cid of [undefined, 9]) {
  const raw = {
    scope: cid === undefined ? 'group' : 'credential',
    id: cid ?? 7,
    group_id: 7,
    credential_id: cid,
    revision_text: '18446744073709551615',
    config_text: compactDecimal,
  }
  const requests = []
  const signal = new AbortController().signal
  const client = {
    request: async (path, options) => {
      requests.push({ path, options })
      return raw
    },
  }
  assert.equal((await ctx.getPolicy(client, 7, signal, cid)).configText, compactDecimal)
  const saved = await ctx.savePolicy(client, 7, '18446744073709551615', compactDecimal, signal, cid)
  assert.equal(saved.revisionText, '18446744073709551615')
  const expectedPath =
    cid === undefined ? '/api/groups/7/policy' : '/api/groups/7/credentials/9/policy'
  assert.equal(requests[0].path, expectedPath)
  assert.equal(requests[1].path, expectedPath)
  assert.equal(requests[1].options.signal, signal)
  assert.equal(
    requests[1].options.body,
    `{"expected_revision":"18446744073709551615","config":${compactDecimal}}`,
  )
  for (const broken of [
    { scope: 'wrong' },
    { id: 8 },
    { group_id: 8 },
    ...(cid === undefined ? [] : [{ credential_id: 8 }]),
  ]) {
    assert.throws(() => ctx.readPolicy({ ...raw, ...broken }, 7, cid), ctx.InvalidResponseError)
  }
}
console.log(
  'PASS: scoped API preserves endpoint/uint64/raw body/signal and rejects target mismatches',
)
