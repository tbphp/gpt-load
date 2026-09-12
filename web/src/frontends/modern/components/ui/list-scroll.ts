// 仅在当前前端会话内记住列表位置，不持久化查询内容。
const positions = new Map<string, number>()
export function readListScroll(key?: string): number | undefined {
  return key ? positions.get(key) : undefined
}
export function saveListScroll(key: string | undefined, value: number): void {
  if (!key) return
  positions.delete(key)
  positions.set(key, value)
  if (positions.size > 50) positions.delete(positions.keys().next().value!)
}
