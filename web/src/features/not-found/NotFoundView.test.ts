import { QueryClient } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import { mountApp } from '@/test/mount-app'

import NotFoundView from './NotFoundView.vue'

describe('NotFoundView', () => {
  it('provides a localized heading, explanation, and Home recovery link', async () => {
    const { wrapper } = await mountApp(NotFoundView, {
      api: { request: vi.fn() as ApiClient['request'] },
      queryClient: new QueryClient(),
      path: '/missing/path',
      locale: 'en-US',
    })

    expect(wrapper.get('h1').text()).toBe('Page not found')
    expect(wrapper.text()).toContain('The requested management page does not exist')
    expect(wrapper.get('a').attributes('href')).toBe('/')
    expect(wrapper.get('a').text()).toBe('Back to Home')
  })
})
