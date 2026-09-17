import type { ParameterJSONValue } from '@/api/control/types'
import { assertJSONNumbersRoundTrip } from '@/lib/json-number'

/**
 * 参数值的输入表示：类型是显式的一列，值不需要用户加引号。
 * 类型默认跟随输入推断（`0.7` → 数字、`enabled` → 文本、`true` → 布尔），
 * 用户改过之后就以他选的为准 —— 想把 `7` 当文本存，改类型而不是打引号。
 *
 * 引号只剩一个用途：保住往返。文本 `"7"` 若裸着显示成 7，读出再存回就变成数字，
 * 所以这类值显示时补引号，由 formatValueText 与 textValue 成对处理，用户不必知道。
 */

export type ParameterValueKind = 'text' | 'number' | 'boolean' | 'null' | 'json'

export const parameterValueKinds: readonly ParameterValueKind[] = [
  'text',
  'number',
  'boolean',
  'null',
  'json',
]

export class ParameterValueError extends Error {}

/** JSON 数字字面量。刻意严格：`007` 与 `.5` 不算数字，推断成文本后由类型列暴露出来。 */
const jsonNumberPattern = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/u

function isJSONNumberLiteral(text: string): boolean {
  return jsonNumberPattern.test(text)
}

/** 参数对象的字段名不能为空；快捷路径无法把空字段与“尚未输入”区分开。 */
export function hasEmptyParameterKey(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(hasEmptyParameterKey)
  if (value === null || typeof value !== 'object') return false
  return Object.entries(value).some(([key, nested]) => key === '' || hasEmptyParameterKey(nested))
}

/** 裸文本会被重新推断成别的类型时，才需要补引号。 */
function needsQuoting(value: string): boolean {
  return (
    value === '' ||
    value.trim() !== value ||
    /[\u0000-\u001f]/u.test(value) ||
    value === 'true' ||
    value === 'false' ||
    value === 'null' ||
    isJSONNumberLiteral(value) ||
    value.startsWith('{') ||
    value.startsWith('[') ||
    value.startsWith('"')
  )
}

export function formatValueText(value: ParameterJSONValue): string {
  if (typeof value === 'string') return needsQuoting(value) ? JSON.stringify(value) : value
  return JSON.stringify(value) ?? 'null'
}

/** 从已有 JSON 值反推类型，用于载入时填充类型列。 */
export function valueKind(value: ParameterJSONValue): ParameterValueKind {
  if (value === null) return 'null'
  if (typeof value === 'object') return 'json'
  switch (typeof value) {
    case 'number':
      return 'number'
    case 'boolean':
      return 'boolean'
    default:
      return 'text'
  }
}

/** 从输入文本推断类型；用户没有手动指定时跟随它。 */
export function inferValueKind(text: string): ParameterValueKind {
  const trimmed = text.trim()
  if (!trimmed) return 'text'
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return 'json'
  if (trimmed.startsWith('"')) return 'text'
  if (isJSONNumberLiteral(trimmed)) return 'number'
  if (trimmed === 'true' || trimmed === 'false') return 'boolean'
  if (trimmed === 'null') return 'null'
  return 'text'
}

// 带引号的输入还原成文本本身，引号不完整就按字面处理。
function textValue(text: string): string {
  const trimmed = text.trim()
  if (trimmed.startsWith('"')) {
    try {
      const parsed: unknown = JSON.parse(trimmed)
      if (typeof parsed === 'string') return parsed
    } catch {
      // 落到字面文本
    }
  }
  return text
}

/** 按类型列指定的类型解释输入；类型对不上就是错误，不做静默兜底。 */
export function valueFromText(text: string, kind: ParameterValueKind): ParameterJSONValue {
  if (kind === 'null') return null
  const trimmed = text.trim()
  if (kind === 'text') return textValue(text)
  if (!trimmed) throw new ParameterValueError('empty')

  if (kind === 'number') {
    if (!isJSONNumberLiteral(trimmed)) throw new ParameterValueError('number')
    assertJSONNumbersRoundTrip(trimmed)
    return JSON.parse(trimmed) as number
  }
  if (kind === 'boolean') {
    if (trimmed === 'true') return true
    if (trimmed === 'false') return false
    throw new ParameterValueError('boolean')
  }

  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) {
    throw new ParameterValueError('json')
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    throw new ParameterValueError('json')
  }
  if (hasEmptyParameterKey(parsed)) throw new ParameterValueError('empty-key')
  assertJSONNumbersRoundTrip(trimmed)
  return parsed as ParameterJSONValue
}

export function tryValueFromText(
  text: string,
  kind: ParameterValueKind,
): ParameterJSONValue | undefined {
  try {
    return valueFromText(text, kind)
  } catch {
    return undefined
  }
}
