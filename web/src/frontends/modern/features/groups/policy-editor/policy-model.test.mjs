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
  groupPolicyMode,
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
  const quotaValue = getField(getField(rule.node, 'when'), 'value')
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
  for (const raw of [
    '{"fact":"request.model","op":"in","value":["a",123,{"unknown":1}]}',
    '{"fact":"request.model","op":"in","value":"x"}',
    '{"predicate":"time_window","weekdays":[1,9],"ranges":[["09:00","18:00"]]}',
    '{"predicate":"time_window","weekdays":[1],"ranges":[123]}',
  ]) {
    assert.equal(conditionKind(parseRawJson(raw)), 'unsupported', raw)
  }
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
  assert.equal(literalString(getField(result.rules[0].node, 'id')), 'valid')
  assert.equal(serializeJson(result.root), text)
})

test('group_policy mode defaults to inherit and survives rule edits', () => {
  const plain = readVisualDocument('{"schema_version":1,"rules":[]}')
  assert.equal(groupPolicyMode(plain.root), 'inherit')
  assert.equal(hasFatalIssue(plain.issues), false)

  const overrideText =
    '{"schema_version":1,"group_policy":"override","rules":[{"id":"a","name":"a","domain":"scheduling","enabled":false,' +
    '"when":{"predicate":"time_window","weekdays":[1],"ranges":[["09:00","18:00"]]},"then":{"type":"exclude_candidate"}}]}'
  const result = readVisualDocument(overrideText)
  assert.equal(groupPolicyMode(result.root), 'override')
  const inserted = insertRule(result.root, newRule('b', 'scheduling'))
  assert.equal(groupPolicyMode(inserted), 'override')
  const removed = removeRuleAt(inserted, 0)
  assert.equal(groupPolicyMode(removed), 'override')
  assert.equal(getField(removed, 'group_policy')?.raw, '"override"')

  // 未知取值只标记该字段不可可视化，不将整份配置判为 fatal。
  const unknown = readVisualDocument('{"schema_version":1,"group_policy":"weird","rules":[]}')
  assert.equal(hasFatalIssue(unknown.issues), false)
  assert.deepEqual(unsupportedPaths(unknown.issues), ['group_policy'])
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

test('string literals reject illegal escapes and raw control characters, keeping raw tokens', () => {
  // 合法转义必须逐字保留（含 \u 序列与转义斜杠），不做 decode/encode 往返。
  const valid = '{"schema_version":1,"rules":[],"note":"a\\u0041\\/b\\n\\t\\"c\\\\"}'
  const parsed = readVisualDocument(valid)
  assert.deepEqual(parsed.issues, [])
  assert.equal(getField(parsed.root, 'note')?.raw, '"a\\u0041\\/b\\n\\t\\"c\\\\"')
  assert.equal(serializeJson(parsed.root), valid)

  // 非法反斜杠转义与不完整 \u：后端 json.Decoder 拒绝，前端语法判断必须同样拒绝，
  // 否则编辑器会显示“有效”并原样提交给服务端。
  for (const bad of [
    '{"schema_version":1,"rules":[],"note":"\\q"}',
    '{"schema_version":1,"rules":[],"note":"\\u12"}',
    '{"schema_version":1,"rules":[],"note":"\\uZZZZ"}',
    '{"schema_version":1,"rules":[],"note":"dangling\\"}',
  ]) {
    assert.equal(parseRawJsonSafe(bad), false, bad)
    const result = readVisualDocument(bad)
    assert.equal(result.root, undefined, bad)
    assert.equal(result.issues[0]?.code, 'syntax', bad)
  }

  // 裸控制字符（未转义换行）不是合法 JSON 字符串。
  const bareNewline = '{"schema_version":1,"rules":[],"note":"line1\nline2"}'
  assert.equal(parseRawJsonSafe(bareNewline), false)
  assert.equal(readVisualDocument(bareNewline).issues[0]?.code, 'syntax')
})

test('decoded duplicate object keys are rejected, including escaped spellings', () => {
  const duplicatedThen =
    '{"schema_version":1,"rules":[{"id":"a","name":"a","domain":"scheduling","enabled":false,' +
    '"when":{"fact":"request.model","op":"eq","value":"x"},' +
    '"then":{"type":"exclude_candidate"},"then":{"type":"exclude_candidate"}}]}'
  for (const bad of [
    '{"schema_version":1,"rules":[],"a":1,"a":2}',
    '{"schema_version":1,"rules":[],"a":1,"\\u0061":2}',
    duplicatedThen,
    '{"schema_version":1,"rules":[],"\\ud800":1,"\\ufffd":2}',
    '{"schema_version":1,"rules":[],"\\ud800":1,"\\ud801":2}',
    '{"schema_version":1,"rules":[],"\\udc00":1,"\\ufffd":2}',
  ]) {
    assert.equal(parseRawJsonSafe(bad), false, bad)
    const result = readVisualDocument(bad)
    assert.equal(result.root, undefined, bad)
    assert.equal(result.issues[0]?.code, 'syntax', bad)
  }
  // 大小写不同的键是不同字段，不能误判为重复。
  const distinct = readVisualDocument('{"schema_version":1,"rules":[],"a":1,"A":2}')
  assert.equal(hasFatalIssue(distinct.issues), false)
})

test('weekday literals use raw Go integer syntax, never Number rounding', () => {
  const windowText = (days) =>
    '{"schema_version":1,"rules":[{"id":"w","name":"w","domain":"scheduling","enabled":false,' +
    `"when":{"predicate":"time_window","weekdays":[${days}],"ranges":[["09:00","18:00"]]},` +
    '"then":{"type":"exclude_candidate"}}]}'

  // 合法 JSON 整数语法仍可视化（-0 按 Go strconv.Atoi 语义等于 0）。
  const ok = readVisualDocument(windowText('1,5'))
  assert.equal(hasFatalIssue(ok.issues), false)
  assert.equal(conditionKind(getField(ok.rules[0].node, 'when')), 'time_window')
  assert.deepEqual(unsupportedPaths(ok.issues), [])
  const negativeZero = readVisualDocument(windowText('-0'))
  assert.equal(conditionKind(getField(negativeZero.rules[0].node, 'when')), 'time_window')

  // 1.0 / 1e0 / 高精度小数是后端拒绝的整数语法；不能被 Number 舍入成合法星期后视觉改写。
  for (const bad of ['1.0', '1e0', '0.99999999999999999999', '1.0000000000000000001']) {
    const text = windowText(bad)
    const result = readVisualDocument(text)
    assert.equal(hasFatalIssue(result.issues), false, bad)
    assert.equal(conditionKind(getField(result.rules[0].node, 'when')), 'unsupported', bad)
    assert.deepEqual(unsupportedPaths(result.issues), ['rules[0].when'], bad)
    // 原始 token 不被改写，节点按 JSON-only 保真保留。
    const days = arrayItems(getField(getField(result.rules[0].node, 'when'), 'weekdays'))
    assert.equal(literalNumberRaw(days?.[0]), bad)
    assert.equal(serializeJson(result.root), text, bad)
  }
})
