import { createDirtyNavigationController } from '@/features/import/use-dirty-navigation'

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
      dirtyNavigation: { bypassNext: () => order.push('bypass'), consumeBypass: () => false },
      session: { clear: () => order.push('clear') },
      router: { replace },
      redirect: '/import?mode=new',
    })

    expect(order).toEqual(['capture', 'bypass', 'clear', 'replace'])
    expect(replace).toHaveBeenCalledWith({ name: 'login', query: { redirect: '/import?mode=new' } })
  })

  it('does not leave a stale dirty-navigation bypass after Login navigation completes', async () => {
    const dirtyNavigation = createDirtyNavigationController()
    await handleGlobalUnauthorized({
      recovery: { captureForUnauthorized: () => 'no-active-draft' },
      dirtyNavigation,
      session: { clear: () => {} },
      router: { replace: vi.fn(async () => {}) },
      redirect: '/',
    })
    expect(dirtyNavigation.consumeBypass()).toBe(false)
  })

  it('continues credential cleanup and Login navigation when recovery storage is unavailable', async () => {
    const clear = vi.fn()
    const replace = vi.fn(async () => {})
    const bypassNext = vi.fn()
    await expect(
      handleGlobalUnauthorized({
        recovery: { captureForUnauthorized: () => 'storage-unavailable' },
        dirtyNavigation: { bypassNext, consumeBypass: () => false },
        session: { clear },
        router: { replace },
        redirect: '/import',
      }),
    ).resolves.toBeUndefined()
    expect(bypassNext).toHaveBeenCalledOnce()
    expect(clear).toHaveBeenCalledOnce()
    expect(replace).toHaveBeenCalledOnce()
  })
})
