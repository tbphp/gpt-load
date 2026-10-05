// 可组合策略可视化编辑器的纯逻辑层。
// 只依赖标准 JavaScript，不引用 Vue、路由、既有面板或业务 API，可由 Node 做类型擦除执行。
// 模型是原生 JSON 对象上的扁平结构：规则持有 match(all|any) + 平铺 conditions + actions，
// 解析/序列化直接用 JSON.parse / JSON.stringify，不做自定义词法或 AST。
// 无法扁平表示的嵌套/未知结构整体退化为只读 JSON 模式，不丢失原始正文。

export type PolicyDomain = 'scheduling' | 'pricing'
export type PolicyMatch = 'all' | 'any'
export type GroupPolicyMode = 'inherit' | 'override'

export interface PolicyLimits {
  maxConfigBytes: number
  maxRulesPerConfig: number
  maxListItems: number
  maxNameLength: number
  maxActionsPerRule: number
}

export const policyLimits: PolicyLimits = {
  maxConfigBytes: 256 << 10,
  maxRulesPerConfig: 100,
  maxListItems: 100,
  maxNameLength: 128,
  maxActionsPerRule: 16,
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

function utf8ByteLength(text: string): number {
  return new TextEncoder().encode(text).length
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

// 复刻后端 pricing.ParsePriceMultiplier 的范围与精度：0..1000，最多六位小数。
export function isMultiplierRaw(raw: string): boolean {
  if (!/^\d+(?:\.\d+)?$/.test(raw)) return false
  const dot = raw.indexOf('.')
  const integer = dot < 0 ? raw : raw.slice(0, dot)
  const fraction = dot < 0 ? '' : raw.slice(dot + 1)
  if (fraction.length > 6) return false
  const whole = BigInt(integer)
  if (whole > 1000n) return false
  if (whole === 1000n && /[1-9]/.test(fraction)) return false
  return true
}

// 额度比例按数值语义判定：允许指数与浮点舍入，与后端 float64 比较保持一致。
export function isRatio(value: number): boolean {
  return Number.isFinite(value) && value >= 0 && value <= 1
}

// 时间段 HH:MM，00:00..23:59；开始不等于结束（首批不允许 24:00 或全天常量）。
export function isTimeOfDay(raw: string): boolean {
  const match = raw.match(/^(\d{2}):(\d{2})$/)
  if (!match) return false
  const hours = Number(match[1])
  const minutes = Number(match[2])
  return hours <= 23 && minutes <= 59
}

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
    operators: ['eq', 'in'],
    quota: false,
    labelKey: 'policyEditor.fact.facts.requestModel',
  },
  {
    key: 'upstream.model',
    valueType: 'string',
    operators: ['eq', 'in'],
    quota: false,
    labelKey: 'policyEditor.fact.facts.upstreamModel',
  },
  {
    key: quotaFactKey,
    valueType: 'number',
    operators: ['eq', 'lt', 'lte', 'gt', 'gte'],
    quota: true,
    labelKey: 'policyEditor.fact.facts.quotaRemainingRatio',
  },
]

export function factDefinition(key: string | undefined): FactDefinition | undefined {
  return factDefinitions.find((definition) => definition.key === key)
}

// ---- 扁平可视化模型 ----

export interface VisualParamCondition {
  kind: 'param'
  fact: string
  op: string
  // eq 为字符串；in 为字符串数组；额度比例为 number。
  value: string | number | string[]
  // 仅额度参数使用：选择的时间窗口（秒）。
  windowSeconds?: number
}

export interface VisualTimeWindowCondition {
  kind: 'time_window'
  weekdays: number[]
  ranges: Array<[string, string]>
}

// 为未来的复杂表达式预留的占位条件；后端暂不支持，前端只做透传编辑。
export interface VisualExpressionCondition {
  kind: 'expression'
  expression: string
}

export type VisualCondition =
  | VisualParamCondition
  | VisualTimeWindowCondition
  | VisualExpressionCondition

export type VisualAction =
  | { type: 'exclude_candidate' }
  | { type: 'exclude_models'; models: string[] }
  | { type: 'multiply_price'; factor: string }

export interface VisualRule {
  id: string
  name: string
  domain: PolicyDomain
  enabled: boolean
  match: PolicyMatch
  conditions: VisualCondition[]
  actions: VisualAction[]
}

export interface VisualDocument {
  groupPolicy: GroupPolicyMode
  rules: VisualRule[]
}

