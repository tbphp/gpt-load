// 扁平策略模型的回归：原生 JSON 解析、扁平结构识别、只读回退与编辑操作。
// 运行：node --test web/src/frontends/modern/features/groups/policy-editor/policy-model.test.mjs
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  documentToJson,
  duplicateRule,
  emptyDocument,
  formatConfigText,
  formatQuotaWindow,
  groupPolicyMode,
  hasFatalIssue,
  isMultiplierRaw,
  isRatio,
  isTimeOfDay,
  moveRule,
  newFactCondition,
  newRule,
  newRuleId,
  newTimeWindowCondition,
  parseConfigObject,
  policyLimits,
  quotaWindowFromToken,
  quotaWindowToken,
  readVisualDocument,
  serializeDocument,
  setGroupPolicyMode,
} from './policy-model.ts'

// 普通合法规则 fixture：只显式传变化字段。
const flatRule = (changes = {}) => ({
  id: 'r',
  name: 'r',
  domain: 'scheduling',
  enabled: false,
  when: { all: [{ fact: 'request.model', op: 'eq', value: 'x' }] },
  actions: [{ type: 'exclude_candidate' }],
  ...changes,
})
const configWith = (rules) => JSON.stringify({ schema_version: 1, rules })

test('blank text is fatal-empty, malformed JSON is a syntax error', () => {
  for (const blank of ['', '   \n\t ']) {
    const result = readVisualDocument(blank)
    assert.equal(result.document, undefined)
    assert.equal(result.issues[0]?.code, 'empty')
    assert.equal(hasFatalIssue(result.issues), true)
  }
  const broken = readVisualDocument('{"schema_version":1,"rules":[')
  assert.equal(broken.document, undefined)
  assert.equal(broken.issues[0]?.code, 'syntax')
})

test('non-object root, wrong schema and non-array rules are fatal', () => {
  assert.equal(readVisualDocument('[]').issues[0]?.code, 'rootType')
  assert.equal(readVisualDocument('123').issues[0]?.code, 'rootType')
  assert.equal(readVisualDocument('{"schema_version":2,"rules":[]}').issues[0]?.code, 'schemaVersion')
  assert.equal(readVisualDocument('{"schema_version":1}').issues[0]?.code, 'rulesType')
})

test('rule count and config bytes are bounded before rendering', () => {
  const tooMany = configWith(
    Array.from({ length: policyLimits.maxRulesPerConfig + 1 }, (_, i) => flatRule({ id: `r${i}` })),
  )
  const counted = readVisualDocument(tooMany)
  assert.ok(counted.issues.some((issue) => issue.code === 'ruleCount' && issue.fatal))
  assert.equal(counted.document, undefined)

  const huge = `{"schema_version":1,"rules":[],"note":"${'a'.repeat(policyLimits.maxConfigBytes)}"}`
  assert.ok(readVisualDocument(huge).issues.some((issue) => issue.code === 'configBytes' && issue.fatal))
})

test('a flat configuration parses into plain VisualRule objects', () => {
  const result = readVisualDocument(configWith([flatRule()]))
  assert.equal(hasFatalIssue(result.issues), false)
  assert.deepEqual(result.document, {
    groupPolicy: 'inherit',
    rules: [
      {
        id: 'r',
        name: 'r',
        domain: 'scheduling',
        enabled: false,
        match: 'all',
        conditions: [{ kind: 'param', fact: 'request.model', op: 'eq', value: 'x' }],
        actions: [{ type: 'exclude_candidate' }],
      },
    ],
  })
})

test('quota conditions keep the canonical fact plus windowSeconds', () => {
  const result = readVisualDocument(
    configWith([
      flatRule({
        when: {
          any: [
            {
              fact: 'credential.quota.remaining_ratio',
              select: { scope: 'account', window_seconds: 18000 },
              reduce: 'min',
              op: 'lt',
              value: 0.25,
            },
          ],
        },
      }),
    ]),
  )
  assert.equal(hasFatalIssue(result.issues), false)
  assert.deepEqual(result.document.rules[0].match, 'any')
  assert.deepEqual(result.document.rules[0].conditions[0], {
    kind: 'param',
    fact: 'credential.quota.remaining_ratio',
    op: 'lt',
    value: 0.25,
    windowSeconds: 18000,
  })
})

test('time_window and expression conditions are first-class flat conditions', () => {
  const result = readVisualDocument(
    configWith([
      flatRule({
        when: {
          all: [
            { predicate: 'time_window', weekdays: [1, 2], ranges: [['09:00', '18:00']] },
            { expression: 'request.model == "gpt-4o"' },
          ],
        },
      }),
    ]),
  )
  assert.equal(hasFatalIssue(result.issues), false)
  assert.deepEqual(result.document.rules[0].conditions, [
    { kind: 'time_window', weekdays: [1, 2], ranges: [['09:00', '18:00']] },
    { kind: 'expression', expression: 'request.model == "gpt-4o"' },
  ])
})

