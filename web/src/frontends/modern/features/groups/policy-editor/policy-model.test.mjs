// 纯逻辑层的最小回归：原始数值保留、有界检查、JSON-only 保留与编辑操作。
// 运行：node --test web/src/frontends/modern/features/groups/policy-editor/policy-model.test.mjs
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  actionRepresentable,
  arrayItems,
  conditionKind,
  duplicateRule,
  getField,
  hasFatalIssue,
  insertRule,
  isMultiplierRaw,
  isRatioRaw,
  literalNumberRaw,
  literalString,
  moveRule,
  newRule,
  newRuleId,
  newTimeWindowCondition,
  objectNode,
  parseRawJson,
  policyLimits,
  readVisualDocument,
  removeRuleAt,
  serializeJson,
  stringLiteral,
  unsupportedPaths,
} from './policy-model.ts'

test('raw JSON literals are preserved byte-for-byte', () => {
  const text =
    '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,' +
    '"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},' +
    '"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}'
  const root = parseRawJson(text)
  assert.equal(serializeJson(root), text)
  const rule = readVisualDocument(text).rules[0]
  const value = getField(getField(getField(rule.node, 'when'), 'value'), 'x')
  // quota value is a number literal, raw retained without Number conversion
  const quotaValue = getField(rule.when, 'value')
  assert.equal(literalNumberRaw(quotaValue), '0.10000000000000001')
  assert.equal(value, undefined)
})

test('a longer decimal does not lose digits through the editor', () => {
  const text =
    '{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":false,' +
    '"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":1},' +
    '"reduce":"min","op":"gt","value":0.12345678901234567890123456789},' +
    '"then":{"type":"exclude_candidate"}}]}'
  const result = readVisualDocument(text)
  assert.equal(hasFatalIssue(result.issues), false)
  assert.equal(serializeJson(result.root), text)
})

test('multiplier bounds mirror backend', () => {
  for (const ok of ['0', '2', '0.5', '0999.999999', '1000.000000']) {
    assert.equal(isMultiplierRaw(ok), true, ok)
  }
  for (const bad of ['1e-6', '0.0000001', '1000.000001', '-0', '+1', '1.', '.5', '1.0000000']) {
    assert.equal(isMultiplierRaw(bad), false, bad)
  }
})

test('ratio raw accepts JSON numbers and flags out-of-range plain decimals', () => {
  assert.equal(isRatioRaw('0.1'), true)
  assert.equal(isRatioRaw('1'), true)
  assert.equal(isRatioRaw('0.99999999999999999'), true)
  assert.equal(isRatioRaw('1.5'), false) // 超出 0..1 由 invalidValue 提示，后端再判定
  assert.equal(isRatioRaw('-0.1'), false)
  assert.equal(isRatioRaw('abc'), false)
  // 指数形式不在前端声称为可无损表示，交后端判定并显示为待修正。
  assert.equal(isRatioRaw('1e-1'), false)
  assert.equal(isRatioRaw('1e999'), false)
})

test('JSON nesting is hard-bounded before any traversal', () => {
  const deep = `${'['.repeat(policyLimits.maxJSONDepth + 5)}1${']'.repeat(policyLimits.maxJSONDepth + 5)}`
  const result = readVisualDocument(deep)
  assert.equal(result.root, undefined)
  assert.equal(result.issues[0]?.code, 'syntax')
  assert.equal(hasFatalIssue(result.issues), true)
  assert.equal(parseRawJsonSafe(deep), false)
})

function parseRawJsonSafe(text) {
  try {
    parseRawJson(text)
    return true
  } catch {
    return false
  }
}

