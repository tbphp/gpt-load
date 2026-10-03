// 可组合策略可视化编辑器的纯逻辑层。
// 只依赖标准 JavaScript，不引用 Vue、路由、既有面板或业务 API，可由 Node 做类型擦除执行。
// 关键约束：
// 1. 保留原始 JSON 字面量（尤其是额度阈值等 JSON number），不做 parse/stringify 往返，
//    直到用户显式编辑该字段才替换为新的已验证字面量。
// 2. 不支持可视化但仍受后端支持的结构不丢弃，标记为 JSON-only 并在原树中保留。
// 3. 在递归渲染前先检查深度/节点/规则/字节预算。

export type PolicyDomain = 'scheduling' | 'pricing'

export interface PolicyLimits {
  maxConfigBytes: number
  maxJSONDepth: number
  maxRulesPerConfig: number
  maxConditionDepth: number
  maxNodesPerRule: number
  maxNodesPerConfig: number
  maxListItems: number
  maxNameLength: number
}

export const policyLimits: PolicyLimits = {
  maxConfigBytes: 256 << 10,
  // 与后端 policy.MaxJSONDepth 一致：在递归下降解析阶段硬限制 JSON 嵌套。
  maxJSONDepth: 64,
  maxRulesPerConfig: 100,
  maxConditionDepth: 16,
  maxNodesPerRule: 256,
  maxNodesPerConfig: 4096,
  maxListItems: 100,
  maxNameLength: 128,
}

export const quotaScopeAccount = 'account'
export const quotaReduceMin = 'min'
// 额度参数的 canonical fact key；UI 选择器用 `quota:<seconds>` token 表示“额度比例 + 具体窗口”。
export const quotaFactKey = 'credential.quota.remaining_ratio'

export function quotaWindowToken(rawWindow: string): string {
  return `quota:${rawWindow}`
}

export function quotaWindowFromToken(token: unknown): string | undefined {
  if (typeof token !== 'string') return undefined
  const match = token.match(/^quota:([1-9]\d*)$/)
  return match ? match[1] : undefined
}

// 只用于 UI 文案的紧凑窗口展示（5h / 7d / 90m / 90000s），不参与落盘。
export function formatQuotaWindow(rawWindow: string): string {
  const total = Number(rawWindow)
  if (!Number.isSafeInteger(total) || total <= 0) return `${rawWindow}s`
  if (total % 86400 === 0) return `${total / 86400}d`
  if (total % 3600 === 0) return `${total / 3600}h`
  if (total % 60 === 0) return `${total / 60}m`
  return `${total}s`
}

export type JsonLiteralKind = 'string' | 'number' | 'boolean' | 'null'

export interface JsonLiteralNode {
  type: 'literal'
  literal: JsonLiteralKind
  // 原始 JSON token，字符串含引号；序列化时逐字复用，保证数值精度不丢失。
  raw: string
}

export interface JsonObjectEntry {
  key: string
  value: JsonNode
}

export interface JsonObjectNode {
  type: 'object'
  entries: JsonObjectEntry[]
}

export interface JsonArrayNode {
  type: 'array'
  items: JsonNode[]
}

export type JsonNode = JsonObjectNode | JsonArrayNode | JsonLiteralNode

export class RawJsonSyntaxError extends Error {}

function jsonNumberPrefix(): RegExp {
  return /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/
}

function jsonNumberFull(): RegExp {
  return /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/
}

export function utf8ByteLength(text: string): number {
  return new TextEncoder().encode(text).length
}

