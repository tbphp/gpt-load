import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

import { useVisibleRefetch } from './use-visible-refetch'

function mountVisibleRefetch(refetchers: ReadonlyArray<() => unknown | Promise<unknown>>) {
  return mount(
    defineComponent({
      setup() {
        useVisibleRefetch(refetchers)
        return () => h('div')
      },
    }),
  )
}

describe('useVisibleRefetch', () => {
  it('calls every refetcher exactly once after hidden becomes visible', async () => {
    let hidden = true
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const first = vi.fn()
    const second = vi.fn()
    mountVisibleRefetch([first, second])

    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(first).not.toHaveBeenCalled()
    expect(second).not.toHaveBeenCalled()

    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(1)

    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(1)
  })

  it('removes the visibility listener after unmount', async () => {
    let hidden = true
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const refetch = vi.fn()
    const wrapper = mountVisibleRefetch([refetch])
    wrapper.unmount()

    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(refetch).not.toHaveBeenCalled()
  })

  it('settles each refetch independently when one rejects', async () => {
    let hidden = true
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const rejected = vi.fn().mockRejectedValue(new Error('refresh failed'))
    const resolved = vi.fn().mockResolvedValue(undefined)
    mountVisibleRefetch([rejected, resolved])

    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()

    expect(rejected).toHaveBeenCalledTimes(1)
    expect(resolved).toHaveBeenCalledTimes(1)
  })
})
