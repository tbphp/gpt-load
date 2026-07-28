import { enabledDataProtocols, knownAccessProtocols } from './protocols'

describe('frontend protocol capabilities', () => {
  it('separates every known AccessKey protocol from currently enabled data protocols', () => {
    expect(knownAccessProtocols).toEqual(['openai', 'anthropic', 'gemini', 'openai-response'])
    expect(enabledDataProtocols).toEqual(['openai', 'anthropic', 'gemini'])
  })

  it('keeps both capabilities unique and enabled as a strict subset of known', () => {
    expect(new Set(knownAccessProtocols).size).toBe(knownAccessProtocols.length)
    expect(new Set(enabledDataProtocols).size).toBe(enabledDataProtocols.length)
    expect(enabledDataProtocols.every((protocol) => knownAccessProtocols.includes(protocol))).toBe(
      true,
    )
    expect(enabledDataProtocols.length).toBeLessThan(knownAccessProtocols.length)
    expect(
      knownAccessProtocols.filter(
        (protocol) => !enabledDataProtocols.some((enabled) => enabled === protocol),
      ),
    ).toEqual(['openai-response'])
    expect(enabledDataProtocols).not.toContain('openai-response')
  })
})
