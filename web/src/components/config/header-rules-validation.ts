export type HeaderRuleAction = 'set' | 'remove'
export type HeaderRuleValidationPolicy = 'request' | 'response'

export interface HeaderRuleInput {
  rowKey: number
  action: HeaderRuleAction
  name: string
  value: string
}

export interface HeaderRuleValidationError {
  code: 'required' | 'invalid_name' | 'duplicate_name' | 'forbidden_set_name' | 'invalid_value'
  rowKey: number
}

const credentialNames = new Set([
  'authorization',
  'proxy-authorization',
  'api-key',
  'x-api-key',
  'x-goog-api-key',
])
const forbiddenSetNames = new Set([
  'connection',
  'proxy-connection',
  'keep-alive',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
  'cookie',
  'cookie2',
])

const forbiddenResponseNames = new Set([
  ...forbiddenSetNames,
  'content-encoding',
  'content-length',
  'content-range',
  'content-type',
  'date',
  'server',
  'set-cookie',
  'set-cookie2',
  'vary',
  'access-control-allow-origin',
  'access-control-allow-methods',
  'access-control-allow-headers',
  'access-control-allow-credentials',
  'access-control-expose-headers',
  'access-control-max-age',
])

export function validateHeaderRuleRows(
  rows: readonly HeaderRuleInput[],
  policy: HeaderRuleValidationPolicy = 'request',
): HeaderRuleValidationError[] {
  const errors: HeaderRuleValidationError[] = []
  const duplicateRows = duplicateHeaderRuleRows(rows)

  for (const row of rows) {
    if (!row.name) {
      errors.push({ code: 'required', rowKey: row.rowKey })
      continue
    }
    if (!isHTTPHeaderName(row.name)) {
      errors.push({ code: 'invalid_name', rowKey: row.rowKey })
      continue
    }
    if (duplicateRows.has(row.rowKey)) {
      errors.push({ code: 'duplicate_name', rowKey: row.rowKey })
      continue
    }
    const normalizedName = asciiLower(row.name)
    if (credentialNames.has(normalizedName) || isForbiddenName(normalizedName, policy)) {
      errors.push({ code: 'forbidden_set_name', rowKey: row.rowKey })
      continue
    }
    if (row.action === 'remove') continue
    if (!isHTTPHeaderValue(row.value)) {
      errors.push({ code: 'invalid_value', rowKey: row.rowKey })
      continue
    }
  }

  return errors
}

function duplicateHeaderRuleRows(rows: readonly HeaderRuleInput[]): Set<number> {
  const rowsByName = new Map<string, number[]>()
  for (const row of rows) {
    if (!isHTTPHeaderName(row.name)) continue
    const normalizedName = asciiLower(row.name)
    const matchingRows = rowsByName.get(normalizedName) ?? []
    matchingRows.push(row.rowKey)
    rowsByName.set(normalizedName, matchingRows)
  }

  return new Set([...rowsByName.values()].filter((matchingRows) => matchingRows.length > 1).flat())
}

function isHTTPHeaderName(value: string): boolean {
  if (!value) return false
  for (let index = 0; index < value.length; index += 1) {
    if (!isHTTPTokenCode(value.charCodeAt(index))) return false
  }
  return true
}

function isHTTPTokenCode(value: number): boolean {
  return (
    (value >= 0x30 && value <= 0x39) ||
    (value >= 0x41 && value <= 0x5a) ||
    (value >= 0x61 && value <= 0x7a) ||
    "!#$%&'*+-.^_`|~".includes(String.fromCharCode(value))
  )
}

function isHTTPHeaderValue(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if ((code < 0x20 && code !== 0x09) || code === 0x7f) return false
  }
  return true
}

function isForbiddenName(normalizedName: string, policy: HeaderRuleValidationPolicy): boolean {
  if (normalizedName.startsWith('proxy-')) return true
  if (policy === 'request') return forbiddenSetNames.has(normalizedName)
  return normalizedName.startsWith('x-gptload-') || forbiddenResponseNames.has(normalizedName)
}

function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}
