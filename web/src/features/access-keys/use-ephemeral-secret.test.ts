import { createEphemeralSecretController, ephemeralSecretTtlMs } from './use-ephemeral-secret'

describe('ephemeral AccessKey secret controller', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('retains plaintext for at most 60 seconds without extending on reads', () => {
    const controller = createEphemeralSecretController()
    controller.expose('access-key:9', 'sk-gl-SECRET')

    expect(controller.read('access-key:9')).toBe('sk-gl-SECRET')
    vi.advanceTimersByTime(ephemeralSecretTtlMs - 1)
    expect(controller.read('access-key:9')).toBe('sk-gl-SECRET')
    vi.advanceTimersByTime(1)
    expect(controller.read('access-key:9')).toBeNull()
    expect(controller.owner.value).toBeNull()
  })

  it('clears on conceal/close and replaces an older owner atomically', () => {
    const controller = createEphemeralSecretController()
    controller.expose('operation:a', 'secret-a')
    controller.expose('operation:b', 'secret-b')

    expect(controller.read('operation:a')).toBeNull()
    expect(controller.read('operation:b')).toBe('secret-b')
    controller.clear()
    expect(controller.secret.value).toBeNull()
    expect(controller.owner.value).toBeNull()

    vi.advanceTimersByTime(ephemeralSecretTtlMs)
    expect(controller.secret.value).toBeNull()
  })
})
