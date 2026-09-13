import type { ParameterRule } from '@modern/api/group-detail'
import { isModelPattern } from '@modern/components/ui/model-match'

export const parameterValueTypes = ['text', 'number', 'boolean', 'null', 'json'] as const
export type ParameterValueType = (typeof parameterValueTypes)[number]
export interface ParameterActionDraft {
  key: number
  operation: 'set' | 'remove'
  path: string
  type: ParameterValueType
  typePinned: boolean
  text: string
}
export interface ParameterRuleDraft {
  key: number
  open: boolean
  protocol: string
  model: string
  json: boolean
  setText: string
  actions: ParameterActionDraft[]
}
export interface ParameterRuleResult {
  value?: ParameterRule
  modelError?: string
  setError?: string
  setValue?: Record<string, unknown>
  canSwitch: boolean
  canFormat: boolean
  actionRequired: boolean
  fields: Map<number, { path?: string; value?: string }>
  crossing: boolean
}

const numberPattern = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/u
const protectedFields = new Set(['model', 'stream', 'store'])
class ParameterInputError extends Error {}

export function inferParameterType(source: string): ParameterValueType {
  const text = source.trim()
  if (text.startsWith('{') || text.startsWith('[')) return 'json'
  if (numberPattern.test(text)) return 'number'
  if (text === 'true' || text === 'false') return 'boolean'
  if (text === 'null') return 'null'
  return 'text'
}

function decimalKey(literal: string): string {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/u.exec(literal)
  if (!match) throw new ParameterInputError('unsafeNumber')
  const fraction = match[3] ?? ''
  const digits = `${match[2]}${fraction}`.replace(/^0+/u, '') || '0'
  if (digits === '0') return '0'
  const exponent = Number(match[4] ?? '0')
  if (!Number.isSafeInteger(exponent)) throw new ParameterInputError('unsafeNumber')
  const trailing = /0+$/u.exec(digits)?.[0].length ?? 0
  return `${match[1]}${trailing ? digits.slice(0, -trailing) : digits}e${exponent - fraction.length + trailing}`
}

// 与旧版相同：数字必须能经过浏览器 JSON 往返，不能静默舍入后保存。
function assertNumbers(source: string): void {
  let index = 0
  while (index < source.length) {
    if (source[index] === '"') {
      index++
      while (index < source.length) {
        if (source[index] === '\\') index += 2
        else if (source[index++] === '"') break
      }
      continue
    }
    if (!/[-\d]/u.test(source[index] ?? '')) {
      index++
      continue
    }
    let end = index + 1
    while (end < source.length && '0123456789eE+-.'.includes(source[end]!)) end++
    const literal = source.slice(index, end)
    const value = Number(literal)
    const encoded = JSON.stringify(value)
    if (
      !Number.isFinite(value) ||
      Object.is(value, -0) ||
      (Number.isInteger(value) && !Number.isSafeInteger(value)) ||
      typeof encoded !== 'string' ||
      decimalKey(literal) !== decimalKey(encoded)
    )
      throw new ParameterInputError('unsafeNumber')
    index = end
  }
}

function hasEmptyKey(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(hasEmptyKey)
  return (
    value !== null &&
    typeof value === 'object' &&
    Object.entries(value).some(([key, child]) => key === '' || hasEmptyKey(child))
  )
}

