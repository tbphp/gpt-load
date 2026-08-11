export const protocolCatalog = [
  {
    value: 'openai-completions',
    labelKey: 'common.protocols.openai-completions',
    supportsProtocolOnlyRouting: false,
  },
  {
    value: 'openai-responses',
    labelKey: 'common.protocols.openai-responses',
    supportsProtocolOnlyRouting: true,
  },
  {
    value: 'anthropic',
    labelKey: 'common.protocols.anthropic',
    supportsProtocolOnlyRouting: false,
  },
  {
    value: 'gemini',
    labelKey: 'common.protocols.gemini',
    supportsProtocolOnlyRouting: false,
  },
] as const

export const upstreamAPICatalog = [
  {
    value: 'openai-chat-completions',
    labelKey: 'common.upstreamApis.openai-chat-completions',
  },
  {
    value: 'openai-responses',
    labelKey: 'common.upstreamApis.openai-responses',
  },
  {
    value: 'anthropic-messages',
    labelKey: 'common.upstreamApis.anthropic-messages',
  },
  {
    value: 'gemini-generate-content',
    labelKey: 'common.upstreamApis.gemini-generate-content',
  },
  {
    value: 'openai-models',
    labelKey: 'common.upstreamApis.openai-models',
  },
  {
    value: 'anthropic-models',
    labelKey: 'common.upstreamApis.anthropic-models',
  },
  {
    value: 'gemini-models',
    labelKey: 'common.upstreamApis.gemini-models',
  },
  {
    value: 'azure-openai',
    labelKey: 'common.upstreamApis.azure-openai',
  },
  {
    value: 'aws-bedrock',
    labelKey: 'common.upstreamApis.aws-bedrock',
  },
  {
    value: 'google-vertex',
    labelKey: 'common.upstreamApis.google-vertex',
  },
] as const

export type ProtocolValue = (typeof protocolCatalog)[number]['value']
export type ProtocolLabelKey = (typeof protocolCatalog)[number]['labelKey']
export type UpstreamAPIValue = (typeof upstreamAPICatalog)[number]['value']
export type UpstreamAPILabelKey = (typeof upstreamAPICatalog)[number]['labelKey']

export const knownAccessProtocols = protocolCatalog.map(({ value }) => value) as ProtocolValue[]
export const enabledDataProtocols = knownAccessProtocols
export const knownUpstreamAPIs = upstreamAPICatalog.map(({ value }) => value) as UpstreamAPIValue[]

const protocolValues = new Set<string>(enabledDataProtocols)
const labelKeys = new Map<ProtocolValue, ProtocolLabelKey>(
  protocolCatalog.map(({ value, labelKey }) => [value, labelKey]),
)
const upstreamAPILabelKeys = new Map<UpstreamAPIValue, UpstreamAPILabelKey>(
  upstreamAPICatalog.map(({ value, labelKey }) => [value, labelKey]),
)
const protocolOnlyValues = new Set<ProtocolValue>(
  protocolCatalog
    .filter(({ supportsProtocolOnlyRouting }) => supportsProtocolOnlyRouting)
    .map(({ value }) => value),
)

export function isDataProtocol(value: unknown): value is ProtocolValue {
  return typeof value === 'string' && protocolValues.has(value)
}

export function protocolLabelKey(value: ProtocolValue): ProtocolLabelKey {
  const label = labelKeys.get(value)
  if (label === undefined) throw new Error(`Unknown protocol: ${value}`)
  return label
}

export function upstreamAPILabelKey(value: UpstreamAPIValue): UpstreamAPILabelKey {
  const label = upstreamAPILabelKeys.get(value)
  if (label === undefined) throw new Error(`Unknown upstream API: ${value}`)
  return label
}

export function supportsProtocolOnlyRouting(values: readonly ProtocolValue[]): boolean {
  return values.some((value) => protocolOnlyValues.has(value))
}
