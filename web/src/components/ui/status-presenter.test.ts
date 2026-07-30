import { presentMutationStatus, presentOperationalStatus } from './status-presenter'

describe('status presenter', () => {
  it.each([
    ['available', 'success', 'check'],
    ['cooldown', 'warning', 'alert'],
    ['blacklisted', 'danger', 'off'],
    ['disabled', 'neutral', 'off'],
    ['unavailable', 'danger', 'off'],
    ['unknown', 'neutral', 'help'],
  ] as const)('maps operational %s to %s with %s icon', (status, tone, icon) => {
    expect(presentOperationalStatus(status)).toEqual({ tone, icon })
  })

  it.each([
    ['confirmed', 'success', 'check'],
    ['failed', 'danger', 'off'],
    ['indeterminate', 'warning', 'alert'],
    ['reconciling', 'warning', 'progress'],
  ] as const)('maps mutation %s to %s with %s icon', (status, tone, icon) => {
    expect(presentMutationStatus(status)).toEqual({ tone, icon })
  })
})
