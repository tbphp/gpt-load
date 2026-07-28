import { createUnsavedChangesController } from '@/app/unsaved-changes'

import { handleGlobalUnauthorized } from './unauthorized'

describe('handleGlobalUnauthorized', () => {
  it('captures synchronously before bypassing dirty navigation and clearing the credential', async () => {
    const order: string[] = []
    const replace = vi.fn(async () => {
      order.push('replace')
    })

    await handleGlobalUnauthorized({
      recovery: {
        captureForUnauthorized: () => {
          order.push('capture')
          return 'stored'
        },
      },
      unsavedChanges: { bypassNext: () => order.push('bypass'), consumeBypass: () => false },
      session: { hasCredential: () => true, clear: () => order.push('clear') },
      router: { replace },
      redirect: '/import?mode=new',
    })

    expect(order).toEqual(['capture', 'bypass', 'clear', 'replace'])
    expect(replace).toHaveBeenCalledWith({ name: 'login', query: { redirect: '/import?mode=new' } })
  })

  it('does not leave a stale dirty-navigation bypass after Login navigation completes', async () => {
    const unsavedChanges = createUnsavedChangesController()
    await handleGlobalUnauthorized({
      recovery: { captureForUnauthorized: () => 'no-active-draft' },
      unsavedChanges,
      session: { hasCredential: () => true, clear: () => {} },
      router: { replace: vi.fn(async () => {}) },
      redirect: '/',
    })
    expect(unsavedChanges.consumeBypass()).toBe(false)
  })

  it('continues credential cleanup and Login navigation when recovery storage is unavailable', async () => {
    const clear = vi.fn()
    const replace = vi.fn(async () => {})
    const bypassNext = vi.fn()
    await expect(
      handleGlobalUnauthorized({
        recovery: { captureForUnauthorized: () => 'storage-unavailable' },
        unsavedChanges: { bypassNext, consumeBypass: () => false },
        session: { hasCredential: () => true, clear },
        router: { replace },
        redirect: '/import',
      }),
    ).resolves.toBeUndefined()
    expect(bypassNext).toHaveBeenCalledOnce()
    expect(clear).toHaveBeenCalledOnce()
    expect(replace).toHaveBeenCalledOnce()
  })
})
