// 真实 SFC 编译到自定义渲染器的交互回归。单一 harness、共享 fixture、flat 条件与行内卡片用例。
// 运行：node --test web/src/frontends/modern/features/groups/policy-editor/policy-editor.runtime.test.mjs
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import test from 'node:test'

import { compileSfc, mountComponent, Vue } from '../../../../../../scripts/policy-test-utils.mjs'
import { enUS, jaJP, zhCN } from '../../../i18n/locales/policy-editor.ts'
import * as draftModule from './use-policy-draft.ts'
import * as model from './policy-model.ts'

const nodeRequire = createRequire(import.meta.url)
const dir = import.meta.dirname

// ---- 极简 i18n：t() 读取 locale ref，渲染即随语言切换重跑。 ----
const locale = Vue.ref('zh-CN')
const messages = {
  'zh-CN': { policyEditor: zhCN },
  'en-US': { policyEditor: enUS },
  'ja-JP': { policyEditor: jaJP },
}
function t(key, params) {
  let node = messages[locale.value]
  for (const part of key.split('.'))
    node = node && typeof node === 'object' ? node[part] : undefined
  if (typeof node !== 'string') return key
  if (params)
    for (const [name, value] of Object.entries(params))
      node = node.split(`{${name}}`).join(String(value))
  return node
}

// ---- UI/图标替身：把 attrs（label / modelValue / onUpdate / onClick / onChange）透传给宿主节点。 ----
const passthrough = Vue.defineComponent({
  inheritAttrs: false,
  setup:
    (_, { attrs, slots }) =>
    () =>
      Vue.h('ui-mock', attrs, slots.default?.() ?? []),
})
const passthroughProxy = new Proxy({}, { get: () => passthrough })

// ---- SFC 编译：按依赖顺序求值，相对导入命中已注册组件。 ----
const registry = {}
function mockRequire(name) {
  if (name === 'vue') return Vue
  if (name === '@modern/components/ui') return passthroughProxy
  if (name === '@lucide/vue') return passthroughProxy
  if (name === './use-policy-messages') return { usePolicyMessages: () => ({ t }) }
  if (name === './use-policy-draft') return draftModule
  if (name === './policy-model') return model
  if (name.startsWith('./') && registry[name.slice(2)]) return registry[name.slice(2)]
  if (name.startsWith('./')) throw new Error(`unexpected import: ${name}`)
  return nodeRequire(name)
}
for (const name of [
  'PolicyActionEditor.vue',
  'PolicyFactLeaf.vue',
  'PolicyTimeWindowLeaf.vue',
  'PolicyConditionEditor.vue',
  'PolicyRuleCard.vue',
  'PolicyEditor.vue',
]) {
  const component = compileSfc(name, readFileSync(path.join(dir, name), 'utf8'), mockRequire)
    .exports.default
  registry[name] = component
}

// ---- 共享自定义渲染器 harness；文本/查找辅助留给本文件。 ----
function walk(node, fn) {
  fn(node)
  for (const child of node.children ?? []) walk(child, fn)
}
const texts = (node) => {
  const out = []
  walk(node, (item) => item.text && out.push(item.text))
  return out.join(' ')
}
const findAll = (node, predicate) => {
  const out = []
  walk(node, (item) => predicate(item) && out.push(item))
  return out
}
const find = (node, predicate) => findAll(node, predicate)[0]
function isVisible(node) {
  let cur = node
  while (cur) {
    if (cur.style?.display === 'none') return false
    cur = cur.parent
  }
  return true
}
const uiNode = (node, label) =>
  find(node, (n) => n.tag === 'ui-mock' && n.props.label === label && isVisible(n))
const optionSelect = (node, label) =>
  find(
    node,
    (n) => n.tag === 'ui-mock' && n.props.label === label && Array.isArray(n.props.options),
  )
const click = (node) => node.events.onClick?.()
const setValue = (node, value) => node.events['onUpdate:modelValue']?.(value)
const fire = (node, event, ...args) => node.events[event]?.(...args)

// 持有子组件 modelValue 的宿主，用来验证 emit/草稿。
function stateful(component, initial, extra = {}) {
  const state = Vue.ref(initial)
  const wrapper = Vue.defineComponent({
    setup: () => () =>
      Vue.h(component, {
        ...extra,
        modelValue: state.value,
        'onUpdate:modelValue': (value) => (state.value = value),
      }),
  })
  return { state, wrapper }
}