// 记录原始 token 的递归下降 JSON 解析器。仅接受标准 JSON，不吞掉尾随内容。
export function parseRawJson(text: string): JsonNode {
  let index = 0
  const length = text.length

  function fail(message: string): never {
    throw new RawJsonSyntaxError(`${message} (offset ${index})`)
  }

  function skipWhitespace(): void {
    while (index < length) {
      const ch = text[index]
      if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') index++
      else break
    }
  }

  // 严格校验 JSON 字符串字面量：只接受标准转义（\" \\ \/ \b \f \n \r \t \uXXXX），
  // 拒绝其他反斜杠转义与未转义的控制字符。返回值始终是原始切片，含转义序列，逐字保留。
  function parseStringRaw(): string {
    const start = index
    index++
    while (index < length) {
      const ch = text[index]
      if (ch === '"') {
        index++
        return text.slice(start, index)
      }
      if (ch === '\\') {
        const escape = text[index + 1]
        if (escape === 'u') {
          if (!/^[0-9a-fA-F]{4}$/.test(text.slice(index + 2, index + 6))) {
            return fail('invalid unicode escape in string')
          }
          index += 6
          continue
        }
        if (escape === undefined || !'"\\/bfnrt'.includes(escape)) {
          return fail('invalid escape sequence in string')
        }
        index += 2
        continue
      }
      if (ch.charCodeAt(0) < 0x20) return fail('unescaped control character in string')
      index++
    }
    return fail('unterminated string')
  }

  function parseValue(depth: number): JsonNode {
    skipWhitespace()
    if (index >= length) return fail('unexpected end of JSON input')
    if (depth > policyLimits.maxJSONDepth) return fail('JSON nesting too deep')
    const ch = text[index]
    if (ch === '{') return parseObject(depth)
    if (ch === '[') return parseArray(depth)
    if (ch === '"') {
      return { type: 'literal', literal: 'string', raw: parseStringRaw() }
    }
    const rest = text.slice(index)
    for (const [token, literal] of [
      ['true', 'boolean'],
      ['false', 'boolean'],
      ['null', 'null'],
    ] as const) {
      if (rest.startsWith(token)) {
        index += token.length
        return { type: 'literal', literal, raw: token }
      }
    }
    const match = rest.match(jsonNumberPrefix())
    if (!match) return fail('unexpected token')
    index += match[0].length
    return { type: 'literal', literal: 'number', raw: match[0] }
  }

  function parseObject(depth: number): JsonObjectNode {
    const entries: JsonObjectEntry[] = []
    const seenKeys = new Set<string>()
    index++
    skipWhitespace()
    if (text[index] === '}') {
      index++
      return { type: 'object', entries }
    }
    for (;;) {
      skipWhitespace()
      if (text[index] !== '"') return fail('expected object key')
      const keyRaw = parseStringRaw()
      let key: string
      try {
        key = JSON.parse(keyRaw) as string
      } catch {
        return fail('invalid object key')
      }
      // 按解码后的键判重：`"a"` 与 `"\u0061"` 是同一字段，与后端严格解析保持一致。
      const duplicateKey = Array.from(key, (character) => {
        const codePoint = character.codePointAt(0)!
        return codePoint >= 0xd800 && codePoint <= 0xdfff ? '\ufffd' : character
      }).join('')
      if (seenKeys.has(duplicateKey)) return fail(`duplicate object key ${JSON.stringify(key)}`)
      seenKeys.add(duplicateKey)
      skipWhitespace()
      if (text[index] !== ':') return fail('expected colon')
      index++
      entries.push({ key, value: parseValue(depth + 1) })
      skipWhitespace()
      const ch = text[index]
      if (ch === ',') {
        index++
        continue
      }
      if (ch === '}') {
        index++
        return { type: 'object', entries }
      }
      return fail('expected comma or closing brace')
    }
  }

  function parseArray(depth: number): JsonArrayNode {
    const items: JsonNode[] = []
    index++
    skipWhitespace()
    if (text[index] === ']') {
      index++
      return { type: 'array', items }
    }
    for (;;) {
      items.push(parseValue(depth + 1))
      skipWhitespace()
      const ch = text[index]
      if (ch === ',') {
        index++
        continue
      }
      if (ch === ']') {
        index++
        return { type: 'array', items }
      }
      return fail('expected comma or closing bracket')
    }
  }

  const node = parseValue(1)
  skipWhitespace()
  if (index !== length) fail('unexpected trailing content')
  return node
}

// 序列化会规范化空白，但所有保留字面量逐字输出，数值/字符串精度不变。
export function serializeJson(node: JsonNode): string {
  if (node.type === 'literal') return node.raw
  if (node.type === 'array') return `[${node.items.map(serializeJson).join(',')}]`
  return `{${node.entries
    .map((entry) => `${JSON.stringify(entry.key)}:${serializeJson(entry.value)}`)
    .join(',')}}`
}

export function objectNode(entries: JsonObjectEntry[]): JsonObjectNode {
  return { type: 'object', entries }
}

