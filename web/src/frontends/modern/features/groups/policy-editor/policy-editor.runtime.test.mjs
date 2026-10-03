// 真实 SFC 编译到自定义渲染器的交互回归。单一 harness、共享 fixture、13 个用例。
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

// ---- UI/图标替身：把 attrs（label / modelValue / onUpdate:modelValue / onClick）透传给宿主节点。 ----
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
const mount = mountComponent
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
const click = (node) => node.events.onClick?.()
const setValue = (node, value) => node.events['onUpdate:modelValue']?.(value)

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
const firstRule = () => model.readVisualDocument(twoPricing).rules[0]

// ---- 用例表：全部复用同一个 harness。 ----
const cases = [
  [
    'blank input is fatal-empty, not an empty rule list',
    () => {
      const { root } = mount(registry['PolicyEditor.vue'], { modelValue: '' })
      assert.ok(texts(root).includes(zhCN.errors.empty))
      assert.ok(!texts(root).includes(zhCN.emptyRules))
    },
  ],

  [
    'locale switches reactively across all three languages',
    async () => {
      const { root } = mount(registry['PolicyEditor.vue'], { modelValue: emptyConfig })
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
      const { root, errors } = mount(registry['PolicyEditor.vue'], { modelValue: deepJson })
      assert.equal(errors.length, 0)
      assert.ok(texts(root).includes(zhCN.errors.syntax))
    },
  ],

  [
    'malformed replacement leaf stays JSON-only and keeps its raw payload',
    () => {
      const { root } = mount(registry['PolicyConditionEditor.vue'], {
        modelValue: malformedLeaf,
        depth: 1,
      })
      assert.ok(texts(root).includes(zhCN.condition.unsupported))
      assert.ok(texts(root).includes('"unknown"'))
      assert.equal(uiNode(root, zhCN.fact.value), undefined)
    },
  ],

  [
    'a pending time-window draft survives an unrelated weekday edit',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyTimeWindowLeaf.vue'],
        model.newTimeWindowCondition(),
      )
      const { root } = mount(wrapper)
      setValue(uiNode(root, zhCN.timeWindow.from), 'garbage')
      await Vue.nextTick()
      setValue(uiNode(root, zhCN.timeWindow.weekday['2']), true)
      await Vue.nextTick()
      assert.match(model.serializeJson(state.value), /"weekdays":\[1,2\]/)
      assert.equal(uiNode(root, zhCN.timeWindow.from).props.modelValue, 'garbage')
    },
  ],

  [
    'a disabled rule card never opens removal confirmation or emits remove',
    async () => {
      let removed = 0
      const { root } = mount(registry['PolicyRuleCard.vue'], {
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
      const { root } = mount(wrapper)
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
      const { root } = mount(registry['PolicyEditor.vue'], { modelValue: twoPricing })
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
    'condition append is blocked at the depth limit and emits nothing',
    async () => {
      const initial = model.newConditionGroup('all', [model.newTimeWindowCondition()])
      const { state, wrapper } = stateful(registry['PolicyConditionEditor.vue'], initial, {
        depth: 16,
      })
      const { root } = mount(wrapper)
      const add = find(
        root,
        (n) =>
          n.tag === 'ui-mock' &&
          n.props.disabled === true &&
          texts(n).includes(zhCN.condition.addCondition),
      )
      assert.ok(add)
      const before = model.serializeJson(state.value)
      click(add)
      await Vue.nextTick()
      assert.equal(model.serializeJson(state.value), before)
    },
  ],

  [
    'depth gate: 15 blocks group adds but allows a leaf, 16 blocks both',
    async () => {
      const build = (depth) =>
        stateful(
          registry['PolicyConditionEditor.vue'],
          model.newConditionGroup('all', [model.newFactCondition('request.model')]),
          { depth },
        )
      const addControls = (root) => {
        const box = find(
          root,
          (n) => n.tag === 'div' && String(n.props.class).includes('policy-condition-add'),
        )
        const select = findAll(box, (n) => Array.isArray(n.props.options)).find(
          (n) => n.props.modelValue === 'time_window',
        )
        return { select, button: find(box, (n) => n.tag === 'ui-mock' && n.props.icon) }
      }
      const disabledFor = (select, values) =>
        select.props.options.filter((o) => values.includes(o.value)).map((o) => !!o.disabled)
      const apply = async (select, button, value) => {
        setValue(select, value)
        await Vue.nextTick()
        const wasDisabled = button.props.disabled
        click(button)
        await Vue.nextTick()
        return wasDisabled
      }

      const shallow = build(15)
      const shallowRoot = mount(shallow.wrapper).root
      const { select, button } = addControls(shallowRoot)
      assert.equal(disabledFor(select, ['all', 'any', 'not']).join(','), 'false,false,false')
      assert.equal(disabledFor(select, ['param', 'time_window']).join(','), 'false,false')
      const at15 = model.serializeJson(shallow.state.value)
      assert.equal(await apply(select, button, 'all'), true) // 分组被按钮拦下
      assert.equal(model.serializeJson(shallow.state.value), at15)
      assert.equal(await apply(select, button, 'time_window'), false) // 叶子仍可加
      assert.notEqual(model.serializeJson(shallow.state.value), at15)

      const deep = build(16)
      const deepRoot = mount(deep.wrapper).root
      const deepControls = addControls(deepRoot)
      assert.equal(
        disabledFor(deepControls.select, ['param', 'time_window']).join(','),
        'true,true',
      )
      assert.equal(deepControls.button.props.disabled, true)
      const at16 = model.serializeJson(deep.state.value)
      await apply(deepControls.select, deepControls.button, 'all')
      assert.equal(model.serializeJson(deep.state.value), at16)
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
      const { root } = mount(wrapper)
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
    'all-to-any conversion preserves every child condition',
    async () => {
      const children = [
        { type: 'literal', literal: 'string', raw: '"x"' },
        model.newFactCondition('request.model'),
      ]
      const { state, wrapper } = stateful(
        registry['PolicyConditionEditor.vue'],
        model.newConditionGroup('all', children),
        { depth: 1 },
      )
      const { root } = mount(wrapper)
      setValue(uiNode(root, zhCN.condition.kindLabel), 'any')
      await Vue.nextTick()
      const converted = model.arrayItems(model.getField(state.value, 'any')) ?? []
      assert.equal(converted.length, 2)
      assert.equal(model.serializeJson(converted[0]), model.serializeJson(children[0]))
      assert.equal(model.serializeJson(converted[1]), model.serializeJson(children[1]))
    },
  ],

  [
    'converting into a leaf is not offered and changes nothing',
    async () => {
      const initial = model.newFactCondition('request.model')
      const { state, wrapper } = stateful(registry['PolicyConditionEditor.vue'], initial, {
        depth: 1,
      })
      const { root } = mount(wrapper)
      setValue(uiNode(root, zhCN.condition.kindLabel), 'time_window')
      await Vue.nextTick()
      assert.equal(model.serializeJson(state.value), model.serializeJson(initial))
    },
  ],

  [
    'name/id/enabled edits emit an updated rule node',
    async () => {
      let updated
      const { root } = mount(registry['PolicyRuleCard.vue'], {
        rule: firstRule(),
        index: 0,
        total: 1,
        onUpdate: (node) => (updated = node),
      })
      setValue(uiNode(root, zhCN.rule.name), 'renamed')
      assert.equal(model.literalString(model.getField(updated, 'name')), 'renamed')
      setValue(uiNode(root, zhCN.rule.id), 'renamed-id')
      assert.equal(model.literalString(model.getField(updated, 'id')), 'renamed-id')
      setValue(uiNode(root, zhCN.rule.enabled), false)
      assert.equal(model.literalBoolean(model.getField(updated, 'enabled')), false)
    },
  ],

  [
    'duplicate inserts a disabled copy with a fresh id',
    async () => {
      const { state, wrapper } = stateful(registry['PolicyEditor.vue'], twoPricing)
      const { root } = mount(wrapper)
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
