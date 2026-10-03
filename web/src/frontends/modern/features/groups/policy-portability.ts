// 可组合策略的可移植 JSON 操作：导出、导入、复制与原始片段扫描。
//
// 该模块只做前端可移植性所需的原始 JSON 读写：
// - 不调用后端语义编译器；when/then 等条件保持原始文本，不规范化、不丢弃未知结构。
// - 不使用 JSON.parse -> JSON.stringify 往返，数字字面量（如 0.10000000000000001）按原样切片保留。
// - 通用读取器接受 null，只在必填的根/规则字段处按类型拒绝；已识别的管理信封字段即使为 null
//   也按“丢弃+警告”处理，未知 when/then 内的 null 按用户内容原样保留。
// - 只在根层按字段名丢弃已识别的信封字段；不递归清洗规则内的键名（规则内的 secret/token 等
//   属于用户内容，不声称做密钥扫描）。
// - 只校验内层策略正文的顶层与规则信封结构、重复键、预算与条件节点上限。
//   外层管理传输信封的深度 32 限制由网关单独实施，不在此模块职责内。
//   此处的校验是前端可移植性检查，不构成服务端保存保证；后端仍是权威语义编译器。
import { createUUID } from '@shared/uuid'

export const POLICY_MAX_CONFIG_BYTES = 256 << 10
export const POLICY_MAX_JSON_DEPTH = 64
export const POLICY_MAX_RULES = 100
export const POLICY_MAX_ID_LENGTH = 64
export const POLICY_MAX_NAME_LENGTH = 128
export const POLICY_MAX_CONDITION_DEPTH = 16
export const POLICY_MAX_NODES_PER_RULE = 256
export const POLICY_MAX_NODES_PER_CONFIG = 4096

export type PolicyDomain = 'scheduling' | 'pricing'
export type PolicyImportMode = 'append' | 'replace'

/** 生成新规则 ID；existing 为当前配置内已占用的 ID，preferred 为期望 ID（如原 ID）。 */
export type PolicyIDFactory = (existing: readonly string[], preferred: string) => string

export interface PolicyIssue {
  /** 与后端编译器一致的稳定错误码（如 ERR_UNKNOWN_FIELD）；warnings 使用前端自有码。 */
  code: string
  path: string
  message: string
}

export interface PolicyPortabilityResult {
  /** 可移植原始 JSON 文本；数字字面量按原样保留。 */
  text: string
  warnings: PolicyIssue[]
}

export interface PolicyRename {
  from: string
  to: string
}

export interface PolicyImportResult extends PolicyPortabilityResult {
  imported: number
  renamed: PolicyRename[]
}

export interface PolicyRuleSpan {
  index: number
  id: string
  domain: PolicyDomain
  enabled: boolean
  /** 规则对象 '{' 的绝对偏移（含）。 */
  start: number
  /** 规则对象 '}' 之后的绝对偏移（不含）。 */
  end: number
  /** 精确源切片，数字字面量保持不变。 */
  raw: string
}

export interface ScannedPolicyConfig {
  /** schema_version 的精确源词法片段。 */
  schemaVersionRaw: string
  /** rules 数组 '[' 的绝对偏移（含）。 */
  rulesStart: number
  /** rules 数组 ']' 之后的绝对偏移（不含）。 */
  rulesEnd: number
  rules: PolicyRuleSpan[]
}

export class PolicyPortabilityError extends Error {
  readonly issues: readonly PolicyIssue[]

  constructor(issues: readonly PolicyIssue[]) {
    super(issues[0]?.message ?? 'invalid policy configuration')
    this.name = 'PolicyPortabilityError'
    this.issues = issues
  }
}

// 与 internal/policy 的 ValidationError 码保持一致，便于前端映射后端提示。
const ERR_INVALID_VALUE = 'ERR_INVALID_VALUE'
const ERR_DUPLICATE_KEY = 'ERR_DUPLICATE_KEY'
const ERR_UNKNOWN_FIELD = 'ERR_UNKNOWN_FIELD'
const ERR_INVALID_TYPE = 'ERR_INVALID_TYPE'
const ERR_MISSING_FIELD = 'ERR_MISSING_FIELD'
const ERR_BUDGET_EXCEEDED = 'ERR_BUDGET_EXCEEDED'

