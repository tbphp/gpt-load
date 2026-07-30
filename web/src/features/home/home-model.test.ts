import type { AccessKeyOptionDto, GroupSummary } from '@/api/control/types'
import type { KeyCounts } from '@/app/resources/health'

import {
  buildConnectionSnippet,
  buildChatCompletionsSnippet,
  isGroupServiceable,
  isLoopbackHostname,
  normalizeUpstreamHost,
  quotePosixShellArgument,
  selectInitialAccessKey,
} from './home-model'

const group: GroupSummary = {
  id: 1,
  name: 'Example',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai-chat-completions'],
  models: [{ id: 'gpt-real', alias: '' }],
  enabled: true,
  key_count: 1,
}
const counts: KeyCounts = {
  total: 1,
  available: 1,
  cooldown: 0,
  blacklisted: 0,
  disabled: 0,
}
const accessKey = (id: number, status: AccessKeyOptionDto['status']): AccessKeyOptionDto => ({
  id,
  name: `key-${id}`,
  status,
})

describe('Home model', () => {
  it.each(['localhost', 'api.localhost', '127.0.0.1', '::1', '[::1]'])(
    'detects loopback hostname %s',
    (hostname) => expect(isLoopbackHostname(hostname)).toBe(true),
  )

  it.each(['localhost.example', '127.0.0.2', '192.168.1.10', 'example.com'])(
    'does not classify network hostname %s as loopback',
    (hostname) => expect(isLoopbackHostname(hostname)).toBe(false),
  )

  it('returns unknown serviceability without health counts', () => {
    expect(isGroupServiceable(group)).toBeUndefined()
  })

  it('requires an enabled Group, a routable capability, and an available key', () => {
    expect(isGroupServiceable(group, counts)).toBe(true)
    expect(isGroupServiceable({ ...group, enabled: false }, counts)).toBe(false)
    expect(isGroupServiceable({ ...group, models: [] }, counts)).toBe(false)
    expect(
      isGroupServiceable({ ...group, protocols: ['openai-responses'], models: [] }, counts),
    ).toBe(true)
    expect(isGroupServiceable(group, { ...counts, available: 0 })).toBe(false)
  })

  it('selects the first active AccessKey in backend order, otherwise the first key', () => {
    expect(
      selectInitialAccessKey([
        accessKey(1, 'disabled'),
        accessKey(2, 'active'),
        accessKey(3, 'active'),
      ]),
    ).toBe(2)
    expect(selectInitialAccessKey([accessKey(4, 'disabled')])).toBe(4)
    expect(selectInitialAccessKey([])).toBeUndefined()
  })

  it('shows only the normalized upstream host', () => {
    expect(normalizeUpstreamHost(group.upstream_url)).toBe('api.example.com')
    expect(normalizeUpstreamHost('not a url')).toBe('not a url')
  })

  it.each([
    'gpt-4o',
    "model'with-quote",
    'model\nwith-newline',
    String.raw`model\with\slashes`,
    `safe"}' ; printf MODEL_INJECTION_REACHED ; : #`,
  ])('quotes model %j as one POSIX shell argument', (model) => {
    const payload = JSON.stringify({ model })
    const quoted = quotePosixShellArgument(payload)
    const escape = `'"'"'`

    expect(quoted.startsWith("'")).toBe(true)
    expect(quoted.endsWith("'")).toBe(true)
    expect(quoted.slice(1, -1).split(escape).join("'")).toBe(payload)
    expect(quoted.slice(1, -1).split(escape).join('')).not.toContain("'")
    expect(buildChatCompletionsSnippet('https://gateway.example.com', model)).toContain(
      `-d ${quoted}`,
    )
  })

  it('keeps the ordinary curl snippet unchanged', () => {
    expect(buildChatCompletionsSnippet('https://gateway.example.com', 'gpt-4o')).toBe(
      `curl "https://gateway.example.com/v1/chat/completions" \\\n  -H "Authorization: Bearer $GPT_LOAD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"gpt-4o"}'`,
    )
  })

  it.each([
    [
      'openai-chat-completions',
      '/v1/chat/completions',
      'Authorization: Bearer $GPT_LOAD_API_KEY',
      undefined,
    ],
    [
      'openai-responses',
      '/v1/responses',
      'Authorization: Bearer $GPT_LOAD_API_KEY',
      '"input":"Hello"',
    ],
    ['anthropic', '/v1/messages', 'x-api-key: $GPT_LOAD_API_KEY', 'anthropic-version:'],
    [
      'gemini',
      '/v1beta/models/gemini-2.5-flash:generateContent',
      'x-goog-api-key: $GPT_LOAD_API_KEY',
      undefined,
    ],
  ] as const)(
    'builds the native %s endpoint and authentication contract',
    (protocol, path, authentication, extraHeader) => {
      const snippet = buildConnectionSnippet({
        origin: 'https://gateway.example.com/',
        protocol,
        model: protocol === 'gemini' ? 'gemini-2.5-flash' : 'model-real',
      })

      expect(snippet.path).toBe(path)
      expect(snippet.language).toBe('bash')
      expect(snippet.command).toContain(`https://gateway.example.com${path}`)
      expect(snippet.command).toContain(authentication)
      if (extraHeader) expect(snippet.command).toContain(extraHeader)
      if (protocol === 'openai-responses') expect(snippet.command).toContain('"store":false')
      expect(snippet.command).not.toContain('ACCESS_KEY_CANARY')
    },
  )

  it('keeps an explicit model placeholder readable in the Gemini path', () => {
    expect(
      buildConnectionSnippet({
        origin: 'https://gateway.example.com',
        protocol: 'gemini',
        model: '<MODEL_ID>',
      }).path,
    ).toBe('/v1beta/models/<MODEL_ID>:generateContent')
  })
})