export type PolicyIssueCode =
  | 'empty'
  | 'syntax'
  | 'rootType'
  | 'schemaVersion'
  | 'rulesType'
  | 'ruleCount'
  | 'configBytes'
  | 'advanced'

export interface PolicyIssue {
  code: PolicyIssueCode
  path: string
  fatal: boolean
  detail?: string
}

export interface VisualDocumentResult {
  // 扁平结构解析成功时的可视化文档。
  document?: VisualDocument
  // 结构过深/含未知字段时的原始正文，供只读 JSON 模式展示。
  advancedText?: string
  issues: PolicyIssue[]
}

function representCondition(raw: unknown): VisualCondition | undefined {
  if (!isPlainObject(raw)) return undefined
  const keys = Object.keys(raw)
  if (typeof raw.expression === 'string') {
    return keys.length === 1 ? { kind: 'expression', expression: raw.expression } : undefined
  }
  if (raw.predicate !== undefined) {
    if (keys.length !== 3 || raw.predicate !== 'time_window') return undefined
    if (!isWeekdayList(raw.weekdays) || !isRangeList(raw.ranges)) return undefined
    return { kind: 'time_window', weekdays: [...raw.weekdays], ranges: raw.ranges.map((r) => [...r]) }
  }
  if (typeof raw.fact !== 'string' || typeof raw.op !== 'string') return undefined
  const definition = factDefinition(raw.fact)
  if (!definition || !definition.operators.includes(raw.op)) return undefined
  if (definition.quota) {
    if (keys.length !== 5 || !keys.includes('select') || !keys.includes('reduce')) return undefined
    if (!isPlainObject(raw.select) || Object.keys(raw.select).length !== 2) return undefined
    if (raw.select.scope !== quotaScopeAccount || raw.reduce !== quotaReduceMin) return undefined
    const windowSeconds = raw.select.window_seconds
    if (!Number.isInteger(windowSeconds) || (windowSeconds as number) <= 0) return undefined
    if (typeof raw.value !== 'number' || !isRatio(raw.value)) return undefined
    return {
      kind: 'param',
      fact: raw.fact,
      op: raw.op,
      value: raw.value,
      windowSeconds: windowSeconds as number,
    }
  }
  if (keys.length !== 3) return undefined
  if (raw.op === 'in') {
    if (!Array.isArray(raw.value) || !raw.value.every((item) => typeof item === 'string')) {
      return undefined
    }
    return { kind: 'param', fact: raw.fact, op: 'in', value: [...raw.value] }
  }
  if (typeof raw.value !== 'string') return undefined
  return { kind: 'param', fact: raw.fact, op: raw.op, value: raw.value }
}

function isWeekdayList(value: unknown): value is number[] {
  return (
    Array.isArray(value) &&
    value.length > 0 &&
    value.length <= 7 &&
    value.every((item) => Number.isInteger(item) && item >= 0 && item <= 6)
  )
}

function isRangeList(value: unknown): value is Array<[string, string]> {
  return (
    Array.isArray(value) &&
    value.length > 0 &&
    value.length <= policyLimits.maxListItems &&
    value.every(
      (item) =>
        Array.isArray(item) &&
        item.length === 2 &&
        typeof item[0] === 'string' &&
        typeof item[1] === 'string',
    )
  )
}

function representAction(raw: unknown): VisualAction | undefined {
  if (!isPlainObject(raw)) return undefined
  const keys = Object.keys(raw)
  if (raw.type === 'exclude_candidate') {
    return keys.length === 1 ? { type: 'exclude_candidate' } : undefined
  }
  if (raw.type === 'exclude_models') {
    if (keys.length !== 2 || !Array.isArray(raw.models)) return undefined
    if (!raw.models.every((item) => typeof item === 'string')) return undefined
    return { type: 'exclude_models', models: [...raw.models] }
  }
  if (raw.type === 'multiply_price') {
    if (keys.length !== 2 || typeof raw.factor !== 'string' || !isMultiplierRaw(raw.factor)) {
      return undefined
    }
    return { type: 'multiply_price', factor: raw.factor }
  }
  return undefined
}

