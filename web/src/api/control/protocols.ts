export const protocolCatalog = [
  {
    value: 'openai-completions',
    supportsProtocolOnlyRouting: false,
  },
  {
    value: 'openai-responses',
    supportsProtocolOnlyRouting: true,
  },
  {
    value: 'anthropic',
    supportsProtocolOnlyRouting: false,
  },
  {
    value: 'gemini',
    supportsProtocolOnlyRouting: false,
  },
] as const

export const upstreamAPICatalog = [
  'openai-chat-completions',
  'openai-responses',
  'anthropic-messages',
  'gemini-generate-content',
  'openai-models',
  'anthropic-models',
  'gemini-models',
  'azure-openai',
  'aws-bedrock',
  'google-vertex',
] as const

export type ProtocolValue = (typeof protocolCatalog)[number]['value']
export type UpstreamAPIValue = (typeof upstreamAPICatalog)[number]

export const knownAccessProtocols = protocolCatalog.map(({ value }) => value) as ProtocolValue[]
export const enabledDataProtocols = knownAccessProtocols
export const knownUpstreamAPIs = [...upstreamAPICatalog]

const protocolValues = new Set<string>(enabledDataProtocols)
const protocolOnlyValues = new Set<ProtocolValue>(
  protocolCatalog
    .filter(({ supportsProtocolOnlyRouting }) => supportsProtocolOnlyRouting)
    .map(({ value }) => value),
)

export function isDataProtocol(value: unknown): value is ProtocolValue {
  return typeof value === 'string' && protocolValues.has(value)
}

export function supportsProtocolOnlyRouting(values: readonly ProtocolValue[]): boolean {
  return values.some((value) => protocolOnlyValues.has(value))
}
