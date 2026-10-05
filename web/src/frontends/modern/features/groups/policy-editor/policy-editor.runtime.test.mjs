// 真实 SFC 编译到自定义渲染器的交互回归。单一 harness、共享 fixture、扁平条件与卡片用例。
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
  'PolicyModelInput.vue',
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
  find(node, (n) => n.tag === 'ui-mock' && n.props.label === label && Array.isArray(n.props.options))
const plain = (value) => JSON.parse(JSON.stringify(value))
const click = (node) => node.events.onClick?.()
const setValue = (node, value) => node.events['onUpdate:modelValue']?.(value)
const fire = (node, event, ...args) => node.events[event]?.(...args)

// 持有单个 v-model 属性/事件的宿主。
function stateful(component, initial, propName, eventName, extra = {}) {
  const state = Vue.ref(initial)
  const wrapper = Vue.defineComponent({
    setup: () => () =>
      Vue.h(component, {
        ...extra,
        [propName]: state.value,
        [eventName]: (value) => (state.value = value),
      }),
  })
  return { state, wrapper }
}

// 条件编辑器绑定 match + conditions 两个属性，一次性接收 {match, conditions}。
function conditionHost(match, conditions, extra = {}) {
  const state = Vue.ref({ match, conditions })
  const wrapper = Vue.defineComponent({
    setup: () => () =>
      Vue.h(registry['PolicyConditionEditor.vue'], {
        ...extra,
        match: state.value.match,
        conditions: state.value.conditions,
        onUpdate: (value) => (state.value = value),
      }),
  })
  return { state, wrapper }
}

// ---- 共享 fixture。 ----
const emptyConfig = '{"schema_version":1,"rules":[]}'
const invalidJson = '{"schema_version":1,"rules":['
const flatRule = (changes = {}) => ({
  id: 'r',
  name: 'r',
  domain: 'scheduling',
  enabled: false,
  when: { all: [{ fact: 'request.model', op: 'eq', value: 'm' }] },
  actions: [{ type: 'exclude_candidate' }],
  ...changes,
})
const configWith = (rules) => JSON.stringify({ schema_version: 1, rules })
const pricingRule = (id, value, factor) =>
  flatRule({
    id,
    name: id,
    domain: 'pricing',
    enabled: true,
    when: { all: [{ fact: 'request.model', op: 'eq', value }] },
    actions: [{ type: 'multiply_price', factor }],
  })
