import { buildUpstreamKeyPatch, type UpstreamKeyEditable } from './key-patch'

const base: UpstreamKeyEditable = { status: 'active', weight_manual: 50 }

describe('buildUpstreamKeyPatch', () => {
  it('returns an empty object for a normalized no-op', () => {
    expect(buildUpstreamKeyPatch(base, { ...base })).toEqual({})
  })

  it('includes only changed status and manual weight fields', () => {
    expect(buildUpstreamKeyPatch(base, { status: 'disabled', weight_manual: null })).toEqual({
      status: 'disabled',
      weight_manual: null,
    })
  })

  it('accepts an existing zero weight while keeping zero out of new selections', () => {
    const existingDisabledWeight: UpstreamKeyEditable = {
      status: 'active',
      weight_manual: 0,
    }

    expect(buildUpstreamKeyPatch(existingDisabledWeight, { ...existingDisabledWeight })).toEqual({})
    expect(
      buildUpstreamKeyPatch(existingDisabledWeight, {
        status: 'disabled',
        weight_manual: 0,
      }),
    ).toEqual({ status: 'disabled' })
    expect(
      buildUpstreamKeyPatch(existingDisabledWeight, {
        status: 'active',
        weight_manual: null,
      }),
    ).toEqual({ weight_manual: null })
  })

  it.each([
    { status: 'active', weight_manual: 0 },
    { status: 'active', weight_manual: 101 },
    { status: 'active', weight_manual: 1.5 },
  ])('rejects a manual weight outside the UI contract: %j', (next) => {
    expect(() => buildUpstreamKeyPatch(base, next as UpstreamKeyEditable)).toThrow()
  })
})
