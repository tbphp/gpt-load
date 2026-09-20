import type { LogDetail, LogReceipt } from '@modern/api/logs'

const saltStorageKey = 'gpt-load.modern.log-redaction-salt.v1'
let memorySalt = ''
const sessionReferences = new Map<string, string>()

function bytesToHex(value: Uint8Array): string {
  return Array.from(value, (item) => item.toString(16).padStart(2, '0')).join('')
}

function redactionSalt(): string {
  if (memorySalt) return memorySalt
  try {
    const stored = globalThis.localStorage?.getItem(saltStorageKey)
    if (stored && /^[0-9a-f]{32}$/.test(stored)) return (memorySalt = stored)
  } catch {
    // 隐私模式或受限存储下，仍在当前页面会话内保持匿名标识稳定。
  }
  const bytes = new Uint8Array(16)
  globalThis.crypto.getRandomValues(bytes)
  memorySalt = bytesToHex(bytes)
  try {
    globalThis.localStorage?.setItem(saltStorageKey, memorySalt)
  } catch {
    // 存储失败不会影响本次脱敏复制。
  }
  return memorySalt
}

async function anonymousReference(kind: string, value: string | number): Promise<string> {
  // 普通 HTTP 没有 SubtleCrypto；使用会话内随机映射，不能退化为可逆或弱散列。
  if (!globalThis.crypto.subtle) {
    const key = `${kind}\0${String(value)}`
    let reference = sessionReferences.get(key)
    if (!reference) {
      reference = `${kind}_${bytesToHex(globalThis.crypto.getRandomValues(new Uint8Array(12)))}`
      sessionReferences.set(key, reference)
    }
    return reference
  }
  const source = new TextEncoder().encode(`${redactionSalt()}\0${kind}\0${String(value)}`)
  const digest = new Uint8Array(await globalThis.crypto.subtle.digest('SHA-256', source))
  return `${kind}_${bytesToHex(digest).slice(0, 12)}`
}

async function replaceAsync(
  value: string,
  pattern: RegExp,
  replacement: (match: RegExpMatchArray) => Promise<string>,
): Promise<string> {
  let result = ''
  let offset = 0
  for (const match of value.matchAll(pattern)) {
    const index = match.index ?? 0
    result += value.slice(offset, index) + (await replacement(match))
    offset = index + match[0].length
  }
  return result + value.slice(offset)
}

async function redactDiagnosticText(value: string): Promise<string> {
  let result = value
  result = await replaceAsync(
    result,
    /(["']?(?:authorization|proxy-authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|password|client[_-]?secret|secret)["']?\s*[:=]\s*["']?)(?:(?:Bearer|Basic)\s+)?([^"'\s,;}]+)/giu,
    async (match) => `${match[1]}${await anonymousReference('secret', match[2])}`,
  )
  result = await replaceAsync(
    result,
    /\b(Bearer|Basic)\s+([A-Za-z0-9._~+/=-]+)/giu,
    async (match) => {
      return `${match[1]} ${await anonymousReference('secret', match[2])}`
    },
  )
  // URL 先整体处理，避免其中的邮箱或 IP 替换后打断 URL，遗漏路径与查询参数。
  result = await replaceAsync(result, /https?:\/\/[^\s"'<>]+/giu, async (match) => {
    return `<${await anonymousReference('url', match[0])}>`
  })
  result = await replaceAsync(
    result,
    /(?<![\w:])(?:[a-f0-9]{0,4}:){2,}[a-f0-9:.]*(?![\w:])/giu,
    async (match) => {
      try {
        const host = new URL(`http://[${match[0]}]`).hostname
        return `<${await anonymousReference('ip', host)}>`
      } catch {
        return match[0]
      }
    },
  )
  result = await replaceAsync(
    result,
    /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/giu,
    async (match) => `<${await anonymousReference('email', match[0].toLowerCase())}>`,
  )
  result = await replaceAsync(
    result,
    /\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b/gu,
    async (match) => `<${await anonymousReference('ip', match[0])}>`,
  )
  result = await replaceAsync(
    result,
    /\b(?:sk|xai|key)-[A-Za-z0-9._-]{8,}\b|\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b|\b[0-9a-f]{32,}\b/giu,
    async (match) => `<${await anonymousReference('secret', match[0])}>`,
  )
  return result
}

async function identityReference(
  kind: string,
  id: number | null | undefined,
  name?: string | null,
): Promise<string | null> {
  const source = id ?? name?.trim()
  return source === undefined || source === null || source === ''
    ? null
    : anonymousReference(kind, source)
}

async function redactReceipt(receipt: LogReceipt | null): Promise<LogReceipt | null> {
  if (!receipt) return null
  return {
    ...receipt,
    rule: {
      ...receipt.rule,
      scope_key: receipt.rule.scope_key
        ? await anonymousReference('scope', receipt.rule.scope_key)
        : null,
    },
  }
}

export async function createRedactedLogExport(log: LogDetail): Promise<string> {
  const accessKeyReference = await identityReference(
    'access_key',
    log.access_key.id,
    log.access_key.name,
  )
  const groupReference = await identityReference('group', log.group_id)
  const credentialReference = await identityReference(
    'credential',
    log.credential_id,
    log.credential_name,
  )
  const decision = log.auto_decision
    ? {
        ...log.auto_decision,
        receipt: await redactReceipt(log.auto_decision.receipt),
        group_name: (await identityReference('group', null, log.auto_decision.group_name)) ?? '',
        credential_name:
          (await identityReference('credential', null, log.auto_decision.credential_name)) ?? '',
      }
    : null
  const attempts = await Promise.all(
    log.attempts.map(async (attempt) => {
      const attemptGroupReference = await identityReference(
        'group',
        attempt.group_id,
        attempt.group_name,
      )
      const attemptCredentialReference = await identityReference(
        'credential',
        attempt.credential_id,
        attempt.credential_name,
      )
      return {
        ...attempt,
        group_id: attemptGroupReference,
        group_name: attemptGroupReference,
        credential_id: attemptCredentialReference,
        credential_name: attemptCredentialReference,
        upstream_request_id: attempt.upstream_request_id
          ? await anonymousReference('upstream_request', attempt.upstream_request_id)
          : null,
        rule_id: attempt.rule_id ? await anonymousReference('rule', attempt.rule_id) : null,
        error_summary: await redactDiagnosticText(attempt.error_summary),
        pricing_receipt: await redactReceipt(attempt.pricing_receipt),
      }
    }),
  )
  return JSON.stringify(
    {
      format: 'gpt-load-redacted-request-log',
      version: 1,
      redaction: {
        applied: true,
        stable_identifiers: globalThis.crypto.subtle
          ? 'Scoped to this browser profile; page session only if storage is unavailable'
          : 'Scoped to this page session (HTTP without SubtleCrypto)',
        redacted_fields: [
          'access key',
          'group',
          'credential',
          'upstream request',
          'routing rule and scope',
          'known secrets and network identifiers in diagnostic text',
        ],
      },
      log: {
        ...log,
        access_key: {
          reference: accessKeyReference,
          deleted: log.access_key.deleted,
        },
        group_id: groupReference,
        credential_id: credentialReference,
        credential_name: credentialReference,
        auto_decision: decision,
        error_summary: await redactDiagnosticText(log.error_summary),
        attempts,
      },
    },
    null,
    2,
  )
}