export function arrayNode(items: JsonNode[]): JsonArrayNode {
  return { type: 'array', items }
}

export function stringLiteral(value: string): JsonLiteralNode {
  return { type: 'literal', literal: 'string', raw: JSON.stringify(value) }
}

export function numberLiteral(raw: string): JsonLiteralNode | undefined {
  if (!isJsonNumberRaw(raw)) return undefined
  return { type: 'literal', literal: 'number', raw }
}

export function booleanLiteral(value: boolean): JsonLiteralNode {
  return { type: 'literal', literal: 'boolean', raw: value ? 'true' : 'false' }
}

export function objectEntries(node: JsonNode | undefined): JsonObjectEntry[] | undefined {
  return node && node.type === 'object' ? node.entries : undefined
}

export function arrayItems(node: JsonNode | undefined): JsonNode[] | undefined {
  return node && node.type === 'array' ? node.items : undefined
}

export function getField(node: JsonNode | undefined, key: string): JsonNode | undefined {
  const entries = objectEntries(node)
  if (!entries) return undefined
  return entries.find((entry) => entry.key === key)?.value
}

export function fieldKeys(node: JsonNode | undefined): string[] {
  return objectEntries(node)?.map((entry) => entry.key) ?? []
}

export function literalString(node: JsonNode | undefined): string | undefined {
  if (!node || node.type !== 'literal' || node.literal !== 'string') return undefined
  try {
    return JSON.parse(node.raw) as string
  } catch {
    return undefined
  }
}

export function literalNumberRaw(node: JsonNode | undefined): string | undefined {
  if (!node || node.type !== 'literal' || node.literal !== 'number') return undefined
  return node.raw
}

export function literalBoolean(node: JsonNode | undefined): boolean | undefined {
  if (!node || node.type !== 'literal' || node.literal !== 'boolean') return undefined
  return node.raw === 'true'
}

// 返回新的对象节点，保留其余字段（包括可视化不认识的字段）。
export function setField(node: JsonObjectNode, key: string, value: JsonNode): JsonObjectNode {
  const entries = node.entries.map((entry) => ({ ...entry }))
  const at = entries.findIndex((entry) => entry.key === key)
  if (at >= 0) entries[at] = { key, value }
  else entries.push({ key, value })
  return { type: 'object', entries }
}

export function removeField(node: JsonObjectNode, key: string): JsonObjectNode {
  return { type: 'object', entries: node.entries.filter((entry) => entry.key !== key) }
}

export function isJsonNumberRaw(raw: string): boolean {
  return jsonNumberFull().test(raw)
}

// 复刻后端 pricing.ParsePriceMultiplier 的范围与精度：0..1000，最多六位小数。
export function isMultiplierRaw(raw: string): boolean {
  if (!/^\d+(?:\.\d+)?$/.test(raw)) return false
  const dot = raw.indexOf('.')
  const integer = dot < 0 ? raw : raw.slice(0, dot)
  const fraction = dot < 0 ? '' : raw.slice(dot + 1)
  if (fraction.length > 6) return false
  let whole: bigint
  try {
    whole = BigInt(integer)
  } catch {
    return false
  }
  if (whole > 1000n) return false
  if (whole === 1000n && /[1-9]/.test(fraction)) return false
  return true
}

// 额度比例保留 JSON number 原文；拒绝指数形式，避免前端误报“可无损表示”，交后端判定。
export function isRatioRaw(raw: string): boolean {
  if (!isJsonNumberRaw(raw)) return false
  if (raw.includes('e') || raw.includes('E')) return false
  if (raw.startsWith('-')) return false
  const dot = raw.indexOf('.')
  let integer = dot < 0 ? raw : raw.slice(0, dot)
  integer = integer.replace(/^0+(?=\d)/, '')
  if (integer === '0') return true
  if (integer !== '1') return false
  if (dot < 0) return true
  return !/[1-9]/.test(raw.slice(dot + 1))
}

// 时间段 HH:MM，00:00..23:59；开始不等于结束（首批不允许 24:00 或全天常量）。
export function isTimeOfDay(raw: string): boolean {
  const match = raw.match(/^(\d{2}):(\d{2})$/)
  if (!match) return false
  const hours = Number(match[1])
  const minutes = Number(match[2])
  return hours <= 23 && minutes <= 59
}

