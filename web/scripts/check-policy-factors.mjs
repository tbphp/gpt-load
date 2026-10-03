import fs from 'node:fs'
import vm from 'node:vm'
import path from 'node:path'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const scriptDir = import.meta.dirname
const webRoot = path.resolve(scriptDir, '..')
const repoRoot = path.resolve(webRoot, '..')
const ts = require(path.join(webRoot, 'node_modules/typescript'))
const { createRenderer, h } = require(path.join(webRoot, 'node_modules/vue'))
const { parse: parseSFC } = require(path.join(webRoot, 'node_modules/vue/compiler-sfc'))

function source(p) {
  return fs.readFileSync(path.join(repoRoot, p), 'utf8')
}
function parsed(s) {
  return ts.createSourceFile('review.ts', s, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
}
function stripImports(p) {
  const s = source(p)
  return (
    parsed(s)
      .statements.filter((x) => !ts.isImportDeclaration(x))
      .map((x) => x.getFullText())
      .join('\n')
      .replace(/\bexport\s+/g, '') + '\n'
  )
}
function funcs(p, names) {
  const s = source(p)
  return (
    parsed(s)
      .statements.filter((x) => ts.isFunctionDeclaration(x) && names.includes(x.name.text))
      .map((x) => x.getFullText())
      .join('\n')
      .replace(/\bexport\s+/g, '') + '\n'
  )
}
function vars(p, names) {
  const s = source(p)
  return (
    parsed(s)
      .statements.filter(
        (x) =>
          ts.isVariableStatement(x) &&
          x.declarationList.declarations.some((d) => names.includes(d.name.text)),
      )
      .map((x) => x.getFullText())
      .join('\n')
      .replace(/\bexport\s+/g, '') + '\n'
  )
}
function runtime(s) {
  const context = {
    console,
    InvalidResponseError: class InvalidResponseError extends Error {},
  }
  vm.createContext(context)
  vm.runInContext(
    ts.transpileModule(s, {
      compilerOptions: {
        target: ts.ScriptTarget.ES2022,
        module: ts.ModuleKind.None,
      },
    }).outputText,
    context,
  )
  return context
}

const classic = runtime(
  vars('web/src/frontends/classic/app/resources/request-logs.ts', [
    'receiptCodes',
    'receiptLineStates',
  ]) +
    stripImports('web/src/frontends/classic/lib/price-multiplier.ts') +
    stripImports('web/src/frontends/classic/app/resources/projector.ts') +
    funcs('web/src/frontends/classic/app/resources/channels.ts', ['projectChannelID']) +
    funcs('web/src/frontends/classic/app/resources/request-logs.ts', [
      'invalidResponse',
      'projectNonBlankString',
      'projectPricingMode',
      'projectPricingReceipt',
    ]),
)

const modern = runtime(
  stripImports('web/src/frontends/modern/api/response.ts') +
    funcs('web/src/frontends/modern/api/logs.ts', ['count', 'receipt']) +
    '\nconst optionalText = (value) => (value == null ? null : text(value)); const optionalCount = (value) => (value == null ? null : count(value));',
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
    [
      'mixed revisions in same group scope',
      (r) => {
        r.policy_factors.push({
          rule_id: 'p2',
          name_snapshot: 'rule2',
          binding_scope: 'group',
          revision: '43',
          factor: '1',
          multiplier: '1',
        })
      },
    ],
    [
      'duplicate rule id within same group scope',
      (r) => {
        r.policy_factors.push({
          rule_id: 'p',
          name_snapshot: 'rule2',
          binding_scope: 'group',
          revision: '42',
          factor: '1',
          multiplier: '1',
        })
      },
    ],
    [
      'out-of-order scopes (credential before group)',
      (r) => {
        r.policy_factors = [
          {
            rule_id: 'c1',
            name_snapshot: 'cred',
            binding_scope: 'credential',
            revision: '1',
            factor: '1',
            multiplier: '1',
          },
          {
            rule_id: 'g1',
            name_snapshot: 'grp',
            binding_scope: 'group',
            revision: '1',
            factor: '1',
            multiplier: '1',
          },
        ]
      },
    ],
    ['outer whitespace factor', (r) => (r.policy_factors[0].factor = ' 2 ')],
    [
      'unsafe numeric uint64 revision',
      (r) => (r.policy_factors[0].revision = JSON.parse('9007199254740993')),
    ],
    ['unknown top-level field', (r) => (r.unexpected = true)],
    [
      'inherited top-level schema',
      (r) => {
        Object.setPrototypeOf(r, { schema_version: 7 })
        delete r.schema_version
      },
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

for (const field of [
  'pricing_mode',
  'price_multipliers',
  'base_total_nano_usd',
  'policy_factors',
]) {
  for (const [name, parse] of [
    ['Classic', (x) => classic.projectPricingReceipt(x)],
    ['Modern', (x) => modern.receipt(x)],
  ]) {
    test(`${name} rejects inherited v7 required ${field}`, () => {
      const r = structuredClone(normal)
      const value = r[field]
      delete r[field]
      Object.setPrototypeOf(r, { [field]: value })
      try {
        parse(r)
      } catch {
        return
      }
      throw new Error('inherited required field admitted')
    })
  }
}

// 2. Vue actual component SFC AST parsed reconciliation
function readSFCKey(relPath) {
  const txt = fs.readFileSync(path.join(repoRoot, relPath), 'utf8')
  const { descriptor } = parseSFC(txt)
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

function node(tag, text = '') {
  return { tag, text, children: [], parent: null }
}
const host = {
  createElement: (tag) => node(tag),
  createText: (text) => node('#text', text),
  createComment: (text) => node('#comment', text),
  setText: (n, t) => {
    n.text = t
  },
  setElementText: (n, t) => {
    n.text = t
    n.children = []
  },
  parentNode: (n) => n.parent,
  nextSibling: (n) => n.parent?.children[n.parent.children.indexOf(n) + 1] ?? null,
  patchProp() {},
  remove(n) {
    if (n.parent) {
      const at = n.parent.children.indexOf(n)
      if (at >= 0) n.parent.children.splice(at, 1)
      n.parent = null
    }
  },
  insert(n, parent, anchor = null) {
    host.remove(n)
    const idx = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(idx < 0 ? parent.children.length : idx, 0, n)
    n.parent = parent
  },
}

for (const compPath of [
  'web/src/frontends/classic/features/monitor/LogDetailDrawer.vue',
  'web/src/frontends/modern/features/logs/LogPricingReceipt.vue',
]) {
  test(`Vue reconciles actual SFC key expression for ${compPath}`, () => {
    const keyFn = readSFCKey(compPath)
    const { render } = createRenderer(host)
    const rootNode = node('root')
    const draw = (rows) =>
      h(
        'div',
        null,
        rows.map(([binding_scope, rule_id, label]) => {
          const item = { binding_scope, rule_id }
          return h('span', { key: keyFn(item, item) }, label)
        }),
      )

    render(
      draw([
        ['group', 'a', 'Group a'],
        ['group', 'dup', 'Group old dup'],
        ['group', 'b', 'Group b'],
        ['credential', 'dup', 'Credential old dup'],
        ['credential', 'z', 'Credential z'],
      ]),
      rootNode,
    )

    const after = [
      ['group', 'q', 'Group q'],
      ['group', 'dup', 'Group new dup'],
      ['group', 'r', 'Group r'],
      ['credential', 'dup', 'Credential new dup'],
      ['credential', 's', 'Credential s'],
    ]
    render(draw(after), rootNode)

    const actual = rootNode.children[0].children.map((n) => n.text)
    const expected = after.map((x) => x[2])
    if (JSON.stringify(actual) !== JSON.stringify(expected)) {
      throw new Error(`Reconciliation mismatch: expected ${expected}, got ${actual}`)
    }
  })
}

if (failed > 0) {
  console.error(`FAILED: ${failed} checks failed.`)
  process.exit(1)
} else {
  console.log('All frontend policy factor and Vue reconciliation checks passed.')
}
