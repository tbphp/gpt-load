export type HeaderRuleAction = 'set' | 'remove'

export interface HeaderRuleInput {
  rowKey: number
  action: HeaderRuleAction
  name: string
  value: string
}

export interface HeaderRuleValidationError {
  code:
    | 'required'
    | 'invalid_name'
    | 'duplicate_name'
    | 'forbidden_set_name'
    | 'credential_template_required'
    | 'invalid_value'
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

export function validateHeaderRuleRows(
  rows: readonly HeaderRuleInput[],
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
    if (row.action === 'remove') continue

    const normalizedName = asciiLower(row.name)
    if (isForbiddenSetName(normalizedName)) {
      errors.push({ code: 'forbidden_set_name', rowKey: row.rowKey })
      continue
    }
    if (!isHTTPHeaderValue(row.value)) {
      errors.push({ code: 'invalid_value', rowKey: row.rowKey })
      continue
    }
    if (credentialNames.has(normalizedName) && !row.value.includes('${API_KEY}')) {
      errors.push({ code: 'credential_template_required', rowKey: row.rowKey })
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

function isForbiddenSetName(normalizedName: string): boolean {
  return normalizedName.startsWith('proxy-') || forbiddenSetNames.has(normalizedName)
}

function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}