export type PolicyIssueCode =
  | 'empty'
  | 'syntax'
  | 'rootType'
  | 'schemaVersion'
  | 'rulesType'
  | 'ruleCount'
  | 'conditionDepth'
  | 'nodePerRule'
  | 'nodePerConfig'
  | 'configBytes'
  | 'ruleShape'
  | 'unsupported'

export interface PolicyIssue {
  code: PolicyIssueCode
  path: string
  fatal: boolean
  detail?: string
}

export type ConditionKind = 'all' | 'any' | 'not' | 'param' | 'time_window' | 'unsupported'

export interface FactDefinition {
  key: string
  valueType: 'string' | 'number'
  operators: string[]
  quota: boolean
  labelKey: string
}

export const factDefinitions: FactDefinition[] = [
  {
    key: 'request.model',
    valueType: 'string',
    operators: ['eq'],
    quota: false,
    labelKey: 'policyEditor.fact.facts.requestModel',
  },
  {
    key: 'upstream.model',
    valueType: 'string',
    operators: ['eq'],
    quota: false,
    labelKey: 'policyEditor.fact.facts.upstreamModel',
  },
  {
    key: 'credential.quota.remaining_ratio',
    valueType: 'number',
    operators: ['eq', 'lt', 'lte', 'gt', 'gte'],
    quota: true,
    labelKey: 'policyEditor.fact.facts.quotaRemainingRatio',
  },
]

export function factDefinition(key: string | undefined): FactDefinition | undefined {
  return factDefinitions.find((definition) => definition.key === key)
}

function keysEqual(node: JsonNode | undefined, expected: string[]): boolean {
  if (!node || node.type !== 'object') return false
  const keys = node.entries.map((entry) => entry.key)
  if (keys.length !== expected.length) return false
  return expected.every((key) => keys.includes(key))
}

// 动作节点必须恰好是契约支持的形态；额外/缺失字段一律 JSON-only，避免隐式改写。
export function actionRepresentable(
  node: JsonNode | undefined,
  domain: PolicyDomain | '',
): boolean {
  if (!node || node.type !== 'object') return false
  const type = literalString(getField(node, 'type'))
  if (domain === 'scheduling') {
    return type === 'exclude_candidate' && keysEqual(node, ['type'])
  }
  if (domain === 'pricing') {
    return (
      type === 'multiply_price' &&
      keysEqual(node, ['type', 'factor']) &&
      literalString(getField(node, 'factor')) !== undefined
    )
  }
  return false
}

// 与后端 policy 编译保持一致：星期必须是 JSON 整数字面量（strconv.Atoi 语义）。
// 1.0 / 1e0 / 高精度小数不是整数语法，绝不能经 Number 舍入后当成合法星期并被视觉改写。
const jsonIntegerRawPattern = /^-?(?:0|[1-9]\d*)$/

function weekdayFromRaw(node: JsonNode | undefined): number | undefined {
  const raw = literalNumberRaw(node)
  if (raw === undefined || !jsonIntegerRawPattern.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) ? value : undefined
}

function isWeekdaysShape(node: JsonNode | undefined): boolean {
  const items = arrayItems(node)
  if (!items || items.length === 0 || items.length > 7) return false
  return items.every((item) => {
    const value = weekdayFromRaw(item)
    return value !== undefined && value >= 0 && value <= 6
  })
}

function isRangesShape(node: JsonNode | undefined): boolean {
  const items = arrayItems(node)
  if (!items || items.length === 0 || items.length > policyLimits.maxListItems) return false
  return items.every((item) => {
    const pair = arrayItems(item)
    return Boolean(
      pair &&
      pair.length === 2 &&
      literalString(pair[0]) !== undefined &&
      literalString(pair[1]) !== undefined,
    )
  })
}

