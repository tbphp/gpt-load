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

// 绑定 reader 必须无损保留 canonical uint64 revision_text，并拒绝畸形/溢出输入。
const policyScopes = [
  ['readGroupPolicy', 'group'],
  ['readCredentialPolicy', 'credential'],
]
for (const [fn, scope] of policyScopes) {
  const credentialId = scope === 'credential' ? 1 : undefined
  for (const rev of ['0', '9007199254740993', '18446744073709551615']) {
    assert.equal(
      ctx.readPolicy(mockPolicy(scope, rev), 1, credentialId).revisionText,
      rev,
      `${fn} did not preserve revision_text ${rev}`,
    )
  }
  for (const bad of ['00', '-1', '+1', '1e9', ' 1 ', '18446744073709551616', 'notanumber']) {
    assert.throws(
      () => ctx.readPolicy(mockPolicy(scope, bad), 1, credentialId),
      ctx.InvalidResponseError,
      `${fn} accepted malformed revision_text ${bad}`,
    )
  }
}
console.log('PASS: Full uint64 revision_text parsed correctly; malformed/overflowing rejected')

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
assert.equal(discoveryPath, '/api/policy/discovery?group_id=7&credential_id=9')
assert.deepEqual(
  Array.from(scopedExt.quota_windows),
  [604800, 18000],
  'scoped discovery dropped quota_windows',
)
console.log('PASS: Scoped discovery query carries group/credential and real quota windows')

// Verify configText retains exact decimal formatting for both scopes
const decimalRaw = {
  scope: 'group',
  id: 1,
  group_id: 1,
  credential_id: 1,
  revision_text: '1',
  config_text:
    '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},\"actions\":[{"type":"exclude_candidate"}]}]}',
}
for (const scope of ['group', 'credential']) {
  const res = ctx.readPolicy({ ...decimalRaw, scope }, 1, scope === 'credential' ? 1 : undefined)
  assert.ok(
    res.configText.includes('0.10000000000000001'),
    `${scope} reader dropped the exact decimal literal in configText`,
  )
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
const queryCache = {
  cancelQueries: async () => {},
  setQueryData: () => {},
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
  savePolicy: (client, gid, text, signal, cid) =>
    cid === undefined ? blankPolicy : saveCredentialHandler(client, gid, cid, text, signal),
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
      'globalThis.__state={editorMode, draft, dirty, saving, serverError, switchEditorMode}; return (_ctx: any,_cache: any) =>',
    ),
  extra: { AbortController, console },
})
const { root, errors } = mountComponent(panelCtx.exports.default, { group: { id: 1, name: 'g' } })
assert.deepEqual(errors, [], 'panel render produced errors')
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
// 按可见标签定位真实的 ui-mock 点击节点；每次调用都从当前 root 重新查询，不复用失效节点。
const button = (node, label) =>
  pick(node, (n) => n.tag === 'ui-mock' && n.events?.onClick && contains(n, label))
// 模式选择已换成 AppSegmentedField：ui-mock 记录真实 options/model-value，切换走 onUpdate:modelValue。
const modeField = (node) =>
  pick(
    node,
    (n) => Array.isArray(n.props?.options) && n.props.options.some((o) => o.value === 'json'),
  )
const switchMode = (node, value) => modeField(node).events['onUpdate:modelValue'](value)
// 真实调用方 1：policy 面板把自己的 bodyState/失败文案交给共享 frame，不自行渲染 load 骨架。
const policyFrame = pick(root, (node) => 'body-state' in node.props).props
assert.deepEqual(
  [policyFrame['body-state'], policyFrame['unavailable-label']],
  ['ready', 'groupDetail.policy.loadFailed'],
  'policy caller must hand its bodyState/unavailable label to the frame',
)
// 分组 scope 直接渲染编辑器（无 credential 继承/覆盖分支），仅验证模式 tab 与隐藏 discovery。
const editorModeField = modeField(root)
assert.ok(editorModeField, 'panel did not render the editor mode field')
assert.equal(editorModeField.props['model-value'], 'visual', 'panel did not default to visual mode')
assert.equal(
  editorModeField.props.options.map((option) => `${option.value}:${option.label}`).join(','),
  'visual:groupDetail.policy.modeVisual,json:groupDetail.policy.modeJson',
  'editor mode options lost their i18n labels',
)
// 唯一的隐藏 scoped discovery query；面板自身不渲染 reference UI。
assert.equal(discoveryCalls.length, 1, 'panel must issue exactly one hidden scoped discovery query')
assert.ok(discoveryCalls[0].includes('policy-discovery'), 'discovery query is not scoped')
assert.equal(discoveryCalls[0][2], 1, 'discovery query is not scoped to the group id')
const editorNode = pick(root, (node) => Array.isArray(node.props?.['quota-windows']))
assert.ok(
  editorNode && editorNode.props['quota-windows'].join(',') === '18000,604800',
  'panel did not pass the discovered quota windows to the editor',
)
console.log('PASS: Panel issues one hidden scoped discovery query without reference UI')

