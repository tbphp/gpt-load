import type { AccessKeyOptionDto, GroupSummary, KeyCounts } from '@/api/control/types'

const posixSingleQuoteEscape = `'"'"'`

export function quotePosixShellArgument(value: string): string {
  return `'${value.replaceAll("'", posixSingleQuoteEscape)}'`
}

export function buildChatCompletionsSnippet(origin: string, model: string): string {
  const payload = quotePosixShellArgument(JSON.stringify({ model }))
  return `curl "${origin}/v1/chat/completions" \\\n  -H "Authorization: Bearer $GPT_LOAD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d ${payload}`
}

export type NativeProtocol = 'openai' | 'anthropic' | 'gemini'

export interface ConnectionSnippetInput {
  origin: string
  protocol: NativeProtocol
  model: string
}

export interface ConnectionSnippet {
  path: string
  command: string
  language: 'bash'
}

function normalizedOrigin(origin: string): string {
  return origin.replace(/\/+$/, '')
}

export function buildConnectionSnippet(input: ConnectionSnippetInput): ConnectionSnippet {
  const origin = normalizedOrigin(input.origin)
  if (input.protocol === 'openai') {
    const path = '/v1/chat/completions'
    return {
      path,
      command: buildChatCompletionsSnippet(origin, input.model),
      language: 'bash',
    }
  }

  if (input.protocol === 'anthropic') {
    const path = '/v1/messages'
    const payload = quotePosixShellArgument(
      JSON.stringify({
        model: input.model,
        max_tokens: 64,
        messages: [{ role: 'user', content: 'Hello' }],
      }),
    )
    return {
      path,
      command: `curl "${origin}${path}" \\\n  -H "x-api-key: $GPT_LOAD_API_KEY" \\\n  -H "anthropic-version: 2023-06-01" \\\n  -H "Content-Type: application/json" \\\n  -d ${payload}`,
      language: 'bash',
    }
  }

  const modelPath = input.model === '<MODEL_ID>' ? input.model : encodeURIComponent(input.model)
  const path = `/v1beta/models/${modelPath}:generateContent`
  const payload = quotePosixShellArgument(
    JSON.stringify({ contents: [{ parts: [{ text: 'Hello' }] }] }),
  )
  return {
    path,
    command: `curl "${origin}${path}" \\\n  -H "x-goog-api-key: $GPT_LOAD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d ${payload}`,
    language: 'bash',
  }
}

export function isGroupServiceable(group: GroupSummary, counts?: KeyCounts): boolean | undefined {
  if (!group.enabled || group.models.length === 0) return false
  if (!counts) return undefined
  return counts.available > 0
}

export function selectInitialAccessKey(keys: AccessKeyOptionDto[]): number | undefined {
  return keys.find((key) => key.status === 'active')?.id ?? keys[0]?.id
}

export function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase()
  return (
    normalized === 'localhost' ||
    normalized.endsWith('.localhost') ||
    normalized === '127.0.0.1' ||
    normalized === '::1' ||
    normalized === '[::1]'
  )
}

export function normalizeUpstreamHost(upstreamUrl: string): string {
  try {
    return new URL(upstreamUrl).host
  } catch {
    return upstreamUrl
  }
}
