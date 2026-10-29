import presets from './request-redaction-presets.json'

export interface RedactionRule {
  pattern: string
  replacement: string
  mode?: 'replace' | 'encrypt'
}

export const redactionPresets = presets satisfies (RedactionRule & { key: string })[]

export type RedactionQuickKind = 'keyword' | 'prefix'

export function buildRedactionRule(kind: RedactionQuickKind, value: string): RedactionRule | null {
  if (!value.trim()) return null
  // 字面转义兼容 RE2，中文关键词不附加 ASCII 单词边界。
  const literal = value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = literal + (kind === 'prefix' ? '[A-Za-z0-9_-]{8,}' : '')
  if (new TextEncoder().encode(pattern).length > 4096) return null
  return { pattern, replacement: '[REDACTED]', mode: 'encrypt' }
}