// JSON 模式切换必须格式化当前正文，同时保留原始数值精度。
const compactDecimal =
  '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},\"actions\":[{"type":"exclude_candidate"}]}]}'
panelCtx.__state.draft.value = compactDecimal
switchMode(root, 'json')
await vue.nextTick()
assert.equal(
  panelCtx.__state.editorMode.value,
  'json',
  'mode click did not switch the editor to JSON',
)
const formatted = panelCtx.__state.draft.value
assert.ok(formatted.includes('\n'), 'JSON mode watch did not pretty-print the draft')
// 普通 quota 阈值按 float 语义保留：0.10000000000000001 与 0.1 相同。
assert.equal(
  JSON.parse(formatted).rules[0].when.value,
  0.1,
  'JSON mode watch did not preserve the quota threshold value',
)

// HTTP 回退：compiled 面板复制按钮直接复制当前显示的 pretty 源文本（不做业务校验）。
const copyButton = button(root, 'groupDetail.policy.copyJson')
assert.ok(copyButton, 'panel did not render the copy JSON button')
copyButton.events.onClick()
await vue.nextTick()
assert.equal(copied.length, 1, 'copy button did not run the clipboard fallback')
assert.equal(copied[0], formatted, 'copy button did not copy the displayed pretty source text')
assert.ok(
  !contains(root, 'groupDetail.policy.clipboardUnavailable'),
  'copy reported the legacy clipboardUnavailable error on success',
)
console.log('PASS: Panel copy uses the real clipboard fallback on HTTP')
console.log('PASS: Panel mode switch and blank JSON placeholder verified on compiled SFC')

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
assert.deepEqual(credPanel.errors, [], 'credential panel render produced errors')
const credRoot = credPanel.root
const credentialModeField = pick(credRoot, (node) =>
  Array.isArray(node.props?.options)
    ? node.props.options.some((option) => option.value === 'override')
    : false,
)
assert.ok(credentialModeField, 'credential panel did not render the group policy mode field')
assert.equal(
  credentialModeField.props.modelValue,
  'override',
  'credential panel did not select override mode',
)
switchMode(credRoot, 'json')
await vue.nextTick()
const area = pick(credRoot, (node) => node.tag === 'ui-mock' && node.events?.onPaste)
const pasted =
  '{"schema_version":1,"rules":[{"id":"g","name":"模型","domain":"scheduling","enabled":true,' +
  '"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},' +
  '"reduce":"min","op":"lt","value":0.10000000000000001},\"actions\":[{"type":"exclude_candidate"}]},' +
  '{"id":"h","name":"H","domain":"scheduling","enabled":true,' +
  '"when":{"fact":"request.model","op":"eq","value":9007199254740993},\"actions\":[{"type":"exclude_candidate"}]}]}'
const draft = panelCtx.__state.draft.value
area.events.onPaste({
  clipboardData: { getData: () => pasted },
  target: { selectionStart: 0, selectionEnd: draft.length },
  preventDefault() {},
})
await vue.nextTick()
const pastedText = panelCtx.__state.draft.value
for (const token of ['"group_policy": "override"', '模型']) {
  assert.ok(pastedText.includes(token), `credential paste lost ${token}`)
}
// 原生 JSON.parse 会把数值按 double 语义读回：超长小数与超出安全整数的值不再逐字保留。
const pastedRules = JSON.parse(pastedText).rules
assert.equal(pastedRules[0].when.value, 0.1, 'credential paste must keep numeric value semantics')
assert.equal(
  pastedRules[1].when.value,
  9007199254740992,
  'credential paste re-parses numbers natively',
)
console.log('PASS: Credential override paste keeps mode intent and native JSON numbers')

