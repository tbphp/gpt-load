import { clearEphemeralState, registerEphemeralStateCleaner } from './ephemeral-state'

describe('ephemeral application state cleanup', () => {
  it('clears registered feature-local state and supports unregistering', () => {
    const first = vi.fn()
    const second = vi.fn()
    const unregisterFirst = registerEphemeralStateCleaner(first)
    const unregisterSecond = registerEphemeralStateCleaner(second)

    clearEphemeralState()
    expect(first).toHaveBeenCalledOnce()
    expect(second).toHaveBeenCalledOnce()

    unregisterFirst()
    unregisterSecond()
    clearEphemeralState()
    expect(first).toHaveBeenCalledOnce()
    expect(second).toHaveBeenCalledOnce()
  })
})