export function conditionKind(node: JsonNode | undefined): ConditionKind {
  if (!node || node.type !== 'object') return 'unsupported'
  const keys = node.entries.map((entry) => entry.key)
  if (keys.length === 1 && keys[0] === 'all') {
    const items = arrayItems(getField(node, 'all'))
    if (!items) return 'unsupported'
    return 'all'
  }
  if (keys.length === 1 && keys[0] === 'any') {
    const items = arrayItems(getField(node, 'any'))
    if (!items) return 'unsupported'
    return 'any'
  }
  if (keys.length === 1 && keys[0] === 'not') {
    const child = getField(node, 'not')
    return child && child.type === 'object' ? 'not' : 'unsupported'
  }
  if (keys.includes('predicate')) {
    if (
      !keysEqual(node, ['predicate', 'weekdays', 'ranges']) ||
      literalString(getField(node, 'predicate')) !== 'time_window'
    ) {
      return 'unsupported'
    }
    const weekdays = getField(node, 'weekdays')
    const ranges = getField(node, 'ranges')
    if (!isWeekdaysShape(weekdays) || !isRangesShape(ranges)) return 'unsupported'
    return 'time_window'
  }
  if (keys.includes('fact')) {
    const fact = literalString(getField(node, 'fact')) ?? ''
    const definition = factDefinition(fact)
    if (!definition) return 'unsupported'
    const op = literalString(getField(node, 'op')) ?? ''
    if (!definition.operators.includes(op)) return 'unsupported'
    if (definition.quota) {
      if (!keysEqual(node, ['fact', 'select', 'reduce', 'op', 'value'])) return 'unsupported'
      const select = getField(node, 'select')
      if (!keysEqual(select, ['scope', 'window_seconds'])) return 'unsupported'
      if (literalString(getField(select, 'scope')) !== quotaScopeAccount) return 'unsupported'
      const window = literalNumberRaw(getField(select, 'window_seconds'))
      if (window === undefined || !/^[1-9]\d*$/.test(window)) return 'unsupported'
      if (literalString(getField(node, 'reduce')) !== quotaReduceMin) return 'unsupported'
      const valRaw = literalNumberRaw(getField(node, 'value'))
      if (valRaw === undefined || !isRatioRaw(valRaw)) return 'unsupported'
      return 'param'
    }
    if (!keysEqual(node, ['fact', 'op', 'value'])) return 'unsupported'
    if (literalString(getField(node, 'value')) === undefined) return 'unsupported'
    return 'param'
  }
  return 'unsupported'
}

export interface VisualRule {
  index: number
  node: JsonObjectNode
}

export interface VisualDocumentResult {
  root?: JsonObjectNode
  rules: VisualRule[]
  issues: PolicyIssue[]
}

interface Counters {
  nodes: number
}

function inspectCondition(
  node: JsonNode,
  path: string,
  depth: number,
  counters: Counters,
  issues: PolicyIssue[],
): void {
  counters.nodes++
  if (depth > policyLimits.maxConditionDepth) {
    issues.push({ code: 'conditionDepth', path, fatal: true })
  }
  if (counters.nodes > policyLimits.maxNodesPerRule) {
    issues.push({ code: 'nodePerRule', path, fatal: true })
  }
  const kind = conditionKind(node)
  if (kind === 'unsupported') {
    issues.push({ code: 'unsupported', path, fatal: false })
    return
  }
  if (kind === 'all' || kind === 'any') {
    const children = arrayItems(getField(node, kind)) ?? []
    children.forEach((child, index) =>
      inspectCondition(child, `${path}.${kind}[${index}]`, depth + 1, counters, issues),
    )
    return
  }
  if (kind === 'not') {
    const child = getField(node, 'not')
    if (child) inspectCondition(child, `${path}.not`, depth + 1, counters, issues)
  }
}

// 解析顶层正文并做有界检查。任何一个 fatal 存在时都不得递归渲染条件树。
export type GroupPolicyMode = 'inherit' | 'override'

// 顶层 group_policy（credential policy 专用）：默认 inherit。未知取值不算 fatal，其余配置仍可编辑。
export function groupPolicyMode(root: JsonNode | undefined): GroupPolicyMode {
  return literalString(getField(root, 'group_policy')) === 'override' ? 'override' : 'inherit'
}

export function groupPolicyModeRepresentable(root: JsonNode | undefined): boolean {
  const field = getField(root, 'group_policy')
  if (!field) return true
  const raw = literalString(field)
  return raw === 'inherit' || raw === 'override'
}

