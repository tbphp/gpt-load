// 展示统一使用协议枚举的小写名称，接口值始终保留原始枚举。
export const protocolOrder = [
  'openai-responses',
  'anthropic',
  'gemini',
  'openai-completions',
  'openai-images',
  'openai-embeddings',
  'rerank',
] as const
export const protocolMessages = Object.fromEntries(
  protocolOrder.map((protocol) => [protocol, protocol]),
) as Record<(typeof protocolOrder)[number], string>
const protocolRanks = new Map<string, number>(
  protocolOrder.map((protocol, index) => [protocol, index]),
)

export function sortProtocols<T extends string>(values: readonly T[]): T[] {
  return [...new Set(values)].sort(
    (left, right) =>
      (protocolRanks.get(left) ?? protocolOrder.length) -
      (protocolRanks.get(right) ?? protocolOrder.length),
  )
}

export function protocolLabel(
  value: string | null | undefined,
  translate: (key: string) => string,
): string {
  if (!value) return '—'
  return Object.hasOwn(protocolMessages, value)
    ? translate('protocols.' + value)
    : value.toLowerCase()
}
