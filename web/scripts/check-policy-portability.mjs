// 可移植策略 JSON 操作的实际模块检查。
// 复用既有 TS transpile + vm 模式，不读取临时路径，不依赖浏览器。
import { loadTsModule, readSource, stripImports } from './policy-test-utils.mjs'

const MODULE_PATH = 'web/src/frontends/modern/features/groups/policy-portability.ts'

const pp = loadTsModule(
  stripImports(readSource(MODULE_PATH)) +
    '\n;globalThis.__PolicyPortabilityError = PolicyPortabilityError;',
  {
    TextEncoder,
    // 默认 ID 工厂依赖既有 @shared/uuid 约定；测试只用确定性工厂，仍提供桩。
    createUUID: () => 'generated-uuid-0000-0000-0000-000000000000',
  },
)

let failed = 0
function test(name, fn) {
  try {
    fn()
    console.log('PASS', name)
  } catch (error) {
    failed++
    console.log('FAIL', name, error && error.message ? error.message : String(error))
  }
}

function assert(condition, message) {
  if (!condition) throw new Error(message)
}

function issuesOf(raw) {
  return pp.validatePortablePolicy(raw)
}

function issueCodes(raw) {
  return issuesOf(raw).map((issue) => issue.code)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------
const RAW_LITERAL = '0.10000000000000001'

const schedulingRule = (id, enabled = true, value = RAW_LITERAL) =>
  `{"id":${JSON.stringify(id)},"name":"Rule ${id}","domain":"scheduling","enabled":${enabled},"when":{"all":[{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":${value}}]},"then":{"type":"exclude_candidate"}}`
const pricingRule = (id, enabled = true, factor = '2') =>
  `{"id":${JSON.stringify(id)},"name":"Rule ${id}","domain":"pricing","enabled":${enabled},"when":{"predicate":"time_window","weekdays":[1,2],"ranges":[["09:00","12:00"]]},"then":{"type":"multiply_price","factor":${JSON.stringify(factor)}}}`

const valid = `{"schema_version":1,"rules":[${schedulingRule('protect-astra')},${pricingRule('working-hours-price')}]}`
const withExtras = `{"schema_version":1,"group_id":7,"credential_id":9,"observation":{"remaining_ratio":0.5},"secret":"token-value","rules":[${schedulingRule('protect-astra')}]}`

// Deterministic ID factory for tests (matches ^[A-Za-z0-9_-]+$ and <=64 chars).
function sequentialID(existing, preferred) {
  for (let n = 1; n < 1000; n++) {
    const candidate = `${preferred}-copy-${n}`
    if (!existing.includes(candidate)) return candidate
  }
  throw new Error('sequentialID exhausted')
}

// A scheduling rule whose `when` is a compiler-compatible tree with exactly `nodes` nodes.
// Each list stays within the backend MaxListItems (100) so the fixture is valid for the real
// compiler; the reviewer's list-255 fixture is intentionally rejected by the backend.
function treeRule(id, nodes) {
  if (nodes < 2) throw new Error('treeRule requires at least 2 nodes')
  let remaining = nodes - 1
  const groups = []
  let counter = 0
  while (remaining > 0) {
    const leaves = Math.min(100, remaining - 1)
    groups.push(
      `{"all":[${Array.from(
        { length: leaves },
        () => `{"fact":"upstream.model","op":"eq","value":"m${counter++}"}`,
      ).join(',')}]}`,
    )
    remaining -= 1 + leaves
  }
  return `{"id":${JSON.stringify(id)},"name":"Rule ${id}","domain":"scheduling","enabled":true,"when":{"all":[${groups.join(',')}]},"then":{"type":"exclude_candidate"}}`
}

// ---------------------------------------------------------------------------
// 1. scanPolicyConfig exposes exact raw spans
// ---------------------------------------------------------------------------
test('scan returns exact rule spans preserving numeric literal', () => {
  const scanned = pp.scanPolicyConfig(valid)
  assert(scanned.schemaVersionRaw === '1', 'schemaVersionRaw mismatch')
  assert(scanned.rules.length === 2, 'rule count mismatch')
  const first = scanned.rules[0]
  assert(valid.slice(first.start, first.end) === first.raw, 'raw slice mismatch')
  assert(first.raw.includes(RAW_LITERAL), 'raw literal missing from slice')
  assert(first.id === 'protect-astra', 'id mismatch')
  assert(first.domain === 'scheduling', 'domain mismatch')
  assert(first.enabled === true, 'enabled mismatch')
  assert(scanned.rules[1].domain === 'pricing', 'second domain mismatch')
})

// ---------------------------------------------------------------------------
// 2. validation rejects duplicate/canonical/schema/budget errors
// ---------------------------------------------------------------------------
test('blank config is invalid', () => {
  assert(issuesOf('').length > 0, 'empty accepted')
  assert(issuesOf('   \n\t ').length > 0, 'whitespace accepted')
})

test('blank text is never treated as an explicit clear', () => {
  // blank stays invalid and never produces a portable empty ruleset
  for (const blank of ['', ' \n ', '\t']) {
    assert(issuesOf(blank).length > 0, `blank ${JSON.stringify(blank)} accepted`)
    let threw = false
    try {
      pp.exportPortablePolicy(blank)
    } catch (error) {
      threw = error instanceof pp.__PolicyPortabilityError
    }
    assert(threw, `blank ${JSON.stringify(blank)} exported as a clear`)
  }
  for (const [name, call] of [
    ['copy', () => pp.copyPolicyRule('', 0)],
    ['import target', () => pp.importPortablePolicy('', valid, { domain: 'scheduling' })],
    ['import source', () => pp.importPortablePolicy(valid, '  ', { domain: 'scheduling' })],
  ]) {
    let threw = false
    try {
      call()
    } catch (error) {
      threw = error instanceof pp.__PolicyPortabilityError
    }
    assert(threw, `blank ${name} did not throw`)
  }
  // non-string input is a type error, never a clear
  let typed = false
  try {
    pp.exportPortablePolicy(null)
  } catch (error) {
    typed =
      error instanceof pp.__PolicyPortabilityError && error.issues[0].code === 'ERR_INVALID_TYPE'
  }
  assert(typed, 'non-string input accepted')
})

test('null in recognized envelope fields drops with warnings', () => {
  const raw = '{"schema_version":1,"rules":[],"credential_id":null,"observation":null}'
  const result = pp.exportPortablePolicy(raw)
  assert(result.text === '{"schema_version":1,"rules":[]}', `not clean: ${result.text}`)
  assert(result.warnings.length === 2, `warnings=${JSON.stringify(result.warnings)}`)
})

test('unknown null inside when/then is preserved raw', () => {
  const withNullWhen = `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":true,"when":{"future_condition":{"value":${RAW_LITERAL},"optional":null}},"then":{"type":"future_action","optional":null}}]}`
  assert(issuesOf(withNullWhen).length === 0, 'unknown nullable condition/action rejected')
  const results = [
    pp.exportPortablePolicy(withNullWhen),
    pp.copyPolicyRule(withNullWhen, 0, { newID: sequentialID }),
    pp.importPortablePolicy('{"schema_version":1,"rules":[]}', withNullWhen, {
      domain: 'scheduling',
      newID: sequentialID,
    }),
  ]
  for (const result of results) {
    assert(result.text.includes(RAW_LITERAL), 'numeric literal lost')
    assert(
      result.text.includes(`"when":{"future_condition":{"value":${RAW_LITERAL},"optional":null}}`),
      'unknown null in when not preserved',
    )
    assert(
      result.text.includes('"then":{"type":"future_action","optional":null}'),
      'unknown null in then not preserved',
    )
  }
})

test('null in required root/rule fields is rejected', () => {
  const cases = [
    ['{"schema_version":null,"rules":[]}', 'schema_version'],
    ['{"schema_version":1,"rules":null}', 'rules'],
    [
      `{"schema_version":1,"rules":[{"id":null,"name":"R","domain":"scheduling","enabled":true,"when":{},"then":{}}]}`,
      'rules[0].id',
    ],
    [
      `{"schema_version":1,"rules":[{"id":"r","name":null,"domain":"scheduling","enabled":true,"when":{},"then":{}}]}`,
      'rules[0].name',
    ],
    [
      `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":null,"enabled":true,"when":{},"then":{}}]}`,
      'rules[0].domain',
    ],
    [
      `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":null,"when":{},"then":{}}]}`,
      'rules[0].enabled',
    ],
    [
      `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":true,"when":null,"then":{}}]}`,
      'rules[0].when',
    ],
    [
      `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":true,"when":{},"then":null}]}`,
      'rules[0].then',
    ],
    ['null', ''],
  ]
  for (const [raw, path] of cases) {
    const issues = issuesOf(raw)
    assert(issues.length > 0, `null accepted at ${path}`)
    assert(issues[0].path === path, `path=${issues[0].path} expected ${path}`)
  }
})

test('unsupported schema_version rejected', () => {
  assert(issueCodes('{"schema_version":2,"rules":[]}').includes('ERR_INVALID_VALUE'), 'v2 accepted')
  assert(issuesOf('{"schema_version":1.0,"rules":[]}').length > 0, 'float 1.0 accepted')
  assert(issuesOf('{"schema_version":"1","rules":[]}').length > 0, 'string version accepted')
})

test('duplicate JSON fields rejected with backend code', () => {
  assert(
    issueCodes('{"schema_version":1,"schema_version":1,"rules":[]}').includes('ERR_DUPLICATE_KEY'),
    'duplicate field accepted',
  )
})

test('non-canonical root fields rejected by validation', () => {
  const issues = issuesOf('{"schema_version":1,"rules":[],"extra":1}')
  assert(
    issues.some((issue) => issue.code === 'ERR_UNKNOWN_FIELD' && issue.path === 'extra'),
    `expected ERR_UNKNOWN_FIELD, got ${JSON.stringify(issues)}`,
  )
})

test('null and missing rules rejected', () => {
  assert(
    issueCodes('{"schema_version":1,"rules":[null]}').includes('ERR_INVALID_TYPE'),
    'null rule accepted',
  )
  assert(issuesOf('{"schema_version":1}').length > 0, 'missing rules accepted')
})

test('rule envelope errors rejected with backend codes', () => {
  assert(
    issueCodes(
      '{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":{},"then":{},"extra":1}]}',
    ).includes('ERR_UNKNOWN_FIELD'),
    'unknown rule field accepted',
  )
  assert(
    issueCodes(
      '{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":{},"then":{}},{"id":"a","name":"B","domain":"pricing","enabled":true,"when":{},"then":{}}]}',
    ).includes('ERR_DUPLICATE_KEY'),
    'duplicate id accepted',
  )
  assert(
    issueCodes(
      '{"schema_version":1,"rules":[{"id":"bad id","name":"A","domain":"scheduling","enabled":true,"when":{},"then":{}}]}',
    ).includes('ERR_INVALID_VALUE'),
    'invalid id accepted',
  )
  assert(
    issuesOf(
      '{"schema_version":1,"rules":[{"id":"a","name":" A ","domain":"scheduling","enabled":true,"when":{},"then":{}}]}',
    ).length > 0,
    'padded name accepted',
  )
})

test('JSON depth beyond limit rejected', () => {
  let nested = '{}'
  for (let i = 0; i < 40; i++) nested = `{"all":[${nested}]}`
  const raw = `{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":${nested},"then":{}}]}`
  assert(issueCodes(raw).includes('ERR_BUDGET_EXCEEDED'), 'deep config accepted')
})

test('condition depth beyond compiler limit rejected', () => {
  let nested = '{}'
  for (let i = 0; i < 18; i++) nested = `{"all":[${nested}]}`
  const raw = `{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":${nested},"then":{}}]}`
  assert(issueCodes(raw).includes('ERR_BUDGET_EXCEEDED'), 'deep condition accepted')
})

test('per-rule node budget rejected with compiler-compatible tree', () => {
  assert(
    issuesOf(`{"schema_version":1,"rules":[${treeRule('ok', 256)}]}`).length === 0,
    '256-node tree rejected',
  )
  const over = `{"schema_version":1,"rules":[${treeRule('fat', 259)}]}`
  const issues = issuesOf(over)
  assert(
    issues.some((issue) => issue.code === 'ERR_BUDGET_EXCEEDED'),
    '259 nodes accepted',
  )

  // Reviewer fixture: root all of 3 groups (100/100/52 leaves) == 256 nodes, no mutation.
  const leaves = Array.from(
    { length: 252 },
    (_, i) => `{"fact":"upstream.model","op":"eq","value":"m${i}"}`,
  )
  const groups = [leaves.slice(0, 100), leaves.slice(100, 200), leaves.slice(200)].map(
    (group) => `{"all":[${group.join(',')}]}`,
  )
  const compatible = `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":true,"when":{"all":[${groups.join(',')}]},"then":{"type":"exclude_candidate"}}]}`
  assert(issuesOf(compatible).length === 0, 'compiler-compatible 256-node tree rejected')
  assert(pp.exportPortablePolicy(compatible).text === compatible, '256-node tree mutated')
})

test('rule count and byte budget rejected', () => {
  const rules = Array.from({ length: 101 }, (_, i) => schedulingRule(`r${i}`)).join(',')
  assert(
    issueCodes(`{"schema_version":1,"rules":[${rules}]}`).includes('ERR_BUDGET_EXCEEDED'),
    '101 rules accepted',
  )
  const huge = `{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":{"x":"${'a'.repeat(256 * 1024)}"},"then":{}}]}`
  assert(issueCodes(huge).includes('ERR_BUDGET_EXCEEDED'), 'oversize config accepted')
})

// ---------------------------------------------------------------------------
// 3. export strips recognized envelope fields, preserves raw literals
// ---------------------------------------------------------------------------
test('export drops recognized binding/observation/secret fields with warnings', () => {
  const result = pp.exportPortablePolicy(withExtras)
  assert(result.text.includes(RAW_LITERAL), 'literal lost on export')
  for (const leaked of ['group_id', 'credential_id', 'observation', 'secret']) {
    assert(!result.text.includes(leaked), `export leaked ${leaked}`)
  }
  assert(
    ['group_id', 'credential_id', 'observation', 'secret'].every((field) =>
      result.warnings.some((warning) => warning.code === 'dropped_field' && warning.path === field),
    ),
    `missing dropped_field warnings: ${JSON.stringify(result.warnings)}`,
  )
  assert(pp.validatePortablePolicy(result.text).length === 0, 'export output not portable')
})

test('export refuses unknown non-envelope root fields instead of stripping', () => {
  let threw = false
  try {
    pp.exportPortablePolicy('{"schema_version":1,"rules":[],"mystery":1}')
  } catch (error) {
    threw =
      error instanceof pp.__PolicyPortabilityError && error.issues[0].code === 'ERR_UNKNOWN_FIELD'
  }
  assert(threw, 'unknown root field was silently stripped')

  threw = false
  try {
    pp.exportPortablePolicy('{"schema_version":1,"rules":[],"secret_extra":"x"}')
  } catch (error) {
    threw =
      error instanceof pp.__PolicyPortabilityError && error.issues[0].code === 'ERR_UNKNOWN_FIELD'
  }
  assert(threw, 'unknown secret-like root field was silently stripped')
})

test('export refuses rule-level secret fields and keeps unknown conditions raw', () => {
  const withRuleSecret = `{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":{"future_condition":{"value":${RAW_LITERAL}}},"then":{"type":"exclude_candidate"},"secret":"tok"}]}`
  assert(issueCodes(withRuleSecret).includes('ERR_UNKNOWN_FIELD'), 'rule-level secret accepted')

  const unknownCondition = `{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":true,"when":{"future_condition":{"value":${RAW_LITERAL}}},"then":{"type":"exclude_candidate"}}]}`
  assert(issuesOf(unknownCondition).length === 0, 'unknown condition rejected by portability layer')
  const exported = pp.exportPortablePolicy(unknownCondition)
  assert(exported.text.includes(RAW_LITERAL), 'unknown condition literal was normalized or lost')
  assert(exported.text.includes('future_condition'), 'unknown condition structure was stripped')
})

test('export of canonical config is stable and lossless', () => {
  const result = pp.exportPortablePolicy(valid)
  assert(result.warnings.length === 0, 'unexpected warnings')
  assert(result.text.includes(RAW_LITERAL), 'literal lost on canonical export')
  const rescan = pp.scanPolicyConfig(result.text)
  assert(rescan.rules.length === 2, 'rule count changed')
  assert(rescan.rules[1].raw === pp.scanPolicyConfig(valid).rules[1].raw, 'pricing raw changed')
})

// ---------------------------------------------------------------------------
// 4. import append / replace
// ---------------------------------------------------------------------------
const target = `{"schema_version":1,"rules":[${schedulingRule('a')},${pricingRule('x', true, '2')}]}`
const source = `{"schema_version":1,"rules":[${schedulingRule('a')},${schedulingRule('b')},${pricingRule('y', true, '3')}]}`

test('import append keeps other domain order, disables imports, maps collisions', () => {
  const result = pp.importPortablePolicy(target, source, {
    domain: 'scheduling',
    mode: 'append',
    newID: sequentialID,
  })
  assert(result.imported === 2, `imported=${result.imported}`)
  assert(
    result.renamed.length === 1 &&
      result.renamed[0].from === 'a' &&
      result.renamed[0].to === 'a-copy-1',
    `renamed mismatch: ${JSON.stringify(result.renamed)}`,
  )
  assert(result.text.includes(RAW_LITERAL), 'literal lost on import append')
  const rules = pp.scanPolicyConfig(result.text).rules
  assert(
    JSON.stringify(rules.map((rule) => rule.id)) === JSON.stringify(['a', 'x', 'a-copy-1', 'b']),
    `order/id mismatch: ${JSON.stringify(rules.map((rule) => rule.id))}`,
  )
  assert(rules[0].enabled === true, 'existing rule mutated')
  assert(rules[1].enabled === true, 'other domain mutated')
  assert(rules[2].enabled === false, 'renamed import not disabled')
  assert(rules[3].enabled === false, 'import not disabled')
})

test('import replace swaps only chosen domain and preserves other domain', () => {
  const replaceTarget = `{"schema_version":1,"rules":[${schedulingRule('a')},${pricingRule('x', true, '2')},${schedulingRule('b')}]}`
  const result = pp.importPortablePolicy(replaceTarget, source, {
    domain: 'scheduling',
    mode: 'replace',
    newID: sequentialID,
  })
  const rules = pp.scanPolicyConfig(result.text).rules
  assert(
    JSON.stringify(rules.map((rule) => rule.id)) === JSON.stringify(['a-copy-1', 'b-copy-1', 'x']),
    `replace order mismatch: ${JSON.stringify(rules.map((rule) => rule.id))}`,
  )
  assert(rules[0].enabled === false && rules[1].enabled === false, 'replace imports not disabled')
  assert(rules[2].enabled === true, 'other domain mutated')
  assert(rules[2].raw.includes('"factor":"2"'), 'other domain factor changed')
})

test('import replace with empty source clears domain and warns', () => {
  const sourcePricingOnly = `{"schema_version":1,"rules":[${pricingRule('y')}]}`
  const result = pp.importPortablePolicy(target, sourcePricingOnly, {
    domain: 'scheduling',
    mode: 'replace',
    newID: sequentialID,
  })
  const rules = pp.scanPolicyConfig(result.text).rules
  assert(
    JSON.stringify(rules.map((rule) => rule.id)) === JSON.stringify(['x']),
    `expected scheduling cleared, got ${JSON.stringify(rules.map((rule) => rule.id))}`,
  )
  assert(
    result.warnings.some((warning) => warning.code === 'replace_removed_domain'),
    'missing replace warning',
  )
})

test('import enforces combined node budget before returning', () => {
  const bigTarget = `{"schema_version":1,"rules":[${treeRule('t', 256)}]}`
  const bigSource = `{"schema_version":1,"rules":[${Array.from({ length: 16 }, (_, i) =>
    treeRule(`s${i}`, 256),
  ).join(',')}]}`
  assert(issuesOf(bigSource).length === 0, 'source fixture exceeds per-rule budget')
  let threw = false
  try {
    pp.importPortablePolicy(bigTarget, bigSource, { domain: 'scheduling', newID: sequentialID })
  } catch (error) {
    threw =
      error instanceof pp.__PolicyPortabilityError &&
      error.issues.some((issue) => issue.code === 'ERR_BUDGET_EXCEEDED')
  }
  assert(threw, 'combined node budget not enforced')

  // Boundary: target(256) + 15*256 = 4096 exactly must be accepted.
  const boundarySource = `{"schema_version":1,"rules":[${Array.from({ length: 15 }, (_, i) =>
    treeRule(`b${i}`, 256),
  ).join(',')}]}`
  const accepted = pp.importPortablePolicy(bigTarget, boundarySource, {
    domain: 'scheduling',
    newID: sequentialID,
  })
  assert(pp.validatePortablePolicy(accepted.text).length === 0, '4096-node boundary rejected')
})

// ---------------------------------------------------------------------------
// 5. copy
// ---------------------------------------------------------------------------
test('copy inserts disabled rule with new ID after original', () => {
  const result = pp.copyPolicyRule(valid, 0, { newID: sequentialID })
  const rules = pp.scanPolicyConfig(result.text).rules
  assert(
    JSON.stringify(rules.map((rule) => rule.id)) ===
      JSON.stringify(['protect-astra', 'protect-astra-copy-1', 'working-hours-price']),
    `copy order mismatch: ${JSON.stringify(rules.map((rule) => rule.id))}`,
  )
  assert(rules[1].enabled === false, 'copy not disabled')
  assert(rules[1].raw.includes(RAW_LITERAL), 'copy lost numeric literal')
  assert(rules[0].raw === pp.scanPolicyConfig(valid).rules[0].raw, 'original mutated')
})

test('copy rejects out of range index and bad factory output', () => {
  let threw = false
  try {
    pp.copyPolicyRule(valid, 5)
  } catch (error) {
    threw = error instanceof pp.__PolicyPortabilityError
  }
  assert(threw, 'out-of-range copy did not throw PolicyPortabilityError')

  threw = false
  try {
    pp.copyPolicyRule(valid, 0, { newID: (_existing, preferred) => preferred })
  } catch (error) {
    threw = error instanceof pp.__PolicyPortabilityError
  }
  assert(threw, 'colliding factory output accepted')
})

// ---------------------------------------------------------------------------
// 6. source immutability
// ---------------------------------------------------------------------------
test('operations never mutate source text', () => {
  const frozen = valid
  const before = JSON.stringify(pp.scanPolicyConfig(valid))
  pp.exportPortablePolicy(valid)
  pp.copyPolicyRule(valid, 0, { newID: sequentialID })
  pp.importPortablePolicy(target, source, { domain: 'scheduling', newID: sequentialID })
  assert(valid === frozen, 'source string identity/contents changed')
  assert(JSON.stringify(pp.scanPolicyConfig(valid)) === before, 'source scan changed')
})

if (failed > 0) {
  console.error(`FAILED: ${failed} policy portability checks failed.`)
  process.exit(1)
}
console.log('All policy portability module checks passed.')