// 导出时可安全丢弃的管理信封字段；其余未知顶层字段一律拒绝，避免夹带密钥或静默丢失。
const NON_PORTABLE_ENVELOPE_FIELDS = new Set([
  'scope',
  'id',
  'group_id',
  'credential_id',
  'binding_scope',
  'apply_mode',
  'revision',
  'revision_text',
  'config',
  'config_text',
  'observation',
  'observations',
  'secret',
  'secrets',
  'token',
  'tokens',
  'access_key',
  'access_key_id',
  'credential_key',
  'session_key',
  'affinity_key',
])

function fail(path: string, code: string, message: string): never {
  throw new PolicyPortabilityError([{ code, path, message }])
}

function joinPath(base: string, key: string): string {
  return base === '' ? key : `${base}.${key}`
}

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

const ID_PATTERN = /^[A-Za-z0-9_-]+$/
const NUMBER_PATTERN = /^(?:-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/
const JSON_WHITESPACE = new Set([' ', '\t', '\n', '\r'])
const RULE_FIELDS = ['id', 'name', 'domain', 'enabled', 'when', 'then'] as const

interface JsonNodeBase {
  start: number
  end: number
}

interface JsonMember {
  key: string
  value: JsonNode
}

interface JsonObjectNode extends JsonNodeBase {
  kind: 'object'
  members: JsonMember[]
  byKey: Map<string, JsonNode>
}

interface JsonArrayNode extends JsonNodeBase {
  kind: 'array'
  elements: JsonNode[]
}

interface JsonStringNode extends JsonNodeBase {
  kind: 'string'
  value: string
}

interface JsonNumberNode extends JsonNodeBase {
  kind: 'number'
  raw: string
}

interface JsonBooleanNode extends JsonNodeBase {
  kind: 'boolean'
  value: boolean
}

interface JsonNullNode extends JsonNodeBase {
  kind: 'null'
}

type JsonNode =
  JsonObjectNode | JsonArrayNode | JsonStringNode | JsonNumberNode | JsonBooleanNode | JsonNullNode

class JsonReader {
  private index = 0

  constructor(private readonly text: string) {}

  parseRoot(): JsonNode {
    this.skipWhitespace()
    if (this.index >= this.text.length) {
      fail('', ERR_INVALID_VALUE, 'policy config is blank')
    }
    const node = this.readValue(1, '')
    this.skipWhitespace()
    if (this.index !== this.text.length) {
      fail('', ERR_INVALID_VALUE, 'unexpected trailing JSON content')
    }
    return node
  }

  private skipWhitespace(): void {
    while (this.index < this.text.length && JSON_WHITESPACE.has(this.text[this.index]!)) {
      this.index++
    }
  }

  private readValue(depth: number, path: string): JsonNode {
    if (depth > POLICY_MAX_JSON_DEPTH) {
      fail(path, ERR_BUDGET_EXCEEDED, `JSON nesting depth exceeds maximum ${POLICY_MAX_JSON_DEPTH}`)
    }
    const char = this.text[this.index]
    if (char === '{') return this.readObject(depth, path)
    if (char === '[') return this.readArray(depth, path)
    if (char === '"') return this.readString(path)
    if (char === 't' || char === 'f') return this.readBoolean(path)
    if (char === 'n') return this.readNull(path)
    if (char === '-' || (char !== undefined && char >= '0' && char <= '9')) {
      return this.readNumber(path)
    }
    return fail(path, ERR_INVALID_VALUE, `unexpected JSON token ${JSON.stringify(char)}`)
  }

  private readObject(depth: number, path: string): JsonObjectNode {
    const start = this.index
    this.index++
    const members: JsonMember[] = []
    const byKey = new Map<string, JsonNode>()
    this.skipWhitespace()
    if (this.text[this.index] === '}') {
      this.index++
      return { kind: 'object', start, end: this.index, members, byKey }
    }
    for (;;) {
      this.skipWhitespace()
      if (this.text[this.index] !== '"') {
        fail(path, ERR_INVALID_VALUE, 'object key must be a string')
      }
      const key = this.readString(path).value
      const memberPath = joinPath(path, key)
      if (byKey.has(key)) {
        fail(memberPath, ERR_DUPLICATE_KEY, `duplicate field ${JSON.stringify(key)}`)
      }
      this.skipWhitespace()
      if (this.text[this.index] !== ':') {
        fail(memberPath, ERR_INVALID_VALUE, 'expected ":" after object key')
      }
      this.index++
      this.skipWhitespace()
      const value = this.readValue(depth + 1, memberPath)
      members.push({ key, value })
      byKey.set(key, value)
      this.skipWhitespace()
      const delimiter = this.text[this.index]
      if (delimiter === ',') {
        this.index++
        continue
      }
      if (delimiter === '}') {
        this.index++
        return { kind: 'object', start, end: this.index, members, byKey }
      }
      fail(memberPath, ERR_INVALID_VALUE, 'expected "," or "}" in object')
    }
  }

  private readArray(depth: number, path: string): JsonArrayNode {
    const start = this.index
    this.index++
    const elements: JsonNode[] = []
    this.skipWhitespace()
    if (this.text[this.index] === ']') {
      this.index++
      return { kind: 'array', start, end: this.index, elements }
    }
    for (;;) {
      this.skipWhitespace()
      const elementPath = `${path}[${elements.length}]`
      elements.push(this.readValue(depth + 1, elementPath))
      this.skipWhitespace()
      const delimiter = this.text[this.index]
      if (delimiter === ',') {
        this.index++
        continue
      }
      if (delimiter === ']') {
        this.index++
        return { kind: 'array', start, end: this.index, elements }
      }
      fail(elementPath, ERR_INVALID_VALUE, 'expected "," or "]" in array')
    }
  }

  private readString(path: string): JsonStringNode {
    const start = this.index
    this.index++
    while (this.index < this.text.length) {
      const char = this.text[this.index]
      if (char === '"') {
        this.index++
        const raw = this.text.slice(start, this.index)
        try {
          return { kind: 'string', start, end: this.index, value: JSON.parse(raw) as string }
        } catch {
          return fail(path, ERR_INVALID_VALUE, 'invalid string literal')
        }
      }
      if (char === '\\') {
        this.index += 2
        continue
      }
      this.index++
    }
    return fail(path, ERR_INVALID_VALUE, 'unterminated string literal')
  }

  private readNumber(path: string): JsonNumberNode {
    const match = NUMBER_PATTERN.exec(this.text.slice(this.index))
    if (!match) {
      fail(path, ERR_INVALID_VALUE, 'invalid number literal')
    }
    const raw = match[0]
    const start = this.index
    this.index += raw.length
    return { kind: 'number', start, end: this.index, raw }
  }

  private readBoolean(path: string): JsonBooleanNode {
    const start = this.index
    if (this.text.startsWith('true', this.index)) {
      this.index += 4
      return { kind: 'boolean', start, end: this.index, value: true }
    }
    if (this.text.startsWith('false', this.index)) {
      this.index += 5
      return { kind: 'boolean', start, end: this.index, value: false }
    }
    return fail(path, ERR_INVALID_VALUE, 'invalid boolean literal')
  }

  private readNull(path: string): JsonNullNode {
    const start = this.index
    if (!this.text.startsWith('null', this.index)) {
      return fail(path, ERR_INVALID_VALUE, 'invalid null literal')
    }
    this.index += 4
    return { kind: 'null', start, end: this.index }
  }
}

interface ConditionStats {
  depth: number
  nodes: number
}

// 只识别已注册的条件形态来计数；未知结构按叶节点计数并保留原文，交由后端编译判定。
function conditionStats(node: JsonNode): ConditionStats {
  if (node.kind !== 'object') return { depth: 1, nodes: 1 }
  const children = node.byKey.get('all') ?? node.byKey.get('any')
  if (children?.kind === 'array') {
    let depth = 1
    let nodes = 1
    for (const child of children.elements) {
      const stats = conditionStats(child)
      depth = Math.max(depth, 1 + stats.depth)
      nodes += stats.nodes
    }
    return { depth, nodes }
  }
  const child = node.byKey.get('not')
  if (child) {
    const stats = conditionStats(child)
    return { depth: 1 + stats.depth, nodes: 1 + stats.nodes }
  }
  return { depth: 1, nodes: 1 }
}

interface ParsedRule {
  index: number
  id: string
  idStart: number
  idEnd: number
  domain: PolicyDomain
  enabled: boolean
  enabledStart: number
  enabledEnd: number
  conditionNodes: number
  start: number
  end: number
  raw: string
}

interface ParsedConfig {
  schemaVersionRaw: string
  rulesArray: JsonArrayNode
  rules: ParsedRule[]
}

interface ParseOutcome {
  parsed: ParsedConfig
  warnings: PolicyIssue[]
}

function requiredMember(node: JsonObjectNode, key: string, path: string): JsonNode {
  const value = node.byKey.get(key)
  if (!value) {
    fail(joinPath(path, key), ERR_MISSING_FIELD, `missing required field ${JSON.stringify(key)}`)
  }
  return value
}

function readRule(node: JsonObjectNode, index: number, path: string, text: string): ParsedRule {
  for (const member of node.members) {
    if (!(RULE_FIELDS as readonly string[]).includes(member.key)) {
      fail(
        joinPath(path, member.key),
        ERR_UNKNOWN_FIELD,
        `unknown field ${JSON.stringify(member.key)} in rule`,
      )
    }
  }

  const idNode = requiredMember(node, 'id', path)
  if (idNode.kind !== 'string') {
    fail(joinPath(path, 'id'), ERR_INVALID_TYPE, 'rule id must be a string')
  }
  const id = idNode.value
  if (id.length === 0) {
    fail(joinPath(path, 'id'), ERR_INVALID_VALUE, 'rule id cannot be empty')
  }
  if (id.length > POLICY_MAX_ID_LENGTH) {
    fail(
      joinPath(path, 'id'),
      ERR_BUDGET_EXCEEDED,
      `rule id exceeds ${POLICY_MAX_ID_LENGTH} characters`,
    )
  }
  if (!ID_PATTERN.test(id)) {
    fail(
      joinPath(path, 'id'),
      ERR_INVALID_VALUE,
      `rule id ${JSON.stringify(id)} must match ^[A-Za-z0-9_-]+$`,
    )
  }

  const nameNode = requiredMember(node, 'name', path)
  if (nameNode.kind !== 'string') {
    fail(joinPath(path, 'name'), ERR_INVALID_TYPE, 'rule name must be a string')
  }
  const name = nameNode.value
  if (name.length === 0 || name.trim() !== name) {
    fail(
      joinPath(path, 'name'),
      ERR_INVALID_VALUE,
      'rule name cannot be blank or padded with whitespace',
    )
  }
  if ([...name].length > POLICY_MAX_NAME_LENGTH) {
    fail(
      joinPath(path, 'name'),
      ERR_BUDGET_EXCEEDED,
      `rule name exceeds ${POLICY_MAX_NAME_LENGTH} characters`,
    )
  }

  const domainNode = requiredMember(node, 'domain', path)
  if (
    domainNode.kind !== 'string' ||
    (domainNode.value !== 'scheduling' && domainNode.value !== 'pricing')
  ) {
    fail(
      joinPath(path, 'domain'),
      ERR_INVALID_VALUE,
      'rule domain must be "scheduling" or "pricing"',
    )
  }
  const domain: PolicyDomain = domainNode.value

  const enabledNode = requiredMember(node, 'enabled', path)
  if (enabledNode.kind !== 'boolean') {
    fail(joinPath(path, 'enabled'), ERR_INVALID_TYPE, 'rule enabled must be a boolean')
  }

  const whenNode = requiredMember(node, 'when', path)
  if (whenNode.kind !== 'object') {
    fail(joinPath(path, 'when'), ERR_INVALID_TYPE, 'rule when must be an object')
  }
  const whenStats = conditionStats(whenNode)
  if (whenStats.depth > POLICY_MAX_CONDITION_DEPTH) {
    fail(
      joinPath(path, 'when'),
      ERR_BUDGET_EXCEEDED,
      `condition depth ${whenStats.depth} exceeds maximum ${POLICY_MAX_CONDITION_DEPTH}`,
    )
  }
  if (whenStats.nodes > POLICY_MAX_NODES_PER_RULE) {
    fail(
      joinPath(path, 'when'),
      ERR_BUDGET_EXCEEDED,
      `condition node count ${whenStats.nodes} exceeds maximum ${POLICY_MAX_NODES_PER_RULE} per rule`,
    )
  }

  const thenNode = requiredMember(node, 'then', path)
  if (thenNode.kind !== 'object') {
    fail(joinPath(path, 'then'), ERR_INVALID_TYPE, 'rule then must be an object')
  }

  return {
    index,
    id,
    idStart: idNode.start,
    idEnd: idNode.end,
    domain,
    enabled: enabledNode.value,
    enabledStart: enabledNode.start,
    enabledEnd: enabledNode.end,
    conditionNodes: whenStats.nodes,
    start: node.start,
    end: node.end,
    raw: text.slice(node.start, node.end),
  }
}

function parsePolicyConfig(rawConfig: string, allowEnvelopeExtras: boolean): ParseOutcome {
  if (typeof rawConfig !== 'string') {
    fail('', ERR_INVALID_TYPE, 'policy config must be a string')
  }
  if (utf8ByteLength(rawConfig) > POLICY_MAX_CONFIG_BYTES) {
    fail('', ERR_BUDGET_EXCEEDED, `config exceeds ${POLICY_MAX_CONFIG_BYTES} bytes`)
  }

  const root = new JsonReader(rawConfig).parseRoot()
  if (root.kind !== 'object') {
    fail('', ERR_INVALID_TYPE, 'policy config root must be an object')
  }

  const warnings: PolicyIssue[] = []
  for (const member of root.members) {
    if (member.key === 'schema_version' || member.key === 'rules') continue
    if (!allowEnvelopeExtras || !NON_PORTABLE_ENVELOPE_FIELDS.has(member.key)) {
      fail(
        member.key,
        ERR_UNKNOWN_FIELD,
        `unknown root field ${JSON.stringify(member.key)}; only schema_version and rules are portable`,
      )
    }
    warnings.push({
      code: 'dropped_field',
      path: member.key,
      message: `dropped non-portable root field ${JSON.stringify(member.key)}`,
    })
  }

  const schemaNode = requiredMember(root, 'schema_version', '')
  if (schemaNode.kind !== 'number') {
    fail('schema_version', ERR_INVALID_TYPE, 'schema_version must be integer 1')
  }
  if (schemaNode.raw !== '1') {
    fail('schema_version', ERR_INVALID_VALUE, `unsupported schema_version ${schemaNode.raw}`)
  }

  const rulesNode = requiredMember(root, 'rules', '')
  if (rulesNode.kind !== 'array') {
    fail('rules', ERR_INVALID_TYPE, 'rules must be an array')
  }
  if (rulesNode.elements.length > POLICY_MAX_RULES) {
    fail('rules', ERR_BUDGET_EXCEEDED, `rule count exceeds maximum ${POLICY_MAX_RULES}`)
  }

  const seenIDs = new Set<string>()
  const rules: ParsedRule[] = []
  let totalNodes = 0
  rulesNode.elements.forEach((element, index) => {
    const path = `rules[${index}]`
    if (element.kind !== 'object') {
      fail(path, ERR_INVALID_TYPE, 'rule must be an object')
    }
    const rule = readRule(element, index, path, rawConfig)
    if (seenIDs.has(rule.id)) {
      fail(`${path}.id`, ERR_DUPLICATE_KEY, `duplicate rule id ${JSON.stringify(rule.id)}`)
    }
    seenIDs.add(rule.id)
    totalNodes += rule.conditionNodes
    if (totalNodes > POLICY_MAX_NODES_PER_CONFIG) {
      fail(
        path,
        ERR_BUDGET_EXCEEDED,
        `total condition nodes exceed maximum ${POLICY_MAX_NODES_PER_CONFIG} per config`,
      )
    }
    rules.push(rule)
  })

  return {
    parsed: { schemaVersionRaw: schemaNode.raw, rulesArray: rulesNode, rules },
    warnings,
  }
}

function buildConfig(schemaVersionRaw: string, ruleRaws: readonly string[]): string {
  return `{"schema_version":${schemaVersionRaw},"rules":[${ruleRaws.join(',')}]}`
}

// 生成输出后重新按严格模式解析，确保字节/深度/规则/节点预算在返回前满足。
function assertPortableOutput(text: string): void {
  parsePolicyConfig(text, false)
}

function ruleTextWithIdentity(
  text: string,
  rule: ParsedRule,
  id: string,
  enabled: boolean,
): string {
  let raw = text.slice(rule.start, rule.end)
  const edits = [
    { start: rule.idStart - rule.start, end: rule.idEnd - rule.start, value: JSON.stringify(id) },
    {
      start: rule.enabledStart - rule.start,
      end: rule.enabledEnd - rule.start,
      value: enabled ? 'true' : 'false',
    },
  ].sort((a, b) => b.start - a.start)
  for (const edit of edits) {
    raw = raw.slice(0, edit.start) + edit.value + raw.slice(edit.end)
  }
  return raw
}

function assertGeneratedID(
  id: unknown,
  existing: ReadonlySet<string>,
  path: string,
): asserts id is string {
  if (
    typeof id !== 'string' ||
    id.length === 0 ||
    id.length > POLICY_MAX_ID_LENGTH ||
    !ID_PATTERN.test(id) ||
    existing.has(id)
  ) {
    throw new PolicyPortabilityError([
      {
        code: 'invalid_id',
        path,
        message: `ID factory returned an invalid or colliding id ${JSON.stringify(id)}`,
      },
    ])
  }
}

function defaultPolicyIDFactory(existing: readonly string[]): string {
  const taken = new Set(existing)
  for (let attempt = 0; attempt < 128; attempt++) {
    const candidate = createUUID()
    if (!taken.has(candidate)) return candidate
  }
  throw new PolicyPortabilityError([
    { code: 'id_generation_failed', path: 'rules', message: 'unable to generate a unique rule id' },
  ])
}

function mergeRuleRaws(
  existing: readonly ParsedRule[],
  imported: readonly string[],
  domain: PolicyDomain,
  mode: PolicyImportMode,
): string[] {
  if (mode === 'append') {
    return [...existing.map((rule) => rule.raw), ...imported]
  }
  const merged: string[] = []
  let inserted = false
  for (const rule of existing) {
    if (rule.domain === domain) {
      if (!inserted) {
        merged.push(...imported)
        inserted = true
      }
      continue
    }
    merged.push(rule.raw)
  }
  if (!inserted) {
    merged.push(...imported)
  }
  return merged
}

/** 扫描配置并返回规则的原始绝对偏移片段；结构错误抛出 PolicyPortabilityError。 */
export function scanPolicyConfig(rawConfig: string): ScannedPolicyConfig {
  const { parsed } = parsePolicyConfig(rawConfig, false)
  return {
    schemaVersionRaw: parsed.schemaVersionRaw,
    rulesStart: parsed.rulesArray.start,
    rulesEnd: parsed.rulesArray.end,
    rules: parsed.rules.map((rule) => ({
      index: rule.index,
      id: rule.id,
      domain: rule.domain,
      enabled: rule.enabled,
      start: rule.start,
      end: rule.end,
      raw: rule.raw,
    })),
  }
}

/** 校验可移植配置结构；返回结构化 issue（含后端一致错误码），不抛异常。 */
export function validatePortablePolicy(rawConfig: string): PolicyIssue[] {
  try {
    parsePolicyConfig(rawConfig, false)
    return []
  } catch (error) {
    if (error instanceof PolicyPortabilityError) return [...error.issues]
    throw error
  }
}

/** 导出可移植正文：只保留 schema_version 与 rules，丢弃已识别的管理信封字段并给出警告。 */
export function exportPortablePolicy(rawConfig: string): PolicyPortabilityResult {
  const { parsed, warnings } = parsePolicyConfig(rawConfig, true)
  const text = buildConfig(
    parsed.schemaVersionRaw,
    parsed.rules.map((rule) => rule.raw),
  )
  assertPortableOutput(text)
  return { text, warnings }
}

/**
 * 导入可移植正文：把 source 中指定执行域的规则并入 target。
 * mode='append' 追加；mode='replace' 只替换该域现有规则并保留其他域顺序。
 * 导入规则一律 enabled=false；ID 全局冲突时使用 newID 生成新 ID 并返回映射。
 */
export function importPortablePolicy(
  targetConfig: string,
  sourceConfig: string,
  options: { domain: PolicyDomain; mode?: PolicyImportMode; newID?: PolicyIDFactory },
): PolicyImportResult {
  const { domain, mode = 'append', newID = defaultPolicyIDFactory } = options
  if (domain !== 'scheduling' && domain !== 'pricing') {
    fail('domain', ERR_INVALID_VALUE, `unsupported import domain ${String(domain)}`)
  }
  if (mode !== 'append' && mode !== 'replace') {
    fail('mode', ERR_INVALID_VALUE, `unsupported import mode ${String(mode)}`)
  }

  const target = parsePolicyConfig(targetConfig, false).parsed
  const source = parsePolicyConfig(sourceConfig, false).parsed
  const existing = new Set(target.rules.map((rule) => rule.id))
  const warnings: PolicyIssue[] = []
  const renamed: PolicyRename[] = []
  const importedRaws: string[] = []

  for (const rule of source.rules) {
    if (rule.domain !== domain) continue
    let id = rule.id
    if (existing.has(id)) {
      id = newID([...existing], rule.id)
      assertGeneratedID(id, existing, `rules[${rule.index}].id`)
      renamed.push({ from: rule.id, to: id })
    }
    existing.add(id)
    importedRaws.push(ruleTextWithIdentity(sourceConfig, rule, id, false))
  }

  if (
    mode === 'replace' &&
    importedRaws.length === 0 &&
    target.rules.some((rule) => rule.domain === domain)
  ) {
    warnings.push({
      code: 'replace_removed_domain',
      path: 'rules',
      message: `replacement removed all ${domain} rules because the source had none`,
    })
  }

  const merged = mergeRuleRaws(target.rules, importedRaws, domain, mode)
  const text = buildConfig(target.schemaVersionRaw, merged)
  assertPortableOutput(text)
  return { text, warnings, imported: importedRaws.length, renamed }
}

/** 复制指定规则：插入到原规则之后，使用 newID 生成新 ID，enabled=false。 */
export function copyPolicyRule(
  rawConfig: string,
  ruleIndex: number,
  options: { newID?: PolicyIDFactory } = {},
): PolicyPortabilityResult {
  const { newID = defaultPolicyIDFactory } = options
  const parsed = parsePolicyConfig(rawConfig, false).parsed
  if (!Number.isInteger(ruleIndex) || ruleIndex < 0 || ruleIndex >= parsed.rules.length) {
    fail(`rules[${String(ruleIndex)}]`, ERR_INVALID_VALUE, 'rule index is out of range')
  }
  const rule = parsed.rules[ruleIndex]!
  const existing = new Set(parsed.rules.map((item) => item.id))
  const id = newID([...existing], rule.id)
  assertGeneratedID(id, existing, `rules[${ruleIndex}].id`)

  if (parsed.rules.length + 1 > POLICY_MAX_RULES) {
    fail('rules', ERR_BUDGET_EXCEEDED, `rule count exceeds maximum ${POLICY_MAX_RULES}`)
  }

  const raws = parsed.rules.map((item) => item.raw)
  raws.splice(ruleIndex + 1, 0, ruleTextWithIdentity(rawConfig, rule, id, false))
  const text = buildConfig(parsed.schemaVersionRaw, raws)
  assertPortableOutput(text)
  return { text, warnings: [] }
}
