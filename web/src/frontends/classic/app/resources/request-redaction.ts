import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'

import type { RedactionRule } from '@shared/request-redaction'
export type { RedactionRule } from '@shared/request-redaction'
export interface RedactionIssue {
  index: number
  error?: string
  warning?: string
}
export { redactionPresets } from '@shared/request-redaction'

export function readRedactionRules(value: unknown): RedactionRule[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value.map((row: unknown) => {
    if (!row || typeof row !== 'object' || !('pattern' in row) || typeof row.pattern !== 'string')
      throw new InvalidResponseError()
    const mode = 'mode' in row ? row.mode : undefined
    const replacement = 'replacement' in row ? row.replacement : undefined
    if (mode !== undefined && mode !== 'replace' && mode !== 'encrypt')
      throw new InvalidResponseError()
    if (replacement !== undefined && typeof replacement !== 'string')
      throw new InvalidResponseError()
    if (mode !== 'encrypt' && typeof replacement !== 'string') throw new InvalidResponseError()
    return {
      pattern: row.pattern,
      replacement: typeof replacement === 'string' ? replacement : '',
      ...(mode === 'replace' || mode === 'encrypt' ? { mode } : {}),
    }
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
