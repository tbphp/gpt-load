import type { ParameterJSONValue } from '@/api/control/types'

/**
 * 参数覆盖的路径写法：设置与删除共用一套 `a/b` 分层文本，段内的 `~` 与 `/` 按
 * JSON Pointer 规则转义成 `~0` 与 `~1`。后端 remove 存的是带前导斜杠的 Pointer，
 * set 存的是嵌套对象；界面统一省略前导斜杠，两者只差这一个字符。
 *
 * 用 `/` 而不是 `.` 分层，是因为转义规则让它无歧义：`{"a.b":1}` 展平成 `a.b`、
 * `{"a":{"b":1}}` 成 `a/b`、`{"a/b":1}` 成 `a~1b`，三者互不相撞。
 */

/** 快捷行的一行：路径 + 该路径上的 JSON 值。 */
export interface ParameterPathEntry {
  path: string
  value: ParameterJSONValue
}

class ParameterPathError extends Error {}

// 转义顺序不可交换：先 ~ 再 /，否则 ~1 里的 ~ 会被二次转义。
function escapeSegment(segment: string): string {
  return segment.replaceAll('~', '~0').replaceAll('/', '~1')
}

// 解码顺序同样固定：先 ~1 再 ~0，与 JSON Pointer 规范一致。
function unescapeSegment(raw: string): string | undefined {
  if (/~(?![01])/u.test(raw)) return undefined
  return raw.replaceAll('~1', '/').replaceAll('~0', '~')
}

function isPlainObject(value: ParameterJSONValue): value is Record<string, ParameterJSONValue> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function createParameterObject(): Record<string, ParameterJSONValue> {
  return Object.create(null) as Record<string, ParameterJSONValue>
}

// 展平只递归非空对象：空对象没有叶子，必须作为值整体保留，否则整条会消失。
function isNestedObject(value: ParameterJSONValue): value is Record<string, ParameterJSONValue> {
  return isPlainObject(value) && Object.keys(value).length > 0
}

function encodeParameterPath(segments: readonly string[]): string {
  return segments.map(escapeSegment).join('/')
}

/** 解析分层路径；remove 不支持数组下标与 `-`，set 将它们视为普通对象键。 */
export function decodeParameterPath(
  path: string,
  operation: 'set' | 'remove',
): string[] | undefined {
  if (!path) return undefined
  const segments: string[] = []
  for (const raw of path.split('/')) {
    const decoded = unescapeSegment(raw)
    if (
      decoded === undefined ||
      decoded === '' ||
      (operation === 'remove' && (decoded === '-' || /^\d+$/u.test(decoded)))
    )
      return undefined
    segments.push(decoded)
  }
  return segments
}

/**
 * 两条路径是否相交：完全相同，或一方是另一方的祖先。
 * 段内的 `/` 已转义为 `~1`，所以 `/` 只出现在段边界，前缀比较是安全的。
 */
export function parameterPathsCross(left: string, right: string): boolean {
  return left === right || left.startsWith(`${right}/`) || right.startsWith(`${left}/`)
}

export function flattenParameterSet(set: Record<string, ParameterJSONValue>): ParameterPathEntry[] {
  const entries: ParameterPathEntry[] = []
  const walk = (node: Record<string, ParameterJSONValue>, prefix: readonly string[]): void => {
    for (const [key, value] of Object.entries(node)) {
      const segments = [...prefix, key]
      if (isNestedObject(value)) walk(value, segments)
      else entries.push({ path: encodeParameterPath(segments), value })
    }
  }
  walk(set, [])
  return entries
}

/**
 * 还原展平结果。祖先与后代同时出现属于矛盾配置，调用方应先用
 * parameterPathsCross 拦下来；这里遇到就直接抛错，不做静默取舍。
 */
export function expandParameterSet(
  entries: readonly ParameterPathEntry[],
): Record<string, ParameterJSONValue> {
  const result = createParameterObject()
  for (const { path, value } of entries) {
    const segments = decodeParameterPath(path, 'set')
    const leaf = segments?.at(-1)
    if (!segments || leaf === undefined) throw new ParameterPathError(path)
    let node = result
    for (const segment of segments.slice(0, -1)) {
      const existing: ParameterJSONValue | undefined = node[segment]
      if (existing === undefined) node[segment] = createParameterObject()
      else if (!isPlainObject(existing)) throw new ParameterPathError(path)
      node = node[segment] as Record<string, ParameterJSONValue>
    }
    node[leaf] = value
  }
  return result
}

/** remove 在后端存 JSON Pointer；界面省略前导斜杠。 */
export function toParameterPointer(path: string): string {
  return `/${path}`
}

export function fromParameterPointer(pointer: string): string {
  return pointer.startsWith('/') ? pointer.slice(1) : pointer
}