// 只改 mode，其余 root 字段（含规则列表）原样保留。
export function setGroupPolicyMode(root: JsonObjectNode, mode: GroupPolicyMode): JsonObjectNode {
  return setField(root, 'group_policy', stringLiteral(mode))
}

export function readVisualDocument(text: string): VisualDocumentResult {
  const issues: PolicyIssue[] = []
  if (text.trim() === '') {
    issues.push({ code: 'empty', path: '', fatal: true })
    return { rules: [], issues }
  }
  if (utf8ByteLength(text) > policyLimits.maxConfigBytes) {
    issues.push({ code: 'configBytes', path: '', fatal: true })
    return { rules: [], issues }
  }
  let root: JsonNode
  try {
    root = parseRawJson(text)
  } catch (error) {
    issues.push({
      code: 'syntax',
      path: '',
      fatal: true,
      detail: error instanceof Error ? error.message : undefined,
    })
    return { rules: [], issues }
  }
  if (root.type !== 'object') {
    issues.push({ code: 'rootType', path: '', fatal: true })
    return { rules: [], issues }
  }
  const schemaNode = getField(root, 'schema_version')
  const schemaRaw = literalNumberRaw(schemaNode)
  if (schemaRaw !== '1') {
    issues.push({ code: 'schemaVersion', path: 'schema_version', fatal: true })
    return { root, rules: [], issues }
  }
  // 仅标记该字段不可视化，不将整份配置判为不可编辑。
  if (!groupPolicyModeRepresentable(root)) {
    issues.push({ code: 'unsupported', path: 'group_policy', fatal: false })
  }
  const rulesNode = getField(root, 'rules')
  if (!rulesNode || rulesNode.type !== 'array') {
    issues.push({ code: 'rulesType', path: 'rules', fatal: true })
    return { root, rules: [], issues }
  }
  if (rulesNode.items.length > policyLimits.maxRulesPerConfig) {
    issues.push({ code: 'ruleCount', path: 'rules', fatal: true })
    return { root, rules: [], issues }
  }
  const rules: VisualRule[] = []
  let totalNodes = 0
  rulesNode.items.forEach((ruleNode, index) => {
    const counters: Counters = { nodes: 0 }
    if (ruleNode.type !== 'object') {
      issues.push({ code: 'ruleShape', path: `rules[${index}]`, fatal: false })
      return
    }
    const when = getField(ruleNode, 'when')
    if (when) inspectCondition(when, `rules[${index}].when`, 1, counters, issues)
    rules.push({ index, node: ruleNode })
    totalNodes += counters.nodes
  })
  if (totalNodes > policyLimits.maxNodesPerConfig) {
    issues.push({ code: 'nodePerConfig', path: 'rules', fatal: true })
  }
  return { root, rules, issues }
}

export function hasFatalIssue(issues: PolicyIssue[]): boolean {
  return issues.some((issue) => issue.fatal)
}

export function unsupportedPaths(issues: PolicyIssue[]): string[] {
  return issues
    .filter((issue) => issue.code === 'unsupported' || issue.code === 'ruleShape')
    .map((issue) => issue.path)
}

// ---- 编辑操作：全部返回新节点，避免就地修改共享状态。 ----

export function newRuleId(existing: Iterable<string>): string {
  const taken = new Set(existing)
  for (let index = 1; ; index++) {
    const candidate = `rule-${index}`
    if (!taken.has(candidate)) return candidate
  }
}

function defaultLeaf(): JsonObjectNode {
  // 新条件/新规则必须默认可编译，避免后端以“禁用规则也校验”为由整体拒绝。
  return newTimeWindowCondition()
}

export function newConditionGroup(kind: 'all' | 'any', children: JsonNode[] = []): JsonObjectNode {
  return objectNode([{ key: kind, value: arrayNode(children) }])
}

export function newNotCondition(child: JsonNode = defaultLeaf()): JsonObjectNode {
  return objectNode([{ key: 'not', value: child }])
}

export function newQuotaFactCondition(windowRaw: string): JsonObjectNode {
  return objectNode([
    { key: 'fact', value: stringLiteral(quotaFactKey) },
    {
      key: 'select',
      value: objectNode([
        { key: 'scope', value: stringLiteral(quotaScopeAccount) },
        { key: 'window_seconds', value: numberLiteral(windowRaw) ?? numberLiteral('1')! },
      ]),
    },
    { key: 'reduce', value: stringLiteral(quotaReduceMin) },
    { key: 'op', value: stringLiteral('lt') },
    { key: 'value', value: numberLiteral('0.1')! },
  ])
}