// 2c. Credential 继承模式：展示只读分组策略（若有规则）同时始终提供自身规则编辑器
groupPolicyData.value = ctx.readPolicy(
  {
    ...mockPolicy('group', '1'),
    config_text:
      '{"schema_version":1,"rules":[{"id":"g-sched","name":"分组调度规则","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"m"},\"actions\":[{"type":"exclude_candidate"}]}]}',
  },
  1,
)
policyData.value = ctx.readPolicy(
  {
    ...mockPolicy('credential', '4'),
    config_text:
      '{"schema_version":1,"group_policy":"inherit","rules":[{"id":"c-price","name":"账号计价规则","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"p"},\"actions\":[{"type":"multiply_price","factor":"1.5"}]}]}',
  },
  1,
  1,
)
const inheritPanel = mountComponent(panelCtx.exports.default, {
  group: { id: 1, name: 'g' },
  credential: { id: 6, label: 'cred-inherit' },
})
assert.deepEqual(inheritPanel.errors, [], 'credential inherit panel produced errors')
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
assert.equal(
  editors.length,
  2,
  'credential inherit mode must render 1 readonly group editor and 1 own editor',
)
const readonlyEditor = editors.find((e) => e.props.disabled === true || e.props.disabled === '')
const editableEditor = editors.find((e) => e.props.disabled === false)
assert.ok(
  readonlyEditor && editableEditor,
  'could not identify readonly group editor and editable own editor',
)
const readonlyProp = readonlyEditor.props.modelValue ?? readonlyEditor.props['model-value']
assert.ok(
  readonlyProp?.includes('分组调度规则'),
  `inherited group policy editor did not receive distinct group fixture, got: ${readonlyProp}`,
)
assert.ok(
  panelCtx.__state.draft.value?.includes('账号计价规则'),
  'editable credential policy editor did not receive distinct credential fixture',
)
console.log('PASS: Credential inherit renders readonly group editor and editable own editor')

// 2d. 子组件 invalid draft 阻止切换为 JSON 模式，防止旧值保存丢草稿
editableEditor.events['onDraftStatus']?.({ valid: false, pending: false })
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
switchMode(inheritRoot, 'json')
await vue.nextTick()
assert.notEqual(
  panelCtx.__state.editorMode.value,
  'json',
  'panel allowed switching to JSON while child draft is invalid',
)
assert.ok(
  contains(inheritRoot, 'groupDetail.policy.invalidDraftBlockJson'),
  'panel did not display invalidDraftBlockJson notice',
)
// pending 子草稿同样不允许切换：守卫在 switchEditorMode 内，与 invalid 一致。
editableEditor.events['onDraftStatus']?.({ valid: true, pending: true })
await vue.nextTick()
switchMode(inheritRoot, 'json')
await vue.nextTick()
assert.notEqual(
  panelCtx.__state.editorMode.value,
  'json',
  'panel allowed switching to JSON while child draft is pending',
)
// 修复草稿后允许切换为 JSON 模式
editableEditor.events['onDraftStatus']?.({ valid: true, pending: false })
await vue.nextTick()
switchMode(inheritRoot, 'json')
await vue.nextTick()
assert.equal(
  panelCtx.__state.editorMode.value,
  'json',
  'panel did not allow switching to JSON after child draft was resolved',
)
console.log('PASS: Child invalid draft blocks switching to JSON mode')

// 2e. 保存提交回归：网络卡顿下重复点击/双击被短路，失败保留草稿，成功提交最新 config_json。
panelCtx.__state.draft.value = panelCtx.__state.draft.value.replace('"1.5"', '"2.5"')
const saveNode = () => pick(inheritRoot, (node) => node.tag === 'ui-mock' && node.events?.onSave)
assert.ok(saveNode(), 'could not find workspace panel to trigger save')

let saveCalls = 0
let resolveSave
saveCredentialHandler = (_client, _gid, _cid, text) => {
  saveCalls++
  return new Promise((resolve) => {
    resolveSave = () => resolve({ revisionText: '5', configText: text })
  })
}