test('malformed leaf structures are represented as JSON-only, never coerced', () => {
  const mixedIn = {
    all: [
      {
        fact: 'request.model',
        op: 'in',
        value: [
          { type: 'literal', literal: 'string', raw: '"a"' },
          { type: 'literal', literal: 'number', raw: '123' },
          objectNode([{ key: 'unknown', value: { type: 'literal', literal: 'number', raw: '1' } }]),
        ],
      },
    ],
  }
  assert.equal(conditionKind(mixedIn.all[0]), 'unsupported')
  // 字符串参数只支持 eq，导入集合操作必须落回 JSON-only。
  assert.equal(
    conditionKind(
      objectNode([
        { key: 'fact', value: stringLiteral('request.model') },
        { key: 'op', value: stringLiteral('in') },
        { key: 'value', value: stringLiteral('x') },
      ]),
    ),
    'unsupported',
  )

  const badDays = objectNode([
    { key: 'predicate', value: stringLiteral('time_window') },
    {
      key: 'weekdays',
      value: {
        type: 'array',
        items: [
          { type: 'literal', literal: 'number', raw: '1' },
          { type: 'literal', literal: 'number', raw: '9' },
        ],
      },
    },
    {
      key: 'ranges',
      value: {
        type: 'array',
        items: [{ type: 'array', items: [stringLiteral('09:00'), stringLiteral('18:00')] }],
      },
    },
  ])
  assert.equal(conditionKind(badDays), 'unsupported')

  const badRanges = objectNode([
    { key: 'predicate', value: stringLiteral('time_window') },
    {
      key: 'weekdays',
      value: { type: 'array', items: [{ type: 'literal', literal: 'number', raw: '1' }] },
    },
    {
      key: 'ranges',
      value: { type: 'array', items: [{ type: 'literal', literal: 'number', raw: '123' }] },
    },
  ])
  assert.equal(conditionKind(badRanges), 'unsupported')
})

test('unknown actions are JSON-only, not implicitly converted', () => {
  const futureAction = objectNode([
    { key: 'type', value: stringLiteral('future_action') },
    { key: 'factor', value: { type: 'literal', literal: 'number', raw: '2' } },
  ])
  assert.equal(actionRepresentable(futureAction, 'pricing'), false)
  const pricing = objectNode([
    { key: 'type', value: stringLiteral('multiply_price') },
    { key: 'factor', value: stringLiteral('2') },
  ])
  assert.equal(actionRepresentable(pricing, 'pricing'), true)
  assert.equal(actionRepresentable(pricing, 'scheduling'), false)
  const scheduling = objectNode([{ key: 'type', value: stringLiteral('exclude_candidate') }])
  assert.equal(actionRepresentable(scheduling, 'scheduling'), true)
})

test('a non-object rule is preserved without shifting visual indices', () => {
  const text = JSON.stringify({
    schema_version: 1,
    rules: [
      123,
      {
        id: 'valid',
        name: 'valid',
        domain: 'scheduling',
        enabled: false,
        when: newTimeWindowCondition(),
        then: { type: 'exclude_candidate' },
      },
    ],
  })
  const result = readVisualDocument(text)
  assert.equal(result.rules.length, 1)
  assert.equal(result.rules[0].index, 1)
  assert.equal(result.rules[0].id, 'valid')
  assert.equal(serializeJson(result.root), text)
})

test('blank text is not deletion and is fatal-empty', () => {
  for (const blank of ['', '   \n\t ']) {
    const result = readVisualDocument(blank)
    assert.equal(result.root, undefined)
    assert.equal(result.issues[0]?.code, 'empty')
    assert.equal(hasFatalIssue(result.issues), true)
  }
})

