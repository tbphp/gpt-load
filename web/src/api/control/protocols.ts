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

export type ProtocolValue = (typeof protocolCatalog)[number]['value']
export type ProtocolLabelKey = (typeof protocolCatalog)[number]['labelKey']

export const knownAccessProtocols = protocolCatalog.map(({ value }) => value) as ProtocolValue[]
export const enabledDataProtocols = knownAccessProtocols

const protocolValues = new Set<string>(enabledDataProtocols)
const labelKeys = new Map<ProtocolValue, ProtocolLabelKey>(
  protocolCatalog.map(({ value, labelKey }) => [value, labelKey]),
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

export function supportsProtocolOnlyRouting(values: readonly ProtocolValue[]): boolean {
  return values.some((value) => protocolOnlyValues.has(value))
}
