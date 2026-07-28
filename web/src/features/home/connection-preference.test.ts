import { createConnectionPreference } from './connection-preference'

describe('connection preference', () => {
  it('defaults expanded and restores only an explicit collapsed preference', () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    }
    const first = createConnectionPreference(storage)
    expect(first.initialExpanded).toBe(true)

    first.setExpanded(false)

    expect(createConnectionPreference(storage).initialExpanded).toBe(false)
  })

  it('remains expanded and usable when storage is denied', () => {
    const preference = createConnectionPreference({
      getItem() {
        throw new DOMException('denied')
      },
      setItem() {
        throw new DOMException('denied')
      },
    })

    expect(preference.initialExpanded).toBe(true)
    expect(() => preference.setExpanded(false)).not.toThrow()
  })
})
