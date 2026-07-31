export type GatewayClientID = 'nextchat' | 'cherry-studio' | 'claude-code' | 'curl' | 'more'

export interface GatewayClient {
  id: GatewayClientID
  requiredProtocol?: 'openai-completions' | 'anthropic'
}

export const gatewayClients: readonly GatewayClient[] = [
  { id: 'nextchat', requiredProtocol: 'openai-completions' },
  { id: 'cherry-studio', requiredProtocol: 'openai-completions' },
  { id: 'claude-code', requiredProtocol: 'anthropic' },
  { id: 'curl', requiredProtocol: 'openai-completions' },
  { id: 'more' },
]

export function clientConfiguration(
  clientID: Exclude<GatewayClientID, 'more'>,
  origin: string,
  key: string,
): string {
  switch (clientID) {
    case 'nextchat':
      return JSON.stringify({ url: origin, key }, null, 2)
    case 'cherry-studio':
      return JSON.stringify(
        {
          apiHost: `${origin}/v1`,
          apiKey: key,
        },
        null,
        2,
      )
    case 'claude-code':
      return `export ANTHROPIC_BASE_URL="${origin}"\nexport ANTHROPIC_AUTH_TOKEN="${key}"`
    case 'curl':
      return [
        `curl "${origin}/v1/chat/completions" \\`,
        `  -H "Authorization: Bearer ${key}" \\`,
        '  -H "Content-Type: application/json" \\',
        `  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}'`,
      ].join('\n')
  }
}