const twoPricing = configWith([pricingRule('first', 'a', '2'), pricingRule('second', 'b', '3')])
const quotaCondition = {
  kind: 'param',
  fact: 'credential.quota.remaining_ratio',
  op: 'lt',
  value: 0.1,
  windowSeconds: 18000,
}

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
    'invalid JSON reports a syntax error and renders no rules',
    () => {
      const { root, errors } = mountComponent(registry['PolicyEditor.vue'], {
        modelValue: invalidJson,
      })
      assert.equal(errors.length, 0)
      assert.ok(texts(root).includes(zhCN.errors.syntax))
    },
  ],

  [
    'nested structures fall back to read-only JSON',
    () => {
      const nested = configWith([
        flatRule({ when: { all: [{ all: [{ fact: 'request.model', op: 'eq', value: 'm' }] }] } }),
      ])
      const { root, errors } = mountComponent(registry['PolicyEditor.vue'], { modelValue: nested })
      assert.equal(errors.length, 0)
      assert.ok(texts(root).includes(zhCN.advancedTitle))
      assert.ok(texts(root).includes('request.model'))
      assert.equal(uiNode(root, zhCN.rule.name), undefined)
    },
  ],

  [
    'name and enabled edits emit an updated rule while the id stays stable',
    () => {
      const { state, wrapper } = stateful(
        registry['PolicyEditor.vue'],
        configWith([flatRule()]),
        'modelValue',
        'onUpdate:modelValue',
      )
      const { root } = mountComponent(wrapper)
      setValue(uiNode(root, zhCN.rule.name), 'renamed')
      const rule = model.readVisualDocument(state.value).document.rules[0]
      assert.equal(rule.name, 'renamed')
      assert.equal(rule.id, 'r')
      setValue(uiNode(root, zhCN.rule.enabled), true)
      assert.equal(model.readVisualDocument(state.value).document.rules[0].enabled, true)
    },
  ],

  [
    'flat condition flow adds through the menu, switches group, and removes',
    async () => {
      const { state, wrapper } = conditionHost('all', [])
      const { root } = mountComponent(wrapper)
      const menu = () => find(root, (n) => n.tag === 'ui-mock' && Array.isArray(n.props.items))
      assert.equal(
        menu()
          .props.items.map((item) => item.id)
          .join(','),
        'param,time_window',
      )
      for (const id of ['param', 'time_window']) {
        fire(menu(), 'onSelect', id)
        await Vue.nextTick()
      }
      assert.deepEqual(plain(state.value.conditions.map((condition) => condition.kind)), [
        'param',
        'time_window',
      ])
      assert.ok(texts(root).includes(zhCN.condition.itemParam))
      assert.ok(texts(root).includes(zhCN.condition.itemTimeWindow))

      setValue(optionSelect(root, zhCN.condition.title), 'any')
      await Vue.nextTick()
      assert.equal(state.value.match, 'any')
      assert.equal(state.value.conditions.length, 2)

      click(findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.condition.remove)[0])
      await Vue.nextTick()
      assert.deepEqual(plain(state.value.conditions.map((condition) => condition.kind)), [
        'time_window',
      ])
    },
  ],

  [
    'string fact exposes eq and in operators and converts values losslessly',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyFactLeaf.vue'],
        model.newFactCondition('request.model'),
        'condition',
        'onUpdate',
      )
      const { root } = mountComponent(wrapper)
      const op = optionSelect(root, zhCN.fact.operator)
      assert.deepEqual(
        op.props.options.map((option) => option.value),
        ['eq', 'in'],
      )
      setValue(uiNode(root, zhCN.fact.value), 'gpt-4o')
      await Vue.nextTick()
      assert.equal(state.value.value, 'gpt-4o')
      setValue(op, 'in')
      await Vue.nextTick()
      assert.equal(state.value.op, 'in')
      assert.deepEqual(plain(state.value.value), ['gpt-4o'])
      setValue(op, 'eq')
      await Vue.nextTick()
      assert.equal(state.value.op, 'eq')
      assert.equal(state.value.value, 'gpt-4o')
    },
  ],

  [
    'quota threshold blur clears pending when the numeric value is unchanged',
    async () => {
      let reported = null
      const wrapper = Vue.defineComponent({
        setup: () => {
          const { summary } = draftModule.providePolicyDraftCollector()
          Vue.watch(summary, (s) => (reported = s), { immediate: true })
          const val = Vue.ref(model.newQuotaFactCondition(18000))
          return () =>
            Vue.h(registry['PolicyFactLeaf.vue'], {
              condition: val.value,
              onUpdate: (next) => (val.value = next),
            })
        },
      })
      const { root } = mountComponent(wrapper)
      await Vue.nextTick()
      assert.equal(reported.pending, false)
      const threshold = uiNode(root, zhCN.fact.value)
      setValue(threshold, '0.10')
      await Vue.nextTick()
      assert.equal(reported.pending, true)
      fire(threshold, 'onBlur')
      await Vue.nextTick()
      assert.equal(reported.pending, false)
      assert.equal(uiNode(root, zhCN.fact.value).props.modelValue, '0.1')
    },
  ],

  [
    'switching a flat leaf to a quota window keeps operator and threshold',
    async () => {
      const host = conditionHost('all', [model.newFactCondition('request.model')], {
        quotaWindows: [18000, 604800],
      })
      const { root } = mountComponent(host.wrapper)
      const fact = optionSelect(root, zhCN.fact.key)
      assert.equal(
        fact.props.options.map((option) => option.value).join(','),
        'request.model,upstream.model,quota:18000,quota:604800',
      )
      setValue(fact, 'quota:18000')
      await Vue.nextTick()
      let condition = host.state.value.conditions[0]
      assert.equal(condition.fact, 'credential.quota.remaining_ratio')
      assert.equal(condition.windowSeconds, 18000)
      assert.equal(condition.op, 'lt')
      setValue(optionSelect(root, zhCN.fact.operator), 'gt')
      await Vue.nextTick()
      const threshold = uiNode(root, zhCN.fact.value)
      setValue(threshold, '0.25')
      fire(threshold, 'onChange')
      await Vue.nextTick()
      // 同一额度参数换窗口：只改 windowSeconds，保留 op/threshold。
      setValue(optionSelect(root, zhCN.fact.key), 'quota:604800')
      await Vue.nextTick()
      condition = host.state.value.conditions[0]
      assert.equal(condition.windowSeconds, 604800)
      assert.equal(condition.op, 'gt')
      assert.equal(condition.value, 0.25)
    },
  ],

  [
    'quota options follow the seeded catalog and retain unobserved windows',
    async () => {
      const scenarios = [
        { windows: [18000], expected: ['quota:18000'] },
        { windows: [604800, 18000], expected: ['quota:18000', 'quota:604800'] },
      ]
      for (const scenario of scenarios) {
        const { wrapper } = stateful(
          registry['PolicyFactLeaf.vue'],
          model.newFactCondition('request.model'),
          'condition',
          'onUpdate',
          { quotaWindows: scenario.windows },
        )
        const { root } = mountComponent(wrapper)
        const quotas = optionSelect(root, zhCN.fact.key).props.options.filter((option) =>
          option.value.startsWith('quota:'),
        )
        assert.equal(
          quotas.map((option) => option.value).join(','),
          scenario.expected.join(','),
          JSON.stringify(scenario.windows),
        )
      }
      const retained = stateful(
        registry['PolicyFactLeaf.vue'],
        quotaCondition,
        'condition',
        'onUpdate',
        { quotaWindows: [604800] },
      )
      const mounted = mountComponent(retained.wrapper)
      assert.equal(
        optionSelect(mounted.root, zhCN.fact.key)
          .props.options.map((option) => option.value)
          .join(','),
        'request.model,upstream.model,quota:604800,quota:18000',
      )
      assert.ok(texts(mounted.root).includes(zhCN.fact.descriptions.quotaRetained))
    },
  ],

  [
    'the quota catalog threads from the editor down to the leaf',
    () => {
      const { root } = mountComponent(registry['PolicyEditor.vue'], {
        modelValue: configWith([flatRule()]),
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
    'a pending time-window draft survives weekday edits and range removal',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyTimeWindowLeaf.vue'],
        model.newTimeWindowCondition(),
        'condition',
        'onUpdate',
      )
      const { root } = mountComponent(wrapper)
      setValue(uiNode(root, zhCN.timeWindow.from), 'garbage')
      await Vue.nextTick()
      setValue(uiNode(root, zhCN.timeWindow.weekday['2']), true)
      await Vue.nextTick()
      assert.deepEqual(plain(state.value.weekdays), [1, 2])
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
    'time range drafts survive edits and rejected weekday removal stays valid',
    async () => {
      let reported = null
      const wrapper = Vue.defineComponent({
        setup: () => {
          const { summary } = draftModule.providePolicyDraftCollector()
          Vue.watch(summary, (s) => (reported = s), { immediate: true })
          const val = Vue.ref(model.newTimeWindowCondition())
          return () =>
            Vue.h(registry['PolicyTimeWindowLeaf.vue'], {
              condition: val.value,
              onUpdate: (next) => (val.value = next),
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

      const startInputs = () =>
        findAll(editor.root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.from)
      setValue(startInputs()[0], 'garbage')
      await Vue.nextTick()
      assert.equal(reported.valid, false)

      click(
        find(
          editor.root,
          (n) => n.tag === 'ui-mock' && texts(n).includes(zhCN.timeWindow.addRange),
        ),
      )
      await Vue.nextTick()
      assert.equal(startInputs().length, 2)
      assert.equal(startInputs()[0].props.modelValue, 'garbage')
      assert.equal(startInputs()[1].props.modelValue, '09:00')

      click(
        findAll(
          editor.root,
          (n) => n.tag === 'ui-mock' && n.props.label === zhCN.timeWindow.removeRange,
        )[0],
      )
      await Vue.nextTick()
      assert.equal(startInputs().length, 1)
      assert.equal(startInputs()[0].props.modelValue, '09:00')
      assert.equal(reported.valid, true)
    },
  ],

  [
    'rule card removal is immediate when enabled and suppressed when disabled',
    async () => {
      const rule = model.readVisualDocument(configWith([flatRule()])).document.rules[0]
      for (const [disabled, want] of [
        [false, 1],
        [true, 0],
      ]) {
        let removed = 0
        const { root } = mountComponent(registry['PolicyRuleCard.vue'], {
          rule,
          index: 0,
          total: 1,
          disabled,
          onRemove: () => removed++,
        })
        click(uiNode(root, zhCN.rule.remove))
        await Vue.nextTick()
        assert.equal(removed, want, `disabled=${disabled}`)
      }
    },
  ],

  [
    'local action draft follows its rule across a reorder',
    async () => {
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: twoPricing })
      const factors = findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor)
      assert.equal(factors.length, 2)
      setValue(factors[0], 'not-a-number')
      await Vue.nextTick()
      click(findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.moveDown)[0])
      await Vue.nextTick()
      const drafts = findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor).map(
        (n) => n.props.modelValue,
      )
      assert.deepEqual(drafts, ['3', 'not-a-number'])
    },
  ],

  [
    'duplicate inserts a disabled copy with a fresh id',
    async () => {
      const { state, wrapper } = stateful(
        registry['PolicyEditor.vue'],
        twoPricing,
        'modelValue',
        'onUpdate:modelValue',
      )
      const { root } = mountComponent(wrapper)
      click(uiNode(root, zhCN.rule.duplicate))
      await Vue.nextTick()
      const result = model.readVisualDocument(state.value).document
      assert.equal(result.rules.length, 3)
      const copy = result.rules.find((rule) => rule.id === 'rule-1')
      assert.equal(copy.enabled, false)
      assert.equal(copy.name, 'first')
    },
  ],

  [
    'budget prevalidation disables add and duplicate at the rule limit',
    async () => {
      const hundredRules = configWith(
        Array.from({ length: model.policyLimits.maxRulesPerConfig }, (_, i) =>
          flatRule({ id: `r-${i}`, name: `Rule ${i}` }),
        ),
      )
      const { root } = mountComponent(registry['PolicyEditor.vue'], { modelValue: hundredRules })
      const addBtn = find(root, (n) => n.tag === 'ui-mock' && texts(n).includes(zhCN.addRule))
      assert.ok(addBtn)
      assert.equal(addBtn.props.disabled, true)
      const copyBtns = findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.duplicate)
      assert.equal(copyBtns.length, model.policyLimits.maxRulesPerConfig)
      assert.equal(copyBtns[0].props.disabled, true)
      assert.equal(
        findAll(root, (n) => n.tag === 'ui-mock' && n.props.label === zhCN.rule.name).length,
        model.policyLimits.maxRulesPerConfig,
      )
    },
  ],

  [
    'visual draft status aggregates invalid child inputs and keeps the original text',
    async () => {
      let draftStatus = null
      const { state, wrapper } = stateful(
        registry['PolicyEditor.vue'],
        twoPricing,
        'modelValue',
        'onUpdate:modelValue',
        { onDraftStatus: (status) => (draftStatus = status) },
      )
      const { root } = mountComponent(wrapper)
      await Vue.nextTick()
      assert.equal(draftStatus.valid, true)

      const factorField = findAll(
        root,
        (n) => n.tag === 'ui-mock' && n.props.label === zhCN.action.factor,
      )[0]
      setValue(factorField, 'not-a-number')
      await Vue.nextTick()
      assert.equal(draftStatus.valid, false)
      assert.equal(draftStatus.pending, true)
      assert.equal(state.value, twoPricing)
    },
  ],

  [
    'test host unbinds detached event handlers',
    () => {
      const rule = model.readVisualDocument(configWith([flatRule()])).document.rules[0]
      const { host } = mountComponent(registry['PolicyRuleCard.vue'], {
        rule,
        index: 0,
        total: 1,
      })
      const el = host.createElement('div')
      let fired = 0
      host.patchProp(el, 'onClick', null, () => fired++)
      assert.equal(typeof el.events.onClick, 'function')
      el.events.onClick()
      assert.equal(fired, 1)
      host.patchProp(el, 'onClick', el.events.onClick, null)
      assert.equal(el.events.onClick, undefined)
    },
  ],
]

for (const [name, run] of cases) test(name, run)
