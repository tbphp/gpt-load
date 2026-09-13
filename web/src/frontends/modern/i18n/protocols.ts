// 展示统一使用协议枚举的小写名称，接口值始终保留原始枚举。
export const protocolMessages = {
  'openai-completions': 'openai-completions',
  'openai-responses': 'openai-responses',
  'openai-images': 'openai-images',
  'openai-embeddings': 'openai-embeddings',
  rerank: 'rerank',
  anthropic: 'anthropic',
  gemini: 'gemini',
} as const

export function protocolLabel(
  value: string | null | undefined,
  translate: (key: string) => string,
): string {
  if (!value) return '—'
  return Object.hasOwn(protocolMessages, value)
    ? translate('protocols.' + value)
    : value.toLowerCase()
}