function representRule(raw: unknown): VisualRule | undefined {
  if (!isPlainObject(raw)) return undefined
  const keys = Object.keys(raw)
  if (keys.some((key) => !['id', 'name', 'domain', 'enabled', 'when', 'actions'].includes(key))) {
    return undefined
  }
  if (typeof raw.id !== 'string' || typeof raw.name !== 'string') return undefined
  if (raw.domain !== 'scheduling' && raw.domain !== 'pricing') return undefined
  if (typeof raw.enabled !== 'boolean') return undefined
  if (!isPlainObject(raw.when)) return undefined
  const matchKeys = Object.keys(raw.when)
  if (matchKeys.length !== 1 || (matchKeys[0] !== 'all' && matchKeys[0] !== 'any')) return undefined
  const match = matchKeys[0] as PolicyMatch
  const rawConditions = raw.when[match]
  if (!Array.isArray(rawConditions)) return undefined
  const conditions: VisualCondition[] = []
  for (const item of rawConditions) {
    const condition = representCondition(item)
    if (!condition) return undefined
    conditions.push(condition)
  }
  if (
    !Array.isArray(raw.actions) ||
    raw.actions.length === 0 ||
    raw.actions.length > policyLimits.maxActionsPerRule
  ) {
    return undefined
  }
  const actions: VisualAction[] = []
  for (const item of raw.actions) {
    const action = representAction(item)
    if (!action) return undefined
    actions.push(action)
  }
  return { id: raw.id, name: raw.name, domain: raw.domain, enabled: raw.enabled, match, conditions, actions }
}

function representDocument(root: Record<string, unknown>): VisualDocument | undefined {
  const keys = Object.keys(root)
  if (keys.some((key) => !['schema_version', 'group_policy', 'rules'].includes(key))) {
    return undefined
  }
  if (root.group_policy !== undefined && root.group_policy !== 'inherit' && root.group_policy !== 'override') {
    return undefined
  }
  if (!Array.isArray(root.rules)) return undefined
  const rules: VisualRule[] = []
  for (const item of root.rules) {
    const rule = representRule(item)
    if (!rule) return undefined
    rules.push(rule)
  }
  return { groupPolicy: root.group_policy === 'override' ? 'override' : 'inherit', rules }
}

// 解析顶层正文。空正文与 fatal 语法/结构问题不返回文档；嵌套或未知结构返回只读正文。
export function readVisualDocument(text: string): VisualDocumentResult {
  const issues: PolicyIssue[] = []
  if (text.trim() === '') {
    issues.push({ code: 'empty', path: '', fatal: true })
    return { issues }
  }
  if (utf8ByteLength(text) > policyLimits.maxConfigBytes) {
    issues.push({ code: 'configBytes', path: '', fatal: true })
    return { issues }
  }
  let root: unknown
  try {
    root = JSON.parse(text)
  } catch (error) {
    issues.push({
      code: 'syntax',
      path: '',
      fatal: true,
      detail: error instanceof Error ? error.message : undefined,
    })
    return { issues }
  }
  if (!isPlainObject(root)) {
    issues.push({ code: 'rootType', path: '', fatal: true })
    return { issues }
  }
  if (root.schema_version !== 1) {
    issues.push({ code: 'schemaVersion', path: 'schema_version', fatal: true })
    return { issues }
  }
  if (!Array.isArray(root.rules)) {
    issues.push({ code: 'rulesType', path: 'rules', fatal: true })
    return { issues }
  }
  if (root.rules.length > policyLimits.maxRulesPerConfig) {
    issues.push({ code: 'ruleCount', path: 'rules', fatal: true })
    return { issues }
  }
  const document = representDocument(root)
  if (!document) {
    issues.push({ code: 'advanced', path: '', fatal: false })
    return { advancedText: text, issues }
  }
  return { document, issues }
}

export function hasFatalIssue(issues: PolicyIssue[]): boolean {
  return issues.some((issue) => issue.fatal)
}

// ---- 原生 JSON 辅助：JSON 模式与 group_policy 切换复用。 ----

export function parseConfigObject(text: string): Record<string, unknown> | undefined {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  try {
    const value: unknown = JSON.parse(trimmed)
    return isPlainObject(value) ? value : undefined
  } catch {
    return undefined
  }
}

export function formatConfigText(text: string): string | undefined {
  const value = parseConfigObject(text)
  return value ? JSON.stringify(value, null, 2) : undefined
}

// 顶层 group_policy（credential policy 专用）：默认 inherit。
export function groupPolicyMode(root: Record<string, unknown> | undefined): GroupPolicyMode {
  return root?.group_policy === 'override' ? 'override' : 'inherit'
}