// ---- 共享 fixture。 ----
const emptyConfig = '{"schema_version":1,"rules":[]}'
const deepJson = `${'['.repeat(200)}1${']'.repeat(200)}`
const twoPricing = JSON.stringify({
  schema_version: 1,
  rules: [
    {
      id: 'first',
      name: 'first',
      domain: 'pricing',
      enabled: true,
      when: { fact: 'request.model', op: 'eq', value: 'a' },
      then: { type: 'multiply_price', factor: '2' },
    },
    {
      id: 'second',
      name: 'second',
      domain: 'pricing',
      enabled: true,
      when: { fact: 'request.model', op: 'eq', value: 'b' },
      then: { type: 'multiply_price', factor: '3' },
    },
  ],
})
const malformedLeaf = {
  type: 'object',
  entries: [
    { key: 'fact', value: { type: 'literal', literal: 'string', raw: '"request.model"' } },
    { key: 'op', value: { type: 'literal', literal: 'string', raw: '"in"' } },
    {
      key: 'value',
      value: {
        type: 'array',
        items: [
          { type: 'literal', literal: 'string', raw: '"a"' },
          { type: 'literal', literal: 'number', raw: '123' },
          {
            type: 'object',
            entries: [{ key: 'unknown', value: { type: 'literal', literal: 'number', raw: '1' } }],
          },
        ],
      },
    },
  ],
}
const quotaRaw = model.objectNode([
  { key: 'fact', value: model.stringLiteral('credential.quota.remaining_ratio') },
  {
    key: 'select',
    value: model.objectNode([
      { key: 'scope', value: model.stringLiteral('account') },
      { key: 'window_seconds', value: model.numberLiteral('18000') },
    ]),
  },
  { key: 'reduce', value: model.stringLiteral('min') },
  { key: 'op', value: model.stringLiteral('lt') },
  { key: 'value', value: model.numberLiteral('0.10000000000000001') },
])
const firstRule = () => model.readVisualDocument(twoPricing).rules[0]

