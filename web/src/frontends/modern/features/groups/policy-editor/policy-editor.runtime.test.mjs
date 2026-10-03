// 真实 SFC 编译到自定义渲染器的交互回归。单一 harness、共享 fixture、flat 条件与行内卡片用例。
// 运行：node --test web/src/frontends/modern/features/groups/policy-editor/policy-editor.runtime.test.mjs
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import test from 'node:test'

import { compileSfc, mountComponent, Vue } from '../../../../../../scripts/policy-test-utils.mjs'
import { enUS, jaJP, zhCN } from '../../../i18n/locales/policy-editor.ts'
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
const uiNode = (node, label) => find(node, (n) => n.tag === 'ui-mock' && n.props.label === label)
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
      assert.ok(texts(root).includes(zhCN.emptyRules))
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
        assert.ok(texts(root).includes(messages[code].policyEditor.title))
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
      assert.ok(texts(root).includes(zhCN.condition.empty))

      fire(menu(), 'onSelect', 'param')
      await Vue.nextTick()
      fire(menu(), 'onSelect', 'time_window')
      await Vue.nextTick()
      assert.equal(kinds('all').join(','), 'param,time_window')

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
    'switching a flat leaf to a numeric quota fact keeps its operator and ratio controls',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', [model.newFactCondition('request.model')]),
      )
      const { root } = mountComponent(wrapper)
      setValue(uiNode(root, zhCN.fact.key), 'credential.quota.remaining_ratio')
      await Vue.nextTick()
      assert.ok(!texts(root).includes(zhCN.condition.unsupported))
      const op = optionSelect(root, zhCN.fact.operator)
      assert.deepEqual(
        op.props.options.map((option) => option.value),
        ['eq', 'lt', 'lte', 'gt', 'gte'],
      )
      setValue(op, 'lt')
      await Vue.nextTick()
      const value = uiNode(root, zhCN.fact.value)
      assert.ok(value)
      setValue(value, '0.25')
      fire(value, 'onChange')
      await Vue.nextTick()
      const leaf = (model.arrayItems(model.getField(state.value, 'all')) ?? [])[0]
      assert.equal(model.literalString(model.getField(leaf, 'op')), 'lt')
      assert.equal(model.literalNumberRaw(model.getField(leaf, 'value')), '0.25')
      assert.equal(
        model.literalNumberRaw(model.getField(model.getField(leaf, 'select'), 'window_seconds')),
        '18000',
      )
    },
  ],

  [
    'quota row keeps the raw decimal while the window is edited',
    async () => {
      const { state, wrapper } = stateful(registry['PolicyFactLeaf.vue'], quotaRaw)
      const { root } = mountComponent(wrapper)
      assert.equal(uiNode(root, zhCN.fact.value).props.modelValue, '0.10000000000000001')
      const window = uiNode(root, zhCN.fact.quota.windowSeconds)
      setValue(window, '3600')
      fire(window, 'onChange')
      await Vue.nextTick()
      assert.equal(
        model.literalNumberRaw(model.getField(state.value, 'value')),
        '0.10000000000000001',
      )
      assert.equal(
        model.literalNumberRaw(
          model.getField(model.getField(state.value, 'select'), 'window_seconds'),
        ),
        '3600',
      )
    },
  ],

  [
    'a pending time-window draft survives an unrelated weekday edit',
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
    'a disabled rule card never opens removal confirmation or emits remove',
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
      assert.ok(!texts(root).includes(zhCN.rule.confirmRemove))
      assert.equal(removed, 0)
    },
  ],

  [
    'switching a card to disabled closes a pending confirmation',
    async () => {
      const disabled = Vue.ref(false)
      let removed = 0
      const wrapper = Vue.defineComponent({
        setup: () => () =>
          Vue.h(registry['PolicyRuleCard.vue'], {
            rule: firstRule(),
            index: 0,
            total: 1,
            disabled: disabled.value,
            onRemove: () => removed++,
          }),
      })
      const { root } = mountComponent(wrapper)
      click(uiNode(root, zhCN.rule.remove))
      await Vue.nextTick()
      assert.ok(texts(root).includes(zhCN.rule.confirmRemove))
      disabled.value = true
      await Vue.nextTick()
      assert.ok(!texts(root).includes(zhCN.rule.confirmRemove))
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
      assert.equal(drafts.filter((value) => value === 'not-a-number').length, 1)
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
      const copy = result.rules.find((rule) => rule.id === 'rule-1')
      assert.equal(copy?.enabled, false)
      assert.equal(copy?.name, 'first')
    },
  ],
]

for (const [name, run] of cases) test(name, run)
