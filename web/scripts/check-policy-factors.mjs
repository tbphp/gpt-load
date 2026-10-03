import { extract, loadTsModule, parseSfc, readSource, stripImports } from './policy-test-utils.mjs'

const runtimeGlobals = () => ({
  InvalidResponseError: class InvalidResponseError extends Error {},
})

const classic = loadTsModule(
  extract(readSource('web/src/frontends/classic/app/resources/request-logs.ts'), [
    'receiptCodes',
    'receiptLineStates',
  ]) +
    stripImports(readSource('web/src/frontends/classic/lib/price-multiplier.ts')) +
    stripImports(readSource('web/src/frontends/classic/app/resources/projector.ts')) +
    extract(readSource('web/src/frontends/classic/app/resources/channels.ts'), [
      'projectChannelID',
    ]) +
    extract(readSource('web/src/frontends/classic/app/resources/request-logs.ts'), [
      'invalidResponse',
      'projectNonBlankString',
      'projectPricingMode',
      'projectPricingReceipt',
    ]),
  runtimeGlobals(),
)

const modern = loadTsModule(
  stripImports(readSource('web/src/frontends/modern/api/response.ts')) +
    extract(readSource('web/src/frontends/modern/api/logs.ts'), ['count', 'receipt']) +
    '\nconst optionalText = (value) => (value == null ? null : text(value)); const optionalCount = (value) => (value == null ? null : count(value));',
  runtimeGlobals(),
)

let failed = 0
function test(name, fn) {
  try {
    fn()
    console.log('PASS', name)
  } catch (e) {
    failed++
    console.log('FAIL', name, e.message || e.constructor.name)
  }
}

const fixture = {
  schema_version: 7,
  method: 'unit_rate_sum',
  method_version: 1,
  currency: 'USD',
  pricing_mode: 'standard',
  price_multipliers: { group: '1', access_key: '1' },
  policy_factors: [
    {
      rule_id: 'p',
      name_snapshot: 'Admin billing rule',
      binding_scope: 'group',
      revision: '18446744073709551615',
      factor: '2',
      multiplier: '2',
    },
  ],
  rule: { channel_id: 'openai', model_id: 'gpt-4o' },
  context_threshold_tokens: null,
  line_items: [
    {
      code: 'input',
      quantity: '1000000',
      rate_nano_usd_per_million: '100',
      multiplier: { numerator: '1', denominator: '1' },
      state: 'priced',
      amount_nano_usd: '100',
    },
  ],
  base_total_nano_usd: '100',
  total_nano_usd: '200',
}

test('Classic accepts exact full uint64 revision', () => {
  classic.projectPricingReceipt(fixture)
})

test('Modern preserves exact full uint64 revision', () => {
  const r = modern.receipt(fixture)
  if (r.policy_factors[0].revision !== '18446744073709551615') {
    throw new Error('lost precision')
  }
})

test('Classic accepts valid noncanonical factor 02.000000', () => {
  const noncanon = structuredClone(fixture)
  noncanon.policy_factors[0].factor = '02.000000'
  classic.projectPricingReceipt(noncanon)
})

const normal = structuredClone(fixture)
normal.policy_factors[0].revision = '42'

