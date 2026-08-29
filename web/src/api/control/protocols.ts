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
    value: 'openai-images',
    supportsProtocolOnlyRouting: false,
  },
  {
    value: 'openai-embeddings',
    supportsProtocolOnlyRouting: false,
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

export type ProtocolValue = (typeof protocolCatalog)[number]['value']

export const knownAccessProtocols = protocolCatalog.map(({ value }) => value) as ProtocolValue[]
export const enabledDataProtocols = knownAccessProtocols

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
