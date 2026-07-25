import { enableAutoUnmount } from '@vue/test-utils'
import { afterEach, vi } from 'vitest'

enableAutoUnmount(afterEach)

afterEach(() => {
  window.sessionStorage.clear()
  window.localStorage.clear()
  document.documentElement.lang = 'zh-CN'
  document.documentElement.removeAttribute('data-theme')
  document.title = ''
  vi.restoreAllMocks()
  vi.useRealTimers()
})