test('rule/node/depth budgets are enforced before recursive render', () => {
  const tooManyRules = JSON.stringify({
    schema_version: 1,
    rules: Array.from({ length: policyLimits.maxRulesPerConfig + 1 }, (_, i) => ({
      id: `r${i}`,
      name: 'r',
      domain: 'scheduling',
      enabled: false,
      when: { fact: 'request.model', op: 'eq', value: 'x' },
      then: { type: 'exclude_candidate' },
    })),
  })
  assert.ok(readVisualDocument(tooManyRules).issues.some((i) => i.code === 'ruleCount' && i.fatal))

  const deep = (depth) => {
    let node = { fact: 'request.model', op: 'eq', value: 'x' }
    for (let i = 0; i < depth; i++) node = { all: [node] }
    return node
  }
  const deepText = JSON.stringify({
    schema_version: 1,
    rules: [
      {
        id: 'd',
        name: 'd',
        domain: 'scheduling',
        enabled: false,
        when: deep(policyLimits.maxConditionDepth),
        then: { type: 'exclude_candidate' },
      },
    ],
  })
  assert.ok(readVisualDocument(deepText).issues.some((i) => i.code === 'conditionDepth' && i.fatal))
  // 深度恰好用满（叶子在 depth 16）时必须仍然可编译，证明上限判定没有提前一位。
  const boundaryText = JSON.stringify({
    schema_version: 1,
    rules: [
      {
        id: 'b',
        name: 'b',
        domain: 'scheduling',
        enabled: false,
        when: deep(policyLimits.maxConditionDepth - 1),
        then: { type: 'exclude_candidate' },
      },
    ],
  })
  assert.equal(hasFatalIssue(readVisualDocument(boundaryText).issues), false)

  const wideLeaf = { fact: 'request.model', op: 'eq', value: 'x' }
  const wide = { all: Array.from({ length: policyLimits.maxNodesPerRule + 1 }, () => wideLeaf) }
  const wideText = JSON.stringify({
    schema_version: 1,
    rules: [
      {
        id: 'w',
        name: 'w',
        domain: 'scheduling',
        enabled: false,
        when: wide,
        then: { type: 'exclude_candidate' },
      },
    ],
  })
  assert.ok(readVisualDocument(wideText).issues.some((i) => i.code === 'nodePerRule' && i.fatal))
})

test('supported-but-not-visualizable nodes are reported and never discarded', () => {
  const text = JSON.stringify({
    schema_version: 1,
    rules: [
      {
        id: 's',
        name: 's',
        domain: 'scheduling',
        enabled: false,
        when: {
          all: [
            { fact: 'request.model', op: 'eq', value: 'gpt-4o' },
            { fact: 'future.unknown_fact', op: 'eq', value: 'x' },
          ],
        },
        then: { type: 'exclude_candidate' },
      },
    ],
  })
  const result = readVisualDocument(text)
  assert.equal(hasFatalIssue(result.issues), false)
  assert.deepEqual(unsupportedPaths(result.issues), ['rules[0].when.all[1]'])
  assert.equal(serializeJson(result.root), text)
  assert.equal(conditionKind({ all: [] }), 'unsupported')
})

test('new rules default disabled, unique id, domain-correct action', () => {
  assert.equal(newRuleId(['rule-1']), 'rule-2')
  const scheduling = newRule('rule-1', 'scheduling')
  assert.equal(getField(scheduling, 'enabled')?.raw, 'false')
  assert.equal(literalString(getField(scheduling, 'name')), 'rule-1')
  // 新规则不含预设条件，等待用户显式添加。
  assert.equal(conditionKind(getField(scheduling, 'when')), 'all')
  assert.equal(arrayItems(getField(getField(scheduling, 'when'), 'all'))?.length, 0)
  assert.equal(getField(getField(scheduling, 'then'), 'type')?.raw, '"exclude_candidate"')
  const pricing = newRule('rule-1', 'pricing')
  assert.equal(getField(getField(pricing, 'then'), 'type')?.raw, '"multiply_price"')
  assert.equal(getField(getField(pricing, 'then'), 'factor')?.raw, '"1"')
})

test('duplicate assigns new id and disables; ordering and deletion are explicit', () => {
  const base = newRule('a', 'scheduling')
  const root = objectNode([
    { key: 'schema_version', value: { type: 'literal', literal: 'number', raw: '1' } },
    { key: 'rules', value: { type: 'array', items: [base] } },
  ])
  const result = readVisualDocument(serializeJson(root))
  const rule = result.rules[0]
  assert.ok(rule)
  const copy = duplicateRule(rule, ['a'])
  assert.ok(copy)
  assert.equal(literalString(getField(copy, 'id')), 'rule-1')
  assert.equal(getField(copy, 'enabled')?.raw, 'false')

  assert.ok(result.root)
  const inserted = insertRule(result.root, copy)
  const reordered = moveRule(inserted, 1, 0)
  const first = getField(reordered, 'rules')?.items?.[0]
  assert.equal(literalString(getField(first, 'id')), 'rule-1')
  const deleted = removeRuleAt(inserted, 0)
  assert.equal(getField(deleted, 'rules')?.items?.length, 1)
})
