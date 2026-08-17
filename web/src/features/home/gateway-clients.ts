import type { AccessProtocol } from '@/api/control/types'

export type GatewayClientID =
  | 'cc-switch'
  | 'new-api'
  | 'codex'
  | 'nextchat'
  | 'cherry-studio'
  | 'claude-code'
  | 'open-webui'
  | 'cline'
  | 'curl'

export type GatewayClientKind =
  | 'desktopManager'
  | 'gateway'
  | 'desktopWeb'
  | 'desktop'
  | 'web'
  | 'extension'
  | 'commandLine'
  | 'general'

export interface GatewayClient {
  id: GatewayClientID
  kind: GatewayClientKind
  requiredProtocol?: AccessProtocol
  quickImport?: boolean
}

export type CCSwitchTargetID = 'codex' | 'claude' | 'gemini' | 'opencode'

export interface CCSwitchTarget {
  id: CCSwitchTargetID
  requiredProtocol: AccessProtocol
  requiresModel: boolean
}

export const gatewayClients: readonly GatewayClient[] = [
  { id: 'cc-switch', kind: 'desktopManager', quickImport: true },
  { id: 'new-api', kind: 'gateway' },
  { id: 'codex', kind: 'commandLine', requiredProtocol: 'openai-responses' },
  {
    id: 'nextchat',
    kind: 'desktopWeb',
    requiredProtocol: 'openai-completions',
  },
  {
    id: 'cherry-studio',
    kind: 'desktop',
    requiredProtocol: 'openai-completions',
    quickImport: true,
  },
  { id: 'claude-code', kind: 'commandLine', requiredProtocol: 'anthropic' },
  { id: 'open-webui', kind: 'web', requiredProtocol: 'openai-completions' },
  { id: 'cline', kind: 'extension', requiredProtocol: 'openai-completions' },
  { id: 'curl', kind: 'general', requiredProtocol: 'openai-completions' },
]

export const ccSwitchTargets: readonly CCSwitchTarget[] = [
  { id: 'claude', requiredProtocol: 'anthropic', requiresModel: false },
  { id: 'codex', requiredProtocol: 'openai-responses', requiresModel: true },
  { id: 'gemini', requiredProtocol: 'gemini', requiresModel: false },
  { id: 'opencode', requiredProtocol: 'openai-completions', requiresModel: true },
]

export function clientRequiredProtocol(
  client: GatewayClient,
  ccSwitchTarget: CCSwitchTarget,
): AccessProtocol | undefined {
  return client.id === 'cc-switch' ? ccSwitchTarget.requiredProtocol : client.requiredProtocol
}

export function clientConfiguration(
  clientID: GatewayClientID,
  origin: string,
  key: string,
  ccSwitchTarget: CCSwitchTargetID = 'claude',
  model = '',
  ccSwitchProviderName = 'GPT-Load',
): string {
  switch (clientID) {
    case 'cc-switch': {
      const parameters: Record<string, string | boolean> = {
        app: ccSwitchTarget,
        name: ccSwitchProviderName,
        endpoint: ccSwitchEndpoint(origin, ccSwitchTarget),
        apiKey: key,
        enabled: true,
      }
      if (model.trim()) parameters.model = model.trim()
      return JSON.stringify(parameters, null, 2)
    }
    case 'new-api':
      return JSON.stringify(
        {
          _type: 'newapi_channel_conn',
          key,
          url: origin,
        },
        null,
        2,
      )
    case 'codex':
      return [
        'model_provider = "gpt-load"',
        '',
        '[model_providers.gpt-load]',
        'name = "GPT-Load"',
        `base_url = "${openAIBaseURL(origin)}"`,
        'env_key = "GPT_LOAD_API_KEY"',
        'wire_api = "responses"',
      ].join('\n')
    case 'nextchat':
      return JSON.stringify({ url: origin, key }, null, 2)
    case 'cherry-studio':
      return JSON.stringify(
        {
          id: 'gpt-load',
          name: 'GPT-Load',
          type: 'openai',
          baseUrl: openAIBaseURL(origin),
          apiKey: key,
        },
        null,
        2,
      )
    case 'claude-code':
      return [
        `export ANTHROPIC_BASE_URL="${origin}"`,
        `export ANTHROPIC_AUTH_TOKEN="${key}"`,
        'export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY="1"',
      ].join('\n')
    case 'open-webui':
      return JSON.stringify(
        {
          url: openAIBaseURL(origin),
          apiKey: key,
        },
        null,
        2,
      )
    case 'cline':
      return JSON.stringify(
        {
          provider: 'OpenAI Compatible',
          baseUrl: openAIBaseURL(origin),
          apiKey: key,
          modelId: 'YOUR_MODEL',
        },
        null,
        2,
      )
    case 'curl':
      return [
        `curl "${openAIBaseURL(origin)}/chat/completions" \\`,
        `  -H "Authorization: Bearer ${key}" \\`,
        '  -H "Content-Type: application/json" \\',
        `  -d '{"model":"YOUR_MODEL","messages":[{"role":"user","content":"ping"}]}'`,
      ].join('\n')
  }
}

export function clientQuickImportURL(
  clientID: GatewayClientID,
  origin: string,
  key: string,
  ccSwitchTarget: CCSwitchTargetID = 'claude',
  model = '',
  ccSwitchProviderName = 'GPT-Load',
): string | null {
  switch (clientID) {
    case 'cc-switch': {
      const params = new URLSearchParams({
        resource: 'provider',
        app: ccSwitchTarget,
        name: ccSwitchProviderName,
        homepage: origin,
        endpoint: ccSwitchEndpoint(origin, ccSwitchTarget),
        apiKey: key,
        enabled: 'true',
      })
      if (model.trim()) params.set('model', model.trim())
      return `ccswitch://v1/import?${params.toString()}`
    }
    case 'cherry-studio': {
      const payload = encodeURLSafeBase64(
        JSON.stringify({
          id: 'gpt-load',
          name: 'GPT-Load',
          type: 'openai',
          baseUrl: openAIBaseURL(origin),
          apiKey: key,
        }),
      )
      return `cherrystudio://providers/api-keys?${new URLSearchParams({ v: '1', data: payload })}`
    }
    default:
      return null
  }
}

function ccSwitchEndpoint(origin: string, target: CCSwitchTargetID): string {
  return target === 'codex' || target === 'opencode' ? openAIBaseURL(origin) : origin
}

function openAIBaseURL(origin: string): string {
  return `${origin.replace(/\/+$/, '')}/v1`
}

function encodeURLSafeBase64(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replaceAll('+', '_').replaceAll('/', '-')
}