export function setGroupPolicyMode(
  root: Record<string, unknown>,
  mode: GroupPolicyMode,
): Record<string, unknown> {
  return { ...root, group_policy: mode }
}

// ---- 序列化：可视化文档 -> 原生 JSON 对象。 ----

function conditionToJson(condition: VisualCondition): Record<string, unknown> {
  if (condition.kind === 'expression') return { expression: condition.expression }
  if (condition.kind === 'time_window') {
    return {
      predicate: 'time_window',
      weekdays: [...condition.weekdays],
      ranges: condition.ranges.map(([start, end]) => [start, end]),
    }
  }
  if (condition.fact === quotaFactKey && condition.windowSeconds !== undefined) {
    return {
      fact: condition.fact,
      select: { scope: quotaScopeAccount, window_seconds: condition.windowSeconds },
      reduce: quotaReduceMin,
      op: condition.op,
      value: condition.value,
    }
  }
  return { fact: condition.fact, op: condition.op, value: condition.value }
}

function actionToJson(action: VisualAction): Record<string, unknown> {
  if (action.type === 'exclude_models') return { type: action.type, models: [...action.models] }
  if (action.type === 'multiply_price') return { type: action.type, factor: action.factor }
  return { type: action.type }
}

export function documentToJson(document: VisualDocument): Record<string, unknown> {
  const root: Record<string, unknown> = { schema_version: 1 }
  if (document.groupPolicy === 'override') root.group_policy = 'override'
  root.rules = document.rules.map((rule) => ({
    id: rule.id,
    name: rule.name,
    domain: rule.domain,
    enabled: rule.enabled,
    when: { [rule.match]: rule.conditions.map(conditionToJson) },
    actions: rule.actions.map(actionToJson),
  }))
  return root
}

export function serializeDocument(document: VisualDocument): string {
  return JSON.stringify(documentToJson(document), null, 2)
}

// ---- 新建与编辑操作：全部返回新对象，避免就地修改共享状态。 ----

export function newRuleId(existing: Iterable<string>): string {
  const taken = new Set(existing)
  for (let index = 1; ; index++) {
    const candidate = `rule-${index}`
    if (!taken.has(candidate)) return candidate
  }
}

export function newExpressionCondition(expression = ''): VisualExpressionCondition {
  return { kind: 'expression', expression }
}

export function newQuotaFactCondition(windowSeconds: number): VisualParamCondition {
  return {
    kind: 'param',
    fact: quotaFactKey,
    op: 'lt',
    value: 0.1,
    windowSeconds,
  }
}

export function newFactCondition(fact: string, windowSeconds?: number): VisualParamCondition {
  const definition = factDefinition(fact)
  // 额度参数没有通用默认窗口，必须由调用方给出实际 catalog 窗口，否则回落到时间条件。
  if (!definition || (definition.quota && windowSeconds === undefined)) {
    return { kind: 'param', fact: 'request.model', op: 'eq', value: '' }
  }
  if (definition.quota) return newQuotaFactCondition(windowSeconds as number)
  return { kind: 'param', fact, op: definition.operators[0]!, value: '' }
}

export function newTimeWindowCondition(): VisualTimeWindowCondition {
  return { kind: 'time_window', weekdays: [1], ranges: [['09:00', '18:00']] }
}

export function newCondition(kind: 'param' | 'time_window' | 'expression'): VisualCondition {
  if (kind === 'time_window') return newTimeWindowCondition()
  if (kind === 'expression') return newExpressionCondition()
  return newFactCondition('request.model')
}

export function newRule(id: string, domain: PolicyDomain): VisualRule {
  return {
    id,
    // 名称默认取合法的规则 ID，保证新建规则整体可编译；用户可再修改。
    name: id,
    domain,
    enabled: false,
    match: 'all',
    conditions: [],
    actions:
      domain === 'pricing'
        ? [{ type: 'multiply_price', factor: '1' }]
        : [{ type: 'exclude_candidate' }],
  }
}

export function duplicateRule(rule: VisualRule, existing: Iterable<string>): VisualRule {
  return { ...rule, id: newRuleId(existing), enabled: false }
}

export function emptyDocument(): VisualDocument {
  return { groupPolicy: 'inherit', rules: [] }
}

export function moveRule(rules: VisualRule[], from: number, to: number): VisualRule[] {
  if (from < 0 || from >= rules.length || to < 0 || to >= rules.length) return rules
  const next = [...rules]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved!)
  return next
}
