import { effectScope, ref } from 'vue'

import { useCountdown } from './use-countdown'

describe('useCountdown', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('rounds the initial value up with a minimum of one second', () => {
    const positive = useCountdown(2.2)
    const zero = useCountdown(0)

    expect(positive.seconds.value).toBe(3)
    expect(zero.seconds.value).toBe(1)
  })

  it('decrements every second without going below zero', () => {
    const countdown = useCountdown(2)

    vi.advanceTimersByTime(3_000)

    expect(countdown.seconds.value).toBe(0)
    expect(countdown.active.value).toBe(false)
  })

  it('stops its timer when the owning scope is disposed', () => {
    const scope = effectScope()
    const countdown = scope.run(() => useCountdown(3))
    expect(countdown).toBeDefined()

    vi.advanceTimersByTime(1_000)
    expect(countdown?.seconds.value).toBe(2)

    scope.stop()
    vi.advanceTimersByTime(2_000)

    expect(countdown?.seconds.value).toBe(2)
  })

  it('resets from a new server-provided value', () => {
    const serverSeconds = ref(5)
    const countdown = useCountdown(serverSeconds)

    vi.advanceTimersByTime(2_000)
    countdown.reset(1.2)

    expect(countdown.seconds.value).toBe(2)
    vi.advanceTimersByTime(2_000)
    expect(countdown.seconds.value).toBe(0)
  })
})