export function newFactCondition(fact: string, windowRaw?: string): JsonObjectNode {
  const definition = factDefinition(fact)
  if (!definition) return defaultLeaf()
  // 额度参数没有通用默认窗口，必须由调用方给出实际 catalog 窗口，否则回落到可编译默认叶子。
  if (definition.quota) return windowRaw ? newQuotaFactCondition(windowRaw) : defaultLeaf()
  const op = definition.operators[0]
  return objectNode([
    { key: 'fact', value: stringLiteral(fact) },
    { key: 'op', value: stringLiteral(op) },
    { key: 'value', value: stringLiteral('') },
  ])
}

export function newTimeWindowCondition(): JsonObjectNode {
  return objectNode([
    { key: 'predicate', value: stringLiteral('time_window') },
    { key: 'weekdays', value: arrayNode([numberLiteral('1')!]) },
    {
      key: 'ranges',
      value: arrayNode([arrayNode([stringLiteral('09:00'), stringLiteral('18:00')])]),
    },
  ])
}

export function newRule(id: string, domain: PolicyDomain): JsonObjectNode {
  return objectNode([
    { key: 'id', value: stringLiteral(id) },
    // 名称默认取合法的规则 ID，保证新建规则整体可编译；用户可再修改。
    { key: 'name', value: stringLiteral(id) },
    { key: 'domain', value: stringLiteral(domain) },
    { key: 'enabled', value: booleanLiteral(false) },
    { key: 'when', value: newConditionGroup('all', []) },
    {
      key: 'then',
      value:
        domain === 'pricing'
          ? objectNode([
              { key: 'type', value: stringLiteral('multiply_price') },
              { key: 'factor', value: stringLiteral('1') },
            ])
          : objectNode([{ key: 'type', value: stringLiteral('exclude_candidate') }]),
    },
  ])
}

export function duplicateRule(rule: VisualRule, existing: Iterable<string>): JsonObjectNode {
  const next = setField(rule.node, 'id', stringLiteral(newRuleId(existing)))
  return setField(next, 'enabled', booleanLiteral(false))
}

export function replaceRuleAt(
  root: JsonObjectNode,
  index: number,
  ruleNode: JsonNode,
): JsonObjectNode {
  const rulesNode = getField(root, 'rules')
  if (!rulesNode || rulesNode.type !== 'array') return root
  const items = rulesNode.items.map((item, at) => (at === index ? ruleNode : item))
  return setField(root, 'rules', arrayNode(items))
}

export function insertRule(root: JsonObjectNode, ruleNode: JsonNode): JsonObjectNode {
  const rulesNode = getField(root, 'rules')
  const items =
    rulesNode && rulesNode.type === 'array' ? [...rulesNode.items, ruleNode] : [ruleNode]
  return setField(root, 'rules', arrayNode(items))
}

export function insertRuleAfter(
  root: JsonObjectNode,
  index: number,
  ruleNode: JsonNode,
): JsonObjectNode {
  const rulesNode = getField(root, 'rules')
  if (!rulesNode || rulesNode.type !== 'array') return insertRule(root, ruleNode)
  const items = [...rulesNode.items]
  items.splice(index + 1, 0, ruleNode)
  return setField(root, 'rules', arrayNode(items))
}

export function removeRuleAt(root: JsonObjectNode, index: number): JsonObjectNode {
  const rulesNode = getField(root, 'rules')
  if (!rulesNode || rulesNode.type !== 'array') return root
  return setField(root, 'rules', arrayNode(rulesNode.items.filter((_, at) => at !== index)))
}

export function moveRule(root: JsonObjectNode, from: number, to: number): JsonObjectNode {
  const rulesNode = getField(root, 'rules')
  if (!rulesNode || rulesNode.type !== 'array') return root
  const items = [...rulesNode.items]
  if (from < 0 || from >= items.length || to < 0 || to >= items.length) return root
  const [moved] = items.splice(from, 1)
  items.splice(to, 0, moved)
  return setField(root, 'rules', arrayNode(items))
}
