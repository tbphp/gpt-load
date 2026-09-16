/* surface 决定配置怎么呈现：cli 给可直接粘贴执行的终端命令，gui 给「填到哪个框」的字段对照。
   mirror 是该客户端设置界面里这几个框的原文标签，留空则退回通用标签。 */
export const gatewayClients = [
  {
    id: 'codex',
    name: 'Codex',
    protocol: 'openai-responses',
    kind: 'snippet',
    surface: 'cli',
    group: 'cli',
  },
  {
    id: 'claude-code',
    name: 'Claude Code',
    protocol: 'anthropic',
    kind: 'snippet',
    surface: 'cli',
    group: 'cli',
  },
  {
    id: 'gemini-cli',
    name: 'Gemini CLI',
    protocol: 'gemini',
    kind: 'snippet',
    surface: 'cli',
    group: 'cli',
  },
  {
    id: 'cline',
    name: 'Cline',
    protocol: 'openai-completions',
    kind: 'fields',
    surface: 'gui',
    group: 'cli',
    mirror: [
      { label: 'API Provider' },
      { label: 'Base URL', slot: 'endpoint' },
      { label: 'API Key', slot: 'apiKey' },
      { label: 'Model ID', slot: 'model' },
    ],
  },
  {
    id: 'cc-switch',
    name: 'CC Switch',
    protocol: '',
    kind: 'snippet',
    surface: 'gui',
    group: 'desktop',
  },
  {
    id: 'cherry-studio',
    name: 'Cherry Studio',
    protocol: 'openai-completions',
    kind: 'fields',
    surface: 'gui',
    group: 'desktop',
  },
  {
    id: 'nextchat',
    name: 'NextChat',
    protocol: 'openai-completions',
    kind: 'fields',
    surface: 'gui',
    group: 'desktop',
  },
  {
    id: 'open-webui',
    name: 'Open WebUI',
    protocol: 'openai-completions',
    kind: 'fields',
    surface: 'gui',
    group: 'desktop',
    mirror: [
      { label: 'OpenAI API' },
      { label: 'Base URL', slot: 'endpoint' },
      { label: 'API Key', slot: 'apiKey' },
    ],
  },
  {
    id: 'new-api',
    name: 'New API',
    protocol: '',
    kind: 'snippet',
    surface: 'gui',
    group: 'relay',
  },
  {
    id: 'curl',
    name: 'cURL',
    protocol: 'openai-completions',
    kind: 'snippet',
    surface: 'cli',
    group: 'relay',
  },
] as const
export const gatewayGroups = ['cli', 'desktop', 'relay'] as const
export type GatewayGroupID = (typeof gatewayGroups)[number]
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
export interface GatewaySelection {
  accessKeyID: number
  keyName: string
  protocol: string
  model: string
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

/* 这几个客户端自己会向网关拉模型列表，配置里不写死模型名。 */
const modelFreeClients: readonly string[] = [
  'gemini-cli',
  'new-api',
  'nextchat',
  'open-webui',
  'cherry-studio',
]
export function gatewayNeedsModel(client: GatewayClientID, target?: GatewayTargetID): boolean {
  if (client === 'cc-switch' && target)
    return Boolean(gatewayTargets.find((entry) => entry.id === target)?.requiresModel)
  return !modelFreeClients.includes(client)
}

export const gatewaySlots = ['endpoint', 'apiKey', 'model'] as const
export type GatewaySlot = (typeof gatewaySlots)[number]
export interface GatewayField {
  slot: GatewaySlot
  value: string
}
/* 图形客户端要手填的几项。模型名跟着 gatewayNeedsModel 走，
   不再出现「界面让你选了模型、配置里却没有它」的情况。 */
export function gatewayFields(config: GatewayConfig, key: string): GatewayField[] {
  const model = config.model.trim()
  return [
    { slot: 'endpoint' as const, value: gatewayEndpoint(config) },
    { slot: 'apiKey' as const, value: key },
    ...(gatewayNeedsModel(config.client, config.target)
      ? [{ slot: 'model' as const, value: model }]
      : []),
  ]
}

export interface MirrorRow {
  label?: string
  slot?: GatewaySlot
}
/**
 * 设置界面示意图的行。
 *
 * 只有登记过字段原文的客户端才写标签（目前是 Cline 与 Open WebUI，它们的
 * 英文标签在各语言界面里都一样）。其余客户端补两行无标签占位：示意图要读起来
 * 像「一个设置表单，其中这几个框填这些值」，而不是「这个界面只有三个框」。
 * 不替没核实过的客户端编字段名——那在别的语言界面下就是错的。
 */
export function gatewayMirror(
  client: GatewayClientID,
  fields: readonly GatewayField[],
  fallback: (slot: GatewaySlot) => string,
): MirrorRow[] {
  const declared = gatewayClients.find((item) => item.id === client)
  if (declared && 'mirror' in declared)
    return (declared.mirror as readonly MirrorRow[]).map((row) => ({ ...row }))
  return [{}, {}, ...fields.map((field) => ({ label: fallback(field.slot), slot: field.slot }))]
}