function objectValue(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function parseJSON(source: string, error: string): unknown {
  let value: unknown
  try {
    value = JSON.parse(source)
  } catch {
    throw new ParameterInputError(error)
  }
  assertNumbers(source)
  if (hasEmptyKey(value)) throw new ParameterInputError('emptyKey')
  return value
}

export function parameterValue(action: ParameterActionDraft): unknown {
  const text = action.text.trim()
  if (action.type === 'null') return null
  if (action.type === 'text') {
    if (text.startsWith('"')) {
      try {
        const value: unknown = JSON.parse(text)
        if (typeof value === 'string') return value
      } catch {
        /* 不完整引号按普通文本保留。 */
      }
    }
    return action.text
  }
  if (!text) throw new ParameterInputError('valueRequired')
  if (action.type === 'number') {
    if (!numberPattern.test(text)) throw new ParameterInputError('number')
    assertNumbers(text)
    return Number(text)
  }
  if (action.type === 'boolean') {
    if (text !== 'true' && text !== 'false') throw new ParameterInputError('boolean')
    return text === 'true'
  }
  const value = parseJSON(text, 'json')
  if (value === null || typeof value !== 'object') throw new ParameterInputError('json')
  return value
}

function formatParameterText(value: unknown): string {
  if (typeof value !== 'string') return JSON.stringify(value) ?? ''
  // 为容易被自动识别为其他类型的字符串保留引号，切换视图不改变值的类型。
  return !value ||
    value.trim() !== value ||
    /[\u0000-\u001f]/u.test(value) ||
    value.startsWith('"') ||
    inferParameterType(value) !== 'text'
    ? JSON.stringify(value)
    : value
}

function escapeSegment(value: string): string {
  return value.replaceAll('~', '~0').replaceAll('/', '~1')
}
function decodePath(path: string, operation: 'set' | 'remove'): string[] | undefined {
  if (!path) return undefined
  const segments = path.split('/')
  if (segments.some((part) => /~(?![01])/u.test(part))) return undefined
  const decoded = segments.map((part) => part.replaceAll('~1', '/').replaceAll('~0', '~'))
  if (
    decoded.some(
      (part) => !part || (operation === 'remove' && (part === '-' || /^\d+$/u.test(part))),
    )
  )
    return undefined
  return decoded
}
function pathsCross(left: string, right: string): boolean {
  return left === right || left.startsWith(`${right}/`) || right.startsWith(`${left}/`)
}
export function blankParameter(action: ParameterActionDraft): boolean {
  return (
    !action.path.trim() &&
    (action.operation === 'remove' || action.type === 'null' || !action.text.trim())
  )
}

export function flattenParameterSet(
  value: Record<string, unknown>,
  prefix: string[] = [],
): { path: string; value: unknown }[] {
  return Object.entries(value).flatMap(([name, child]) => {
    const segments = [...prefix, name]
    if (objectValue(child) && Object.keys(child).length) return flattenParameterSet(child, segments)
    return [{ path: segments.map(escapeSegment).join('/'), value: child }]
  })
}

export function parameterSetDrafts(
  value: Record<string, unknown>,
  key: () => number,
): ParameterActionDraft[] {
  return flattenParameterSet(value).map(({ path, value: child }) => ({
    key: key(),
    operation: 'set',
    path,
    type:
      child === null
        ? 'null'
        : typeof child === 'object'
          ? 'json'
          : typeof child === 'number'
            ? 'number'
            : typeof child === 'boolean'
              ? 'boolean'
              : 'text',
    typePinned: false,
    text: formatParameterText(child),
  }))
}

export function parameterRuleDraft(value: ParameterRule, key: () => number): ParameterRuleDraft {
  return {
    key: key(),
    open: false,
    protocol: value.match.protocol ?? '',
    model: value.match.model ?? '',
    json: false,
    setText: '',
    actions: [
      ...parameterSetDrafts(value.set ?? {}, key),
      ...(value.remove ?? []).map((path): ParameterActionDraft => ({
        key: key(),
        operation: 'remove',
        path: path.startsWith('/') ? path.slice(1) : path,
        type: 'text',
        typePinned: false,
        text: '',
      })),
    ],
  }
}

export function inspectParameterRule(rule: ParameterRuleDraft): ParameterRuleResult {
  const filled = rule.actions.filter(
    (action) => (!rule.json || action.operation === 'remove') && !blankParameter(action),
  )
  const model = rule.model.trim()
  const result: ParameterRuleResult = {
    modelError: !isModelPattern(model) ? 'modelPattern' : undefined,
    actionRequired: false,
    fields: new Map(),
    canSwitch: false,
    canFormat: false,
    crossing: false,
  }
  const values = new Map<number, unknown>()
  for (const action of filled) {
    const errors: { path?: string; value?: string } = {}
    const path = decodePath(action.path, action.operation)
    if (!action.path.trim()) errors.path = 'pathRequired'
    else if (!path) errors.path = 'pathInvalid'
    else if (protectedFields.has(path[0]!.toLowerCase())) errors.path = 'protectedField'
    else if (
      filled.some(
        (other) =>
          other.key !== action.key &&
          other.operation === action.operation &&
          other.path === action.path,
      )
    )
      errors.path = 'duplicatePath'
    else if (
      action.operation === 'set' &&
      filled.some(
        (other) =>
          other.key !== action.key &&
          other.operation === 'set' &&
          pathsCross(action.path, other.path),
      )
    )
      errors.path = 'parentPath'
    if (action.operation === 'set') {
      try {
        values.set(action.key, parameterValue(action))
      } catch (cause) {
        errors.value = cause instanceof ParameterInputError ? cause.message : 'json'
      }
    }
    if (errors.path || errors.value) result.fields.set(action.key, errors)
  }

  if (rule.json) {
    try {
      const value = rule.setText.trim() ? parseJSON(rule.setText, 'invalidJSON') : {}
      if (!objectValue(value)) throw new ParameterInputError('setObject')
      result.setValue = value
      result.canFormat = Boolean(rule.setText.trim())
      if (Object.keys(value).some((name) => protectedFields.has(name.toLowerCase())))
        result.setError = 'protectedField'
    } catch (cause) {
      result.setError = cause instanceof ParameterInputError ? cause.message : 'invalidJSON'
    }
    result.canSwitch = !result.setError
  } else {
    const setActions = filled.filter((action) => action.operation === 'set')
    result.canSwitch = setActions.every((action) => !result.fields.has(action.key))
    if (result.canSwitch) {
      // 无原型对象让 __proto__ 等名称也能作为普通 JSON 键安全编辑。
      const set: Record<string, unknown> = Object.create(null)
      for (const action of setActions) {
        const segments = decodePath(action.path, 'set')!
        let target = set
        for (const segment of segments.slice(0, -1)) {
          if (!Object.hasOwn(target, segment)) target[segment] = Object.create(null)
          target = target[segment] as Record<string, unknown>
        }
        target[segments.at(-1)!] = values.get(action.key)
      }
      result.setValue = set
    }
  }

  const removeActions = filled.filter((action) => action.operation === 'remove')
  const setPaths = result.setValue
    ? flattenParameterSet(result.setValue).map((entry) => entry.path)
    : []
  result.actionRequired = !result.setError && !setPaths.length && !filled.length
  result.crossing = removeActions.some((action) =>
    setPaths.some((path) => pathsCross(action.path, path)),
  )
  if (result.modelError || result.actionRequired || result.setError || result.fields.size)
    return result
  result.value = {
    match: { ...(rule.protocol ? { protocol: rule.protocol } : {}), ...(model ? { model } : {}) },
    ...(result.setValue && Object.keys(result.setValue).length ? { set: result.setValue } : {}),
    ...(removeActions.length ? { remove: removeActions.map((action) => `/${action.path}`) } : {}),
  }
  return result
}