// 首次点击发起请求；请求在途时的第二次点击（双击）必须被短路。
const firstSave = saveNode().events.onSave()
await new Promise(setImmediate)
const duplicateSave = saveNode().events.onSave()
assert.equal(saveCalls, 1, 'duplicate save clicks must not issue a second request')
assert.equal(panelCtx.__state.saving.value, true, 'panel must report saving while in flight')
resolveSave()
await Promise.all([firstSave, duplicateSave])
await vue.nextTick()
assert.equal(panelCtx.__state.dirty.value, false, 'successful save must clear the dirty state')
assert.equal(panelCtx.__state.serverError.value, '', 'successful save must clear the error notice')

// 失败路径：网络错误保留草稿、保持 dirty、显示错误且不关闭面板。
panelCtx.__state.draft.value = panelCtx.__state.draft.value.replace('"2.5"', '"3.5"')
saveCredentialHandler = async () => {
  throw new MockApiError(0, 'network down')
}
await saveNode().events.onSave()
await vue.nextTick()
assert.equal(panelCtx.__state.serverError.value, 'network down')
assert.ok(panelCtx.__state.draft.value.includes('"3.5"'), 'failed save must retain the user draft')
assert.equal(panelCtx.__state.dirty.value, true, 'panel must remain dirty after a failed save')

// 复制功能：只能复制当前已格式化的合法正文（未提交/非法的局部草稿已被阻止入正文）
const copyButtonInherit = button(inheritRoot, 'groupDetail.policy.copyJson')
assert.ok(copyButtonInherit, 'could not find copyJson button in inherit panel')
copyButtonInherit.events.onClick()
await vue.nextTick()
const copiedDraft = copied[copied.length - 1]
assert.ok(copiedDraft.includes('"3.5"'), 'copyJson copies the current draft text')

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
        // 转发 disabled/type，使 frame 默认动作门禁在自定义宿主上可断言。
        AppCollectionState: uiStub,
        AppButton: vue.defineComponent({
          props: ['disabled', 'loading', 'type'],
          emits: ['click'],
          setup:
            (props, { emit, slots }) =>
            () =>
              vue.h(
                'button',
                {
                  class: 'app-btn',
                  type: props.type,
                  disabled: props.disabled,
                  onClick: () => emit('click'),
                },
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

console.log(
  'PASS: save short-circuits duplicate clicks, keeps the draft on failure, and commits the latest config_json on success',
)

for (const [locale, groupDetail] of Object.entries({
  'zh-CN': zhCN,
  'en-US': enUS,
  'ja-JP': jaJP,
})) {
  const i18n = createI18n({ legacy: false, locale, messages: { [locale]: { groupDetail } } })
  panelTranslate = i18n.global.t
  // 无 config_text 的空策略（group scope 主查询走 groupPolicyData）：空草稿显示 placeholder，且不 dirty、不回写 schema。
  groupPolicyData.value = ctx.readPolicy({ ...mockPolicy('group', '0'), config_text: '' }, 1)
  const panel = mountComponent(panelCtx.exports.default, { group: { id: 1, name: 'g' } })
  panelCtx.__state.switchEditorMode('json')
  await vue.nextTick()
  assert.deepEqual(panel.errors, [], locale)
  assert.equal(panelCtx.__state.draft.value, '', `${locale} blank draft must stay empty`)
  assert.equal(panelCtx.__state.dirty.value, false, `${locale} blank draft must not be dirty`)
  const rawArea = pick(
    panel.root,
    (node) => node.tag === 'ui-mock' && typeof node.props.placeholder === 'string',
  )
  assert.ok(rawArea, `${locale} blank JSON guidance missing`)
  assert.deepEqual(
    JSON.parse(rawArea.props.placeholder),
    { schema_version: 1, rules: [] },
    `${locale} blank JSON placeholder changed`,
  )
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
  const saved = await ctx.savePolicy(client, 7, compactDecimal, signal, cid)
  assert.equal(saved.revisionText, '18446744073709551615')
  const expectedPath =
    cid === undefined ? '/api/groups/7/policy' : '/api/groups/7/credentials/9/policy'
  assert.equal(requests[0].path, expectedPath)
  assert.equal(requests[1].path, expectedPath)
  assert.equal(requests[1].options.signal, signal)
  assert.equal(requests[1].options.body, `{"config":${compactDecimal}}`)
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
