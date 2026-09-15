export const gatewayClients = [
  { id: 'codex', name: 'Codex', protocol: 'openai-responses', kind: 'snippet' },
  { id: 'claude-code', name: 'Claude Code', protocol: 'anthropic', kind: 'snippet' },
  { id: 'gemini-cli', name: 'Gemini CLI', protocol: 'gemini', kind: 'snippet' },
  { id: 'cc-switch', name: 'CC Switch', protocol: '', kind: 'snippet' },
  { id: 'cherry-studio', name: 'Cherry Studio', protocol: 'openai-completions', kind: 'fields' },
  { id: 'nextchat', name: 'NextChat', protocol: 'openai-completions', kind: 'fields' },
  { id: 'open-webui', name: 'Open WebUI', protocol: 'openai-completions', kind: 'fields' },
  { id: 'cline', name: 'Cline', protocol: 'openai-completions', kind: 'fields' },
  { id: 'new-api', name: 'New API', protocol: '', kind: 'snippet' },
  { id: 'curl', name: 'cURL', protocol: 'openai-completions', kind: 'snippet' },
] as const
export type GatewayClientID = (typeof gatewayClients)[number]['id']
export const gatewayTargets = [
  { id: 'claude', name: 'Claude Code', protocol: 'anthropic', requiresModel: false },
  { id: 'codex', name: 'Codex', protocol: 'openai-responses', requiresModel: true },
  { id: 'gemini', name: 'Gemini CLI', protocol: 'gemini', requiresModel: false },
  { id: 'opencode', name: 'OpenCode', protocol: 'openai-completions', requiresModel: true },
] as const
export type GatewayTargetID = (typeof gatewayTargets)[number]['id']
export interface GatewayConfig {
  client: GatewayClientID
  origin: string
  model: string
  target: GatewayTargetID
  name: string
}
export interface ConfigBlock {
  label: string
  content: string
}
const shellQuote = (value: string) => "'" + value.replaceAll("'", "'\\''") + "'"
const tomlQuote = (value: string) => JSON.stringify(value)
export function gatewayEndpoint(config: GatewayConfig): string {
  const root = config.origin.replace(/\/+$/, '')
  return ['claude-code', 'gemini-cli', 'nextchat', 'new-api'].includes(config.client) ||
    (config.client === 'cc-switch' && ['claude', 'gemini'].includes(config.target))
    ? root
    : root + '/v1'
}
export function gatewayConfiguration(config: GatewayConfig, key: string): ConfigBlock[] {
  const endpoint = gatewayEndpoint(config)
  const model = config.model.trim() || 'YOUR_MODEL'
  const env = (name: string, value: string) => `export ${name}=${shellQuote(value)}`
  switch (config.client) {
    case 'codex':
      return [
        {
          label: '~/.codex/config.toml',
          content: [
            `model = ${tomlQuote(model)}`,
            'model_provider = "gpt-load"',
            '',
            '[model_providers.gpt-load]',
            'name = "GPT-Load"',
            `base_url = ${tomlQuote(endpoint)}`,
            'env_key = "GPT_LOAD_API_KEY"',
            'wire_api = "responses"',
          ].join('\n'),
        },
        { label: 'shell', content: env('GPT_LOAD_API_KEY', key) },
      ]
    case 'claude-code':
      return [
        {
          label: 'shell',
          content: [
            env('ANTHROPIC_BASE_URL', endpoint),
            env('ANTHROPIC_AUTH_TOKEN', key),
            env('ANTHROPIC_MODEL', model),
            'export ANTHROPIC_CUSTOM_MODEL_OPTION="$ANTHROPIC_MODEL"',
            'export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY="1"',
          ].join('\n'),
        },
      ]
    case 'gemini-cli':
      return [
        {
          label: 'shell',
          content: [env('GOOGLE_GEMINI_BASE_URL', endpoint), env('GEMINI_API_KEY', key)].join('\n'),
        },
      ]
    case 'cc-switch':
      return [
        {
          label: 'JSON',
          content: JSON.stringify(
            {
              app: config.target,
              name: config.name,
              endpoint,
              apiKey: key,
              enabled: true,
              ...(config.model.trim() ? { model: config.model.trim() } : {}),
            },
            null,
            2,
          ),
        },
      ]
    case 'new-api':
      return [
        {
          label: 'JSON',
          content: JSON.stringify({ _type: 'newapi_channel_conn', key, url: endpoint }, null, 2),
        },
      ]
    case 'curl':
      return [
        {
          label: 'shell',
          content: [
            `curl ${shellQuote(endpoint + '/chat/completions')} \\`,
            `  -H ${shellQuote('Authorization: Bearer ' + key)} \\`,
            "  -H 'Content-Type: application/json' \\",
            `  -d ${shellQuote(JSON.stringify({ model, messages: [{ role: 'user', content: 'ping' }] }))}`,
          ].join('\n'),
        },
      ]
    default:
      return []
  }
}
export function gatewayImportURL(config: GatewayConfig, key: string): string {
  if (config.client === 'cc-switch') {
    const params = new URLSearchParams({
      resource: 'provider',
      app: config.target,
      name: config.name,
      homepage: config.origin,
      endpoint: gatewayEndpoint(config),
      apiKey: key,
      enabled: 'true',
    })
    if (config.model.trim()) params.set('model', config.model.trim())
    return `ccswitch://v1/import?${params}`
  }
  if (config.client === 'cherry-studio') {
    const value = JSON.stringify({
      id: 'gpt-load',
      name: config.name,
      type: 'openai',
      baseUrl: gatewayEndpoint(config),
      apiKey: key,
    })
    const bytes = new TextEncoder().encode(value)
    let binary = ''
    for (const byte of bytes) binary += String.fromCharCode(byte)
    const data = btoa(binary).replaceAll('+', '-').replaceAll('/', '_')
    return `cherrystudio://providers/api-keys?${new URLSearchParams({ v: '1', data })}`
  }
  throw new Error('UNSUPPORTED_GATEWAY_IMPORT')
}
