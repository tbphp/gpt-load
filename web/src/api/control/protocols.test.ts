import {
  enabledDataProtocols,
  isDataProtocol,
  knownAccessProtocols,
  protocolCatalog,
  protocolLabelKey,
  supportsProtocolOnlyRouting,
} from './protocols'

describe('frontend protocol capabilities', () => {
  it('defines one canonical enabled catalog in backend order', () => {
    expect(protocolCatalog).toEqual([
      {
        value: 'openai-chat-completions',
        labelKey: 'common.protocols.openai-chat-completions',
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
    ])
    expect(knownAccessProtocols).toEqual([
      'openai-chat-completions',
      'openai-responses',
      'anthropic',
      'gemini',
    ])
    expect(enabledDataProtocols).toEqual(knownAccessProtocols)
  })

  it('derives guards and labels from that catalog', () => {
    expect(new Set(knownAccessProtocols).size).toBe(knownAccessProtocols.length)
    expect(new Set(enabledDataProtocols).size).toBe(enabledDataProtocols.length)
    expect(isDataProtocol('openai-responses')).toBe(true)
    expect(isDataProtocol('openai-chat-completions')).toBe(true)
    expect(isDataProtocol('openai')).toBe(false)
    expect(isDataProtocol('openai-response')).toBe(false)
    expect(protocolLabelKey('openai-chat-completions')).toBe(
      'common.protocols.openai-chat-completions',
    )
    expect(supportsProtocolOnlyRouting(['openai-responses'])).toBe(true)
    expect(supportsProtocolOnlyRouting(['openai-chat-completions'])).toBe(false)
    expect(supportsProtocolOnlyRouting(['openai-chat-completions', 'openai-responses'])).toBe(true)
  })
})