// ---- 用例表：全部复用同一个 harness。 ----
const cases = [
  [
    'blank input opens an empty editor without a fatal error',
    () => {
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: '' })
      assert.ok(texts(root).includes(zhCN.addRule))
      assert.ok(!texts(root).includes(zhCN.errors.empty))
    },
  ],

  [
    'locale switches reactively across all three languages',
    async () => {
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: emptyConfig })
      for (const code of ['zh-CN', 'en-US', 'ja-JP']) {
        locale.value = code
        await Vue.nextTick()
        assert.ok(texts(root).includes(messages[code].policyEditor.addRule))
      }
      locale.value = 'zh-CN'
    },
  ],

  [
    'deeply nested JSON is rejected without overflowing the stack',
    () => {
      const { root, errors } = mountComponent(registry['PolicyEditor.vue'], {
        modelValue: deepJson,
      })
      assert.equal(errors.length, 0)
      assert.ok(texts(root).includes(zhCN.errors.syntax))
    },
  ],

  [
    'unsupported top-level structure stays JSON-only and keeps its raw payload',
    () => {
      const { root } = mountComponent(registry['PolicyConditionEditor.vue'], {
        modelValue: malformedLeaf,
      })
      assert.ok(texts(root).includes(zhCN.condition.unsupported))
      assert.ok(texts(root).includes('"unknown"'))
      assert.equal(uiNode(root, zhCN.fact.value), undefined)
    },
  ],

  [
    'standalone conditions accept additions without rewriting on mount',
    async () => {
      for (const initial of [quotaRaw, model.newTimeWindowCondition()]) {
        const { state, wrapper } = stateful(registry['PolicyConditionEditor.vue'], initial)
        const { root, errors } = mountComponent(wrapper)
        assert.equal(model.serializeJson(state.value), model.serializeJson(initial))
        const menu = find(root, (n) => n.tag === 'ui-mock' && Array.isArray(n.props.items))
        fire(menu, 'onSelect', 'param')
        await Vue.nextTick()
        const children = model.arrayItems(model.getField(state.value, 'all'))
        assert.equal(children.length, 2)
        assert.equal(model.serializeJson(children[0]), model.serializeJson(initial))
        assert.ok(uiNode(root, zhCN.condition.remove))
        assert.equal(errors.length, 0)
      }
    },
  ],

  [
    'flat condition flow adds through the menu, reorders, switches group, and removes',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', []),
      )
      const { root } = mountComponent(wrapper)
      const kinds = (kind) =>
        Array.from(model.arrayItems(model.getField(state.value, kind)) ?? [], model.conditionKind)
      const menu = () => find(root, (n) => n.tag === 'ui-mock' && Array.isArray(n.props.items))
      assert.equal(
        menu()
          .props.items.map((item) => item.id)
          .join(','),
        'param,time_window',
      )
      assert.ok(menu())

      fire(menu(), 'onSelect', 'param')
      await Vue.nextTick()
      fire(menu(), 'onSelect', 'time_window')
      await Vue.nextTick()
      assert.equal(kinds('all').join(','), 'param,time_window')
      assert.ok(texts(root).includes(zhCN.condition.itemParam))
      assert.ok(texts(root).includes(zhCN.condition.itemTimeWindow))

      const up = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.condition.moveUp,
      )
      click(up[1])
      await Vue.nextTick()
      assert.equal(kinds('all').join(','), 'time_window,param')

      setValue(optionSelect(root, zhCN.condition.title), 'any')
      await Vue.nextTick()
      assert.equal(kinds('any').join(','), 'time_window,param')

      click(findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.condition.remove)[0])
      await Vue.nextTick()
      assert.equal(kinds('any').join(','), 'param')
    },
  ],

  [
    'a nested imported group stays JSON-only while siblings remain editable',
    async () => {
      const nested = model.newConditionGroup('any', [model.newFactCondition('upstream.model')])
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', [model.newFactCondition('request.model'), nested]),
      )
      const { root } = mountComponent(wrapper)
      assert.ok(texts(root).includes('"any"'))
      const value = uiNode(root, zhCN.fact.value)
      assert.ok(value)
      setValue(value, 'gpt-4o')
      await Vue.nextTick()
      const left = model.arrayItems(model.getField(state.value, 'all')) ?? []
      assert.equal(left.length, 2)
      assert.equal(model.serializeJson(left[1]), model.serializeJson(nested))
    },
  ],

  [
    'string fact exposes eq only and never the set operator',
    () => {
      const { root } = mountComponent(registry['PolicyFactLeaf.vue'], {
        modelValue: model.newFactCondition('request.model'),
      })
      const op = optionSelect(root, zhCN.fact.operator)
      assert.deepEqual(
        op.props.options.map((option) => option.value),
        ['eq'],
      )
    },
  ],

  [
    'switching a flat leaf to a quota window keeps operator, threshold and canonical fact',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', [model.newFactCondition('request.model')]),
        { quotaWindows: [18000, 604800] },
      )
      const { root } = mountComponent(wrapper)
      const fact = optionSelect(root, zhCN.fact.key)
      assert.equal(
        fact.props.options.map((option) => option.value).join(','),
        'request.model,upstream.model,quota:18000,quota:604800',
      )
      setValue(fact, 'quota:18000')
      await Vue.nextTick()
      assert.ok(!texts(root).includes(zhCN.condition.unsupported))
      let leaf = (model.arrayItems(model.getField(state.value, 'all')) ?? [])[0]
      let select = model.getField(leaf, 'select')
      assert.equal(
        model.literalString(model.getField(leaf, 'fact')),
        'credential.quota.remaining_ratio',
      )
      assert.equal(model.literalString(model.getField(select, 'scope')), 'account')
      assert.equal(model.literalNumberRaw(model.getField(select, 'window_seconds')), '18000')
      assert.equal(model.literalString(model.getField(leaf, 'reduce')), 'min')
      const op = optionSelect(root, zhCN.fact.operator)
      setValue(op, 'gt')
      await Vue.nextTick()
      const threshold = uiNode(root, zhCN.fact.value)
      setValue(threshold, '0.25')
      fire(threshold, 'onChange')
      await Vue.nextTick()
      // 同一额度参数换窗口：只改 window_seconds，保留 op/threshold。
      setValue(optionSelect(root, zhCN.fact.key), 'quota:604800')
      await Vue.nextTick()
      leaf = (model.arrayItems(model.getField(state.value, 'all')) ?? [])[0]
      select = model.getField(leaf, 'select')
      assert.equal(model.literalNumberRaw(model.getField(select, 'window_seconds')), '604800')
      assert.equal(model.literalString(model.getField(leaf, 'op')), 'gt')
      assert.equal(model.literalNumberRaw(model.getField(leaf, 'value')), '0.25')
    },
  ],

  [
    'quota options follow the seeded catalog and save canonical windows',
    async () => {
      const scenarios = [
        { windows: [18000], expected: ['quota:18000'] },
        { windows: [604800], expected: ['quota:604800'] },
        { windows: [604800, 18000], expected: ['quota:18000', 'quota:604800'] },
        { windows: [3600, 90000], expected: ['quota:3600', 'quota:90000'] },
      ]
      for (const scenario of scenarios) {
        const { state, wrapper } = stateful(
          registry['PolicyFactLeaf.vue'],
          model.newFactCondition('request.model'),
          { quotaWindows: scenario.windows },
        )
        const { root } = mountComponent(wrapper)
        const fact = optionSelect(root, zhCN.fact.key)
        const quotas = fact.props.options.filter((option) => option.value.startsWith('quota:'))
        assert.equal(
          quotas.map((option) => option.value).join(','),
          scenario.expected.join(','),
          JSON.stringify(scenario.windows),
        )
        for (const option of quotas) {
          assert.ok(option.label.includes(zhCN.fact.facts.quotaRemainingRatio))
        }
        setValue(fact, scenario.expected[0])
        await Vue.nextTick()
        assert.equal(
          model.literalString(model.getField(state.value, 'fact')),
          'credential.quota.remaining_ratio',
        )
        assert.equal(
          model.literalNumberRaw(
            model.getField(model.getField(state.value, 'select'), 'window_seconds'),
          ),
          scenario.expected[0].slice('quota:'.length),
        )
      }
    },
  ],

  [
    'quota selector omits fake windows without a catalog and retains unobserved ones',
    async () => {
      const plain = mountComponent(registry['PolicyFactLeaf.vue'], {
        modelValue: model.newFactCondition('request.model'),
      })
      assert.equal(
        optionSelect(plain.root, zhCN.fact.key)
          .props.options.map((option) => option.value)
          .join(','),
        'request.model,upstream.model',
      )
      const { state, wrapper } = stateful(registry['PolicyFactLeaf.vue'], quotaRaw, {
        quotaWindows: [604800],
      })
      const { root } = mountComponent(wrapper)
      assert.equal(
        optionSelect(root, zhCN.fact.key)
          .props.options.map((option) => option.value)
          .join(','),
        'request.model,upstream.model,quota:604800,quota:18000',
      )
      assert.ok(texts(root).includes(zhCN.fact.descriptions.quotaRetained))
      assert.equal(
        model.literalNumberRaw(
          model.getField(model.getField(state.value, 'select'), 'window_seconds'),
        ),
        '18000',
      )
    },
  ],

  [
    'the quota catalog threads from the editor down to the leaf',
    () => {
      const raw = JSON.stringify({
        schema_version: 1,
        rules: [
          {
            id: 'q',
            name: 'q',
            domain: 'scheduling',
            enabled: true,
            when: { fact: 'request.model', op: 'eq', value: 'a' },
            then: { type: 'exclude_candidate' },
          },
        ],
      })
      const { root } = mountComponent(registry['PolicyEditor.vue'], {
        modelValue: raw,
        quotaWindows: [18000],
      })
      assert.equal(
        optionSelect(root, zhCN.fact.key)
          .props.options.map((option) => option.value)
          .join(','),
        'request.model,upstream.model,quota:18000',
      )
    },
  ],

  [
    'a pending time-window draft survives weekday edits, appends and removal of other ranges',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyTimeWindowLeaf.vue'],
        model.newTimeWindowCondition(),
      )
      const { root } = mountComponent(wrapper)
      setValue(uiNode(root, zhCN.timeWindow.from), 'garbage')
      await Vue.nextTick()
      setValue(uiNode(root, zhCN.timeWindow.weekday['2']), true)
      await Vue.nextTick()
      assert.match(model.serializeJson(state.value), /"weekdays":\[1,2\]/)
      assert.equal(uiNode(root, zhCN.timeWindow.from).props.modelValue, 'garbage')
      click(find(root, (n) => n.tag === 'ui-mock' && texts(n).includes(zhCN.timeWindow.addRange)))
      await Vue.nextTick()
      const fields = () =>
        findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.from)
      assert.equal(fields()[0].props.modelValue, 'garbage')
      setValue(fields()[1], 'pending-second')
      await Vue.nextTick()
      click(uiNode(root, zhCN.timeWindow.removeRange))
      await Vue.nextTick()
      assert.equal(fields().length, 1)
      assert.equal(fields()[0].props.modelValue, 'pending-second')
    },
  ],

  [
    'a pending time-window draft follows its block across a reorder',
    async () => {
      const ranges = model.arrayNode([
        model.arrayNode([model.stringLiteral('09:00'), model.stringLiteral('18:00')]),
      ])
      const twin = (weekday) =>
        model.objectNode([
          { key: 'predicate', value: model.stringLiteral('time_window') },
          { key: 'weekdays', value: model.arrayNode([model.numberLiteral(weekday)]) },
          { key: 'ranges', value: ranges },
        ])
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', [twin('1'), twin('2')]),
      )
      const { root } = mountComponent(wrapper)
      const fields = () =>
        findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.from)
      setValue(fields()[0], 'garbage')
      await Vue.nextTick()
      click(
        findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.condition.moveDown)[0],
      )
      await Vue.nextTick()
      const after = fields()
      assert.equal(after[0].props.modelValue, '09:00')
      assert.equal(after[1].props.modelValue, 'garbage')
      const saved = Array.from(model.arrayItems(model.getField(state.value, 'all')) ?? [])
      const weekday = (node) =>
        model.literalNumberRaw(model.arrayItems(model.getField(node, 'weekdays'))[0])
      assert.equal(weekday(saved[0]), '2')
      assert.equal(weekday(saved[1]), '1')
    },
  ],

  [
    'an enabled rule card removes directly with no confirmation dialog',
    async () => {
      let removed = 0
      const { root } = mountComponent(registry['PolicyRuleCard.vue'], {
        rule: firstRule(),
        index: 0,
        total: 1,
        onRemove: () => removed++,
      })
      click(uiNode(root, zhCN.rule.remove))
      await Vue.nextTick()
      assert.equal(removed, 1)
    },
  ],

  [
    'a disabled rule card never emits remove',
    async () => {
      let removed = 0
      const { root } = mountComponent(registry['PolicyRuleCard.vue'], {
        rule: firstRule(),
        index: 0,
        total: 1,
        disabled: true,
        onRemove: () => removed++,
      })
      click(uiNode(root, zhCN.rule.remove))
      await Vue.nextTick()
      assert.equal(removed, 0)
    },
  ],

  [
    'local action draft follows its rule across a reorder (stable keys)',
    async () => {
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: twoPricing })
      const factors = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor,
      )
      assert.equal(factors.length, 2)
      setValue(factors[0], 'not-a-number')
      await Vue.nextTick()
      click(findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.moveDown)[0])
      await Vue.nextTick()
      const drafts = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor,
      ).map((n) => n.props.modelValue)
      assert.equal(drafts.length, 2)
      assert.equal(drafts[0], '3')
      assert.equal(drafts[1], 'not-a-number')
    },
  ],

  [
    'duplicate resolves the original rule when a non-object precedes it',
    async () => {
      const raw = JSON.stringify({
        schema_version: 1,
        rules: [
          123,
          {
            id: 'good',
            name: 'good',
            domain: 'scheduling',
            enabled: true,
            when: { fact: 'request.model', op: 'eq', value: 'x' },
            then: { type: 'exclude_candidate' },
          },
        ],
      })
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], raw)
      const { root } = mountComponent(wrapper)
      click(uiNode(root, zhCN.rule.duplicate))
      await Vue.nextTick()
      const result = JSON.parse(state.value)
      assert.equal(result.rules.length, 3)
      assert.equal(result.rules[0], 123)
      assert.equal(result.rules[2].enabled, false)
      assert.equal(result.rules[2].id, 'rule-1')
    },
  ],

  [
    'name and enabled edits emit an updated rule node while the id stays stable',
    () => {
      let updated
      const { root } = mountComponent(registry['PolicyRuleCard.vue'], {
        rule: firstRule(),
        index: 0,
        total: 1,
        onUpdate: (node) => (updated = node),
      })
      setValue(uiNode(root, zhCN.rule.name), 'renamed')
      assert.equal(model.literalString(model.getField(updated, 'name')), 'renamed')
      assert.equal(model.literalString(model.getField(updated, 'id')), 'first')
      setValue(uiNode(root, zhCN.rule.enabled), false)
      assert.equal(model.literalBoolean(model.getField(updated, 'enabled')), false)
    },
  ],

  [
    'duplicate inserts a disabled copy with a fresh id',
    async () => {
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], twoPricing)
      const { root } = mountComponent(wrapper)
      click(uiNode(root, zhCN.rule.duplicate))
      await Vue.nextTick()
      const result = model.readVisualDocument(state.value)
      assert.equal(result.rules.length, 3)
      const copy = result.rules.find(
        (rule) => model.literalString(model.getField(rule.node, 'id')) === 'rule-1',
      )
      assert.equal(model.literalBoolean(model.getField(copy?.node, 'enabled')), false)
      assert.equal(model.literalString(model.getField(copy?.node, 'name')), 'first')
    },
  ],

  [
    'rule commits preserve the root group_policy mode and raw decimal tokens',
    () => {
      const raw =
        '{"schema_version":1,"group_policy":"override","rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,' +
        '"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}'
      let emitted = ''
      const { root } = mountComponent(registry['PolicyEditor.vue'], {
        modelValue: raw,
        'onUpdate:modelValue': (value) => (emitted = value),
      })
      setValue(uiNode(root, zhCN.rule.name), 'renamed')
      assert.ok(emitted.includes('"group_policy":"override"'), emitted)
      assert.ok(emitted.includes('"value":0.10000000000000001'), emitted)
      assert.equal(JSON.parse(emitted).group_policy, 'override')
    },
  ],

  [
    'inherited rules start collapsed and folding never writes config',
    async () => {
      for (const disabled of [false, true]) {
        let updated = 0
        const { root } = mountComponent(registry['PolicyRuleCard.vue'], {
          rule: firstRule(),
          index: 0,
          total: 1,
          disabled,
          onUpdate: () => updated++,
        })
        const toggle = find(root, (n) => n.tag === 'ui-mock' && 'aria-expanded' in n.props)
        assert.equal(toggle.props['aria-expanded'], !disabled)
        assert.equal(Boolean(uiNode(root, zhCN.rule.name)), !disabled)
        click(toggle)
        await Vue.nextTick()
        assert.equal(toggle.props['aria-expanded'], disabled)
        const body = find(root, (n) => n.props.class === 'policy-rule-body')
        assert.equal(body.style.display === 'none', !disabled)
        assert.equal(updated, 0)
      }
    },
  ],

  [
    'lastEmitted echo suppression supports foreign A -> B -> A updates',
    async () => {
      const configA = JSON.stringify({
        schema_version: 1,
        rules: [
          {
            id: 'r1',
            name: 'Rule A',
            domain: 'scheduling',
            enabled: true,
            when: { fact: 'request.model', op: 'eq', value: 'a' },
            then: { type: 'exclude_candidate' },
          },
        ],
      })
      const configB = JSON.stringify({
        schema_version: 1,
        rules: [
          {
            id: 'r1',
            name: 'Rule B',
            domain: 'scheduling',
            enabled: true,
            when: { fact: 'request.model', op: 'eq', value: 'b' },
            then: { type: 'exclude_candidate' },
          },
        ],
      })
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], configA)
      const { root } = mountComponent(wrapper)
      const nameInput = () => uiNode(root, zhCN.rule.name)
      assert.equal(nameInput()?.props.modelValue, 'Rule A')
      setValue(nameInput(), 'Emitted A')
      await Vue.nextTick()
      const emittedA = state.value
      assert.equal(nameInput()?.props.modelValue, 'Emitted A')
      // 外部更新为 B
      state.value = configB
      await Vue.nextTick()
      assert.equal(nameInput()?.props.modelValue, 'Rule B')
      // 外部更新回 A (A -> B -> A)，不应被误判为回声而停留在 B
      state.value = emittedA
      await Vue.nextTick()
      assert.equal(nameInput()?.props.modelValue, 'Emitted A')
    },
  ],

  [
    'budget prevalidation prevents 101 rules from hiding visual editor',
    async () => {
      // 构造正好达到上限 100 条规则的合法配置
      const hundredRules = JSON.stringify({
        schema_version: 1,
        rules: Array.from({ length: 100 }, (_, i) => ({
          id: `r-${i}`,
          name: `Rule ${i}`,
          domain: 'scheduling',
          enabled: false,
          when: { fact: 'request.model', op: 'eq', value: 'm' },
          then: { type: 'exclude_candidate' },
        })),
      })
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: hundredRules })
      // 100 条规则时，新增按钮应被禁用
      const addBtn = find(root, (n) => n.tag === 'ui-mock' && texts(n).includes(zhCN.addRule))
      assert.ok(addBtn)
      assert.equal(addBtn.props.disabled, true)
      // 复制按钮应被禁用
      const copyBtns = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.duplicate,
      )
      assert.equal(copyBtns.length, 100)
      assert.equal(copyBtns[0].props.disabled, true)
      // 视觉规则树正常展示，未被 fatal notice 隐藏
      assert.equal(
        findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.name).length,
        100,
      )
    },
  ],

  [
    'rejected node-budget edits keep the current rules editable',
    async () => {
      const config = JSON.stringify({
        schema_version: 1,
        rules: Array.from({ length: 16 }, (_, i) => ({
          id: `budget-${i}`,
          name: `Budget ${i}`,
          domain: 'scheduling',
          enabled: false,
          when: {
            all: Array.from({ length: 255 }, () => ({
              fact: 'request.model',
              op: 'eq',
              value: 'm',
            })),
          },
          then: { type: 'exclude_candidate' },
        })),
      })
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], config)
      const { root } = mountComponent(wrapper)
      const copies = () => findAll(root, (node) => node.props.label === zhCN.rule.duplicate)
      assert.equal(copies().length, 16)
      click(copies()[0])
      await Vue.nextTick()
      assert.equal(state.value, config)
      assert.equal(copies().length, 16)
      const remove = find(root, (node) => node.props.label === zhCN.rule.remove)
      click(remove)
      await Vue.nextTick()
      assert.equal(JSON.parse(state.value).rules.length, 15)
    },
  ],

  [
    'JSON-only conditions do not report invalid local drafts',
    async () => {
      let status
      mountComponent(registry['PolicyEditor.vue'], {
        modelValue: JSON.stringify({
          schema_version: 1,
          rules: [
            {
              id: 'json-only',
              name: 'JSON only',
              domain: 'scheduling',
              enabled: false,
              when: { not: { fact: 'request.model', op: 'eq', value: 'm' } },
              then: { type: 'exclude_candidate' },
            },
          ],
        }),
        onDraftStatus: (next) => {
          status = next
        },
      })
      await Vue.nextTick()
      assert.equal(status.valid, true)
      assert.equal(status.hasInvalid, false)
    },
  ],

  [
    'duplicate weekday cancellation does not become empty',
    async () => {
      // 带有重复星期 [1, 1] 的配置
      const duplicateDay = model.objectNode([
        { key: 'predicate', value: model.stringLiteral('time_window') },
        {
          key: 'weekdays',
          value: model.arrayNode([model.numberLiteral('1'), model.numberLiteral('1')]),
        },
        {
          key: 'ranges',
          value: model.arrayNode([
            model.arrayNode([model.stringLiteral('09:00'), model.stringLiteral('18:00')]),
          ]),
        },
      ])
      const { state, wrapper } = stateful(registry['PolicyTimeWindowLeaf.vue'], duplicateDay)
      const { root } = mountComponent(wrapper)
      // 尝试取消星期一 (day 1)
      setValue(uiNode(root, zhCN.timeWindow.weekday['1']), false)
      await Vue.nextTick()
      // weekdays 不应变为空数组，且应当显示星期错误提示
      const weekdays = model.arrayItems(model.getField(state.value, 'weekdays')) ?? []
      assert.ok(weekdays.length >= 1)
      assert.ok(texts(root).includes(zhCN.timeWindow.errors.weekdayRequired))
    },
  ],

  [
    'test host unbinds detached event handlers',
    () => {
      const { host } = mountComponent(registry['PolicyRuleCard.vue'], {
        rule: firstRule(),
        index: 0,
        total: 1,
      })
      const el = host.createElement('div')
      let fired = 0
      host.patchProp(el, 'onClick', null, () => fired++)
      assert.equal(typeof el.events.onClick, 'function')
      el.events.onClick()
      assert.equal(fired, 1)
      // 解绑
      host.patchProp(el, 'onClick', el.events.onClick, null)
      assert.equal(el.events.onClick, undefined)
    },
  ],

  [
    'visual draft status aggregates invalid and pending child inputs, preserves drafts through collapse/expand and retains original text',
    async () => {
      let draftStatus = null
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], twoPricing, {
        onDraftStatus: (status) => {
          draftStatus = status
        },
      })
      const { root } = mountComponent(wrapper)
      await Vue.nextTick()
      assert.ok(draftStatus)
      assert.equal(draftStatus.valid, true)
      assert.equal(draftStatus.hasInvalid, false)

      // 输入非法的 factor
      const factorField = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor,
      )[0]
      setValue(factorField, 'not-a-number')
      await Vue.nextTick()
      assert.equal(draftStatus.valid, false)
      assert.equal(draftStatus.hasInvalid, true)
      const toggle = find(root, (n) => n.tag === 'ui-mock' && 'aria-expanded' in n.props)
      click(toggle)
      await Vue.nextTick()
      assert.equal(draftStatus.hasInvalid, true)
      assert.equal(draftStatus.pending, true)

      // 再次展开卡片，验证 draft 保留且正文不变
      click(toggle)
      await Vue.nextTick()
      assert.equal(factorField.props.modelValue, 'not-a-number')
      assert.equal(draftStatus.pending, true)
      assert.equal(state.value, twoPricing)
    },
  ],

  [
    'time range drafts survive edits and rejected weekday removal stays valid',
    async () => {
      let reported = null
      const timeCondition = model.objectNode([
        { key: 'predicate', value: model.stringLiteral('time_window') },
        { key: 'weekdays', value: model.arrayNode([model.numberLiteral('1')]) },
        {
          key: 'ranges',
          value: model.arrayNode([
            model.arrayNode([model.stringLiteral('09:00'), model.stringLiteral('18:00')]),
          ]),
        },
      ])
      const wrapper = Vue.defineComponent({
        setup: () => {
          const { summary } = draftModule.providePolicyDraftCollector()
          Vue.watch(summary, (s) => (reported = s), { immediate: true })
          const val = Vue.ref(timeCondition)
          return () =>
            Vue.h(registry['PolicyTimeWindowLeaf.vue'], {
              modelValue: val.value,
              'onUpdate:modelValue': (next) => (val.value = next),
            })
        },
      })
      const editor = mountComponent(wrapper)
      await Vue.nextTick()
      assert.equal(reported.valid, true)

      setValue(uiNode(editor.root, zhCN.timeWindow.weekday['1']), false)
      await Vue.nextTick()
      assert.ok(texts(editor.root).includes(zhCN.timeWindow.errors.weekdayRequired))
      assert.equal(reported.valid, true)
      assert.equal(reported.hasInvalid, false)
      assert.equal(reported.pending, false)

      // 第一行修改为非法时间
      const startInputs = () =>
        findAll(editor.root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.from)
      setValue(startInputs()[0], 'garbage')
      await Vue.nextTick()
      assert.equal(reported.valid, false)

      // 追加时间段
      const addBtn = find(
        editor.root,
        (n) => n.tag === 'ui-mock' && texts(n).includes(zhCN.timeWindow.addRange),
      )
      click(addBtn)
      await Vue.nextTick()

      // 第一行的非法草稿仍然保留，未被覆盖为 09:00，新增行获得默认 09:00
      const currentInputs = startInputs()
      assert.equal(currentInputs.length, 2)
      assert.equal(currentInputs[0].props.modelValue, 'garbage')
      assert.equal(currentInputs[1].props.modelValue, '09:00')
      assert.equal(reported.valid, false)

      // 删除含有非法草稿的第一行，目标草稿被明确丢弃，第二行草稿（09:00）上移并恢复合法
      const removeBtns = () =>
        findAll(
          editor.root,
          (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.removeRange,
        )
      click(removeBtns()[0])
      await Vue.nextTick()

      const remainingInputs = startInputs()
      assert.equal(remainingInputs.length, 1)
      assert.equal(remainingInputs[0].props.modelValue, '09:00')
      assert.equal(reported.valid, true)
    },
  ],

  [
    'policy and same-id group refresh preserve uncommitted inputs and dirty guards',
    async () => {
      const original = {
        scope: 'group',
        id: 1,
        groupId: 1,
        revisionText: '1',
        configText: twoPricing,
      }
      const policyData = Vue.ref(original)
      const cache = { cancelQueries: async () => {}, setQueryData: () => {} }
      const panelPath = path.resolve(dir, '../GroupPolicyPanel.vue')
      const panelMocks = {
        __esModule: true,
        groupPolicyKey: (id) => ['modern', 'group-policy', id],
        credentialPolicyKey: (g, c) => ['modern', 'credential-policy', g, c],
        policyDiscoveryKey: (g, c) => ['modern', 'policy-discovery', g, c],
        getPolicy: async () => original,
        getPolicyDiscovery: async () => ({ quota_windows: [] }),
        savePolicy: async () => original,
      }
      const panelRequire = (name) => {
        if (name === 'vue') return Vue
        if (name === 'vue-i18n') return { useI18n: () => ({ t }) }
        if (name === '@tanstack/vue-query')
          return {
            useQuery: (opts) => ({
              data: opts.queryKey.value.includes('policy-discovery')
                ? Vue.ref({ quota_windows: [] })
                : policyData,
              isPending: Vue.ref(false),
              isError: Vue.ref(false),
            }),
            useQueryClient: () => cache,
          }
        if (name === '@modern/api/group-detail') return panelMocks
        if (name === './policy-editor/policy-model') return model
        if (name === './policy-editor/PolicyEditor.vue')
          return { __esModule: true, default: registry['PolicyEditor.vue'] }
        if (name === '@modern/components/ui/clipboard') return { copyText: async () => false }
        if (name === '@shared/http/client-context') return { useApiClient: () => ({}) }
        if (name === '@shared/http/errors') return { ApiError: class ApiError extends Error {} }
        if (name === '@modern/components/ui') return passthroughProxy
        return { __esModule: true, default: passthrough }
      }
      const panelCtx = compileSfc(panelPath, readFileSync(panelPath, 'utf8'), panelRequire, {
        transform: (code) =>
          code.replace(
            'return (_ctx: any,_cache: any) =>',
            'globalThis.__state={dirty, serializedDirty, draft, baseline, visualDraftStatus, currentPolicy, saving, serverError, jsonValidation, editorMode, switchEditorMode, save, saveDisabled}; return (_ctx: any,_cache: any) =>',
          ),
        extra: { AbortController, TextEncoder },
      })
      const group = Vue.ref({ id: 1, name: 'g' })
      const host = Vue.defineComponent({
        setup: () => () => Vue.h(panelCtx.exports.default, { group: group.value }),
      })
      const panel = mountComponent(host)
      await Vue.nextTick()
      assert.equal(panel.errors.length, 0)

      // 输入非法的 factor
      setValue(uiNode(panel.root, zhCN.action.factor), 'garbage')
      await Vue.nextTick()

      // collector 变 invalid/pending
      assert.equal(panelCtx.__state.visualDraftStatus.value.valid, false)
      assert.equal(panelCtx.__state.visualDraftStatus.value.pending, true)
      // serializedDirty 仍为 false（未提交到正文）
      assert.equal(panelCtx.__state.serializedDirty.value, false)
      // 但 Panel dirty 与 Workspace dirty 已被置为 true，纳入刷新与离开保护
      assert.equal(panelCtx.__state.dirty.value, true)
      const workspace = find(panel.root, (n) => n.tag === 'ui-mock' && 'dirty' in n.props)
      assert.equal(workspace.props.dirty, true)
      // 保存按钮保持禁用
      assert.equal(panelCtx.__state.saveDisabled.value, true)

      // 远端推送新配置（factor 5）触发 query.data 刷新
      policyData.value = {
        ...original,
        revisionText: '2',
        configText: twoPricing.replace('"factor":"2"', '"factor":"5"'),
      }
      await Vue.nextTick()

      // 本地草稿受 dirty 保护，未被远端刷新覆盖，且 collector 仍保持 invalid
      assert.equal(uiNode(panel.root, zhCN.action.factor).props.modelValue, 'garbage')
      assert.equal(panelCtx.__state.visualDraftStatus.value.valid, false)
      const draftBeforeRefresh = panelCtx.__state.draft.value
      group.value = { id: 1, name: 'refreshed group' }
      await Vue.nextTick()
      assert.equal(panelCtx.__state.draft.value, draftBeforeRefresh)
      assert.equal(panelCtx.__state.dirty.value, true)
      assert.equal(uiNode(panel.root, zhCN.action.factor).props.modelValue, 'garbage')
    },
  ],

  [
    'exponential quota threshold is rendered as JSON-only and does not report invalid draft',
    async () => {
      const expCondition = model.parseRawJson(
        '{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":1e-1}',
      )
      let collectorSummary
      const host = Vue.defineComponent({
        setup() {
          const { summary } = draftModule.providePolicyDraftCollector()
          collectorSummary = summary
          const ast = Vue.ref(expCondition)
          return () =>
            Vue.h(registry['PolicyConditionEditor.vue'], {
              modelValue: ast.value,
              'onUpdate:modelValue': (next) => {
                ast.value = next
              },
            })
        },
      })
      const mounted = mountComponent(host)
      await Vue.nextTick()

      // 指数格式门限作为 JSON-only 呈现，草稿收集器合法，不锁死保存或切换 JSON
      assert.equal(collectorSummary.value.valid, true)
      assert.equal(collectorSummary.value.hasInvalid, false)
      assert.ok(texts(mounted.root).includes(zhCN.condition.unsupported))
      assert.ok(texts(mounted.root).includes('1e-1'))
    },
  ],
]

for (const [name, run] of cases) test(name, run)
