import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'

export interface RedactionRule {
  pattern: string
  replacement: string
}
export interface RedactionIssue {
  index: number
  error?: string
  warning?: string
}
export const redactionPresets = [
  {
    key: 'email',
    pattern: String.raw`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`,
    replacement: '[EMAIL]',
  },
  { key: 'phone', pattern: String.raw`\b1[3-9][0-9]{9}\b`, replacement: '[PHONE]' },
  {
    key: 'apiKey',
    pattern: String.raw`\b(?:sk-(?:proj-|ant-)?|gh[pousr]_)[A-Za-z0-9_-]{16,}\b`,
    replacement: '[API_KEY]',
  },
  {
    key: 'privateKey',
    pattern: String.raw`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`,
    replacement: '[PRIVATE_KEY]',
  },
] as const

export function readRedactionRules(value: unknown): RedactionRule[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value.map((row: unknown) => {
    if (
      !row ||
      typeof row !== 'object' ||
      !('pattern' in row) ||
      !('replacement' in row) ||
      typeof row.pattern !== 'string' ||
      typeof row.replacement !== 'string'
    )
      throw new InvalidResponseError()
    return { pattern: row.pattern, replacement: row.replacement }
  })
}
export async function validateRedaction(
  client: ApiClient,
  rules: RedactionRule[],
  signal: AbortSignal,
): Promise<RedactionIssue[]> {
  const value: unknown = await client.request('/api/settings/request-redaction/validate', {
    method: 'POST',
    json: { rules },
    signal,
  })
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value.map((row: unknown) => {
    if (
      !row ||
      typeof row !== 'object' ||
      !('index' in row) ||
      typeof row.index !== 'number' ||
      !Number.isSafeInteger(row.index)
    )
      throw new InvalidResponseError()
    const issue: RedactionIssue = { index: row.index }
    if ('error' in row) {
      if (typeof row.error !== 'string') throw new InvalidResponseError()
      issue.error = row.error
    }
    if ('warning' in row) {
      if (typeof row.warning !== 'string') throw new InvalidResponseError()
      issue.warning = row.warning
    }
    return issue
  })
}