for (const [name, parse] of [
  ['Classic', (x) => classic.projectPricingReceipt(x)],
  ['Modern', (x) => modern.receipt(x)],
]) {
  test(`${name} valid v7`, () => parse(normal))

  for (const [caseName, change] of [
    ['empty factors', (r) => (r.policy_factors = [])],
    ['invalid binding scope', (r) => (r.policy_factors[0].binding_scope = 'access_key')],
    ['zero revision', (r) => (r.policy_factors[0].revision = '0')],
    ['mismatched factor/multiplier', (r) => (r.policy_factors[0].multiplier = '3')],
    ['unknown nested factor field', (r) => (r.policy_factors[0].unexpected = true)],
    ['v6 with v7 factors', (r) => (r.schema_version = 6)],
    ['v7 missing factors', (r) => delete r.policy_factors],
    ['unsupported schema', (r) => (r.schema_version = 8)],
    [
      'inherited required factor field',
      (r) => {
        const old = r.policy_factors[0]
        r.policy_factors[0] = Object.assign(Object.create({ revision: '42' }), old)
        delete r.policy_factors[0].revision
      },
    ],
    ['outer whitespace factor', (r) => (r.policy_factors[0].factor = ' 2 ')],
    [
      'unsafe numeric uint64 revision',
      (r) => (r.policy_factors[0].revision = JSON.parse('9007199254740993')),
    ],
  ]) {
    test(`${name} rejects ${caseName}`, () => {
      const r = structuredClone(normal)
      change(r)
      try {
        parse(r)
      } catch {
        return
      }
      throw new Error('invalid receipt accepted')
    })
  }

  // Cross-binding duplicate rule IDs must be accepted
  test(`${name} accepts cross-binding duplicate rule IDs`, () => {
    const r = structuredClone(normal)
    r.policy_factors = [
      {
        rule_id: 'dup',
        name_snapshot: 'group rule',
        binding_scope: 'group',
        revision: '10',
        factor: '2',
        multiplier: '2',
      },
      {
        rule_id: 'dup',
        name_snapshot: 'cred rule',
        binding_scope: 'credential',
        revision: '20',
        factor: '3',
        multiplier: '3',
      },
    ]
    parse(r)
  })

  // Exact decimal acceptance
  for (const [n, f, m] of [
    ['micro', '0.000001', '0.000001'],
    ['near_limit', '0999.999999', '999.999999'],
    ['max', '1000.000000', '1000'],
    ['noncanonical_zero', '000.000000', '0'],
  ]) {
    test(`${name} accepts exact decimal ${n}`, () => {
      const r = structuredClone(normal)
      r.policy_factors[0].factor = f
      r.policy_factors[0].multiplier = m
      parse(r)
    })
  }

  // Malformed factor rejection
  for (const f of ['1e-6', '0.0000001', '1000.000001', '-0', '+1', '1.', '.5']) {
    test(`${name} rejects malformed factor ${f}`, () => {
      const r = structuredClone(normal)
      r.policy_factors[0].factor = f
      try {
        parse(r)
      } catch {
        return
      }
      throw new Error('invalid factor accepted')
    })
  }
}

// Actual historical v1-v6 control DTOs matching backend mapRequestLogPricingReceipt normalization
for (let version = 1; version <= 6; version++) {
  let rule
  if (version === 1) {
    rule = { scope_key: 'provider:openai', model_id: 'gpt-4o' }
  } else if (version === 2) {
    rule = { model_id: 'gpt-4o' }
  } else {
    rule = { channel_id: 'openai', model_id: 'gpt-4o' }
  }
  const actual = {
    schema_version: version,
    method: 'unit_rate_sum',
    method_version: 1,
    currency: 'USD',
    pricing_mode: 'standard',
    rule,
    context_threshold_tokens: null,
    line_items: [
      {
        code: 'input',
        quantity: '1000000',
        rate_nano_usd_per_million: '100',
        multiplier: { numerator: '1', denominator: '1' },
        state: 'priced',
        amount_nano_usd: '100',
      },
    ],
    ...(version >= 5 ? { price_multipliers: { group: '1', access_key: '1' } } : {}),
    ...(version >= 6 ? { base_total_nano_usd: '100' } : {}),
    total_nano_usd: '100',
  }
  for (const [name, parse] of [
    ['Classic', (x) => classic.projectPricingReceipt(x)],
    ['Modern', (x) => modern.receipt(x)],
  ]) {
    test(`${name} accepts actual historical v${version} control DTO`, () => parse(actual))
  }
}

// 2. Actual Vue SFC key expression must keep scope and rule IDs distinct.
function readSFCKey(relPath) {
  const { descriptor } = parseSfc(readSource(relPath))
  const ast = descriptor.template.ast
  let key = null
  function visit(n) {
    if (n.props) {
      const forDir = n.props.find(
        (p) => p.name === 'for' && p.exp?.loc?.source.includes('receipt.policy_factors'),
      )
      if (forDir) {
        const boundKey = n.props.find((p) => p.name === 'bind' && p.arg?.content === 'key')
        if (boundKey) key = boundKey.exp.loc.source
      }
    }
    for (const c of n.children ?? []) visit(c)
    if (n.branches) for (const c of n.branches) visit(c)
  }
  visit(ast)
  if (!key) throw new Error('no source policy factor key found in ' + relPath)
  return new Function('rule', 'f', `return (${key})`)
}

for (const compPath of [
  'web/src/frontends/classic/features/monitor/LogDetailDrawer.vue',
  'web/src/frontends/modern/features/logs/LogPricingReceipt.vue',
]) {
  test(`Vue SFC key distinguishes scope and rule IDs for ${compPath}`, () => {
    const keyFn = readSFCKey(compPath)
    const item = { binding_scope: 'group', rule_id: 'dup' }
    if (
      keyFn(item, item) ===
      keyFn({ ...item, binding_scope: 'credential' }, { ...item, binding_scope: 'credential' })
    ) {
      throw new Error(`scope/rule collision in ${compPath}`)
    }
  })
}

if (failed > 0) {
  console.error(`FAILED: ${failed} checks failed.`)
  process.exit(1)
} else {
  console.log('All frontend policy factor and Vue reconciliation checks passed.')
}