test('nested or unknown structures fall back to read-only advanced text', () => {
  const nested = configWith([
    flatRule({ when: { all: [{ all: [{ fact: 'request.model', op: 'eq', value: 'x' }] }] } }),
  ])
  const result = readVisualDocument(nested)
  assert.equal(result.document, undefined)
  assert.equal(result.advancedText, nested)
  assert.deepEqual(result.issues, [{ code: 'advanced', path: '', fatal: false }])
  assert.equal(hasFatalIssue(result.issues), false)

  for (const raw of [
    configWith([flatRule({ when: { not: { fact: 'request.model', op: 'eq', value: 'x' } } })]),
    configWith([flatRule({ extra: 1 })]),
    configWith([flatRule({ when: { all: [{ fact: 'future.fact', op: 'eq', value: 'x' }] } })]),
    configWith([flatRule({ when: { all: [{ fact: 'request.model', op: 'contains', value: 'x' }] } })]),
    configWith([flatRule({ actions: [{ type: 'future_action' }] })]),
    '{"schema_version":1,"rules":[],"unknown":1}',
  ]) {
    assert.equal(readVisualDocument(raw).document, undefined, raw)
    assert.equal(readVisualDocument(raw).issues[0]?.code, 'advanced', raw)
  }
})

test('serializeDocument round-trips the flat model into native JSON', () => {
  const document = {
    groupPolicy: 'override',
    rules: [
      {
        id: 'q',
        name: 'Q',
        domain: 'pricing',
        enabled: true,
        match: 'all',
        conditions: [
          { kind: 'param', fact: 'request.model', op: 'in', value: ['a', 'b'] },
          { kind: 'param', fact: 'credential.quota.remaining_ratio', op: 'lt', value: 0.1, windowSeconds: 3600 },
          { kind: 'time_window', weekdays: [1], ranges: [['09:00', '18:00']] },
          { kind: 'expression', expression: 'true' },
        ],
        actions: [
          { type: 'multiply_price', factor: '1.5' },
          { type: 'exclude_models', models: ['m1'] },
        ],
      },
    ],
  }
  const text = serializeDocument(document)
  assert.equal(JSON.parse(text).group_policy, 'override')
  const reparsed = readVisualDocument(text)
  assert.equal(hasFatalIssue(reparsed.issues), false)
  assert.deepEqual(reparsed.document, document)
  assert.deepEqual(documentToJson(document).schema_version, 1)
})

test('new rules default disabled with unique ids and domain-correct actions', () => {
  assert.equal(newRuleId(['rule-1']), 'rule-2')
  const scheduling = newRule('rule-1', 'scheduling')
  assert.equal(scheduling.enabled, false)
  assert.equal(scheduling.name, 'rule-1')
  assert.equal(scheduling.match, 'all')
  assert.deepEqual(scheduling.conditions, [])
  assert.deepEqual(scheduling.actions, [{ type: 'exclude_candidate' }])
  const pricing = newRule('rule-1', 'pricing')
  assert.deepEqual(pricing.actions, [{ type: 'multiply_price', factor: '1' }])
  assert.deepEqual(emptyDocument(), { groupPolicy: 'inherit', rules: [] })
})

test('duplicate disables with a fresh id; moveRule reorders explicitly', () => {
  const base = newRule('a', 'scheduling')
  const copy = duplicateRule(base, ['a'])
  assert.equal(copy.id, 'rule-1')
  assert.equal(copy.enabled, false)
  const rules = [newRule('a', 'scheduling'), newRule('b', 'scheduling'), newRule('c', 'scheduling')]
  assert.deepEqual(moveRule(rules, 2, 0).map((rule) => rule.id), ['c', 'a', 'b'])
  assert.equal(moveRule(rules, 5, 0), rules)
})

test('new conditions default to compilable shapes', () => {
  assert.deepEqual(newFactCondition('request.model'), {
    kind: 'param',
    fact: 'request.model',
    op: 'eq',
    value: '',
  })
  assert.deepEqual(newFactCondition('credential.quota.remaining_ratio', 18000), {
    kind: 'param',
    fact: 'credential.quota.remaining_ratio',
    op: 'lt',
    value: 0.1,
    windowSeconds: 18000,
  })
  assert.deepEqual(newTimeWindowCondition(), {
    kind: 'time_window',
    weekdays: [1],
    ranges: [['09:00', '18:00']],
  })
})

test('group_policy and native JSON helpers keep only flat intent', () => {
  assert.equal(groupPolicyMode(parseConfigObject('{"schema_version":1,"rules":[]}')), 'inherit')
  assert.equal(groupPolicyMode(parseConfigObject('{"group_policy":"override"}')), 'override')
  const next = setGroupPolicyMode({ schema_version: 1, rules: [] }, 'override')
  assert.equal(next.group_policy, 'override')
  assert.equal(parseConfigObject('nope'), undefined)
  assert.equal(parseConfigObject(''), undefined)
  assert.equal(formatConfigText('{"a":1}'), '{\n  "a": 1\n}')
})

test('numeric and window helpers mirror the backend contract', () => {
  for (const ok of ['0', '2', '0.5', '0999.999999', '1000.000000']) {
    assert.equal(isMultiplierRaw(ok), true, ok)
  }
  for (const bad of ['1e-6', '0.0000001', '1000.000001', '-0', '+1', '1.', '.5', '1.0000000']) {
    assert.equal(isMultiplierRaw(bad), false, bad)
  }
  assert.equal(isRatio(0.1), true)
  assert.equal(isRatio(1), true)
  assert.equal(isRatio(1.5), false)
  assert.equal(isRatio(-0.1), false)
  assert.equal(isTimeOfDay('09:00'), true)
  assert.equal(isTimeOfDay('24:00'), false)
  assert.equal(quotaWindowToken('18000'), 'quota:18000')
  assert.equal(quotaWindowFromToken('quota:18000'), '18000')
  assert.equal(quotaWindowFromToken('quota:0'), undefined)
  assert.equal(formatQuotaWindow('18000'), '5h')
  assert.equal(formatQuotaWindow('90000'), '25h')
})
