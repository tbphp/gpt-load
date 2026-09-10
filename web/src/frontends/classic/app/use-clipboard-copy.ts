import { onScopeDispose, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { copyText } from '@/lib/clipboard'

import { registerEphemeralStateCleaner } from './ephemeral-state'

export type ClipboardCopyResult = 'success' | 'fallback' | 'cancelled'

export function useClipboardCopy() {
  const fallbackText = ref<string>()
  const pending = ref(false)
  const route = useRoute()
  let sequence = 0
  let disposed = false

  function reset(): void {
    sequence++
    fallbackText.value = undefined
    pending.value = false
  }

  async function copy(
    source: string | (() => string | Promise<string>),
  ): Promise<ClipboardCopyResult> {
    if (disposed) return 'cancelled'
    reset()
    const operation = sequence
    const isCurrent = () => !disposed && sequence === operation
    pending.value = true
    try {
      // 已有文本直接复制，避免在用户点击与兼容复制之间额外等待。
      const value = typeof source === 'function' ? await source() : source
      if (!isCurrent()) return 'cancelled'
      const copied = await copyText(value, undefined, isCurrent)
      if (!isCurrent()) return 'cancelled'
      if (copied) return 'success'
      fallbackText.value = value
      return 'fallback'
    } catch (error) {
      if (!isCurrent()) return 'cancelled'
      throw error
    } finally {
      if (isCurrent()) pending.value = false
    }
  }

  watch(() => route.fullPath, reset, { flush: 'sync' })
  const unregister = registerEphemeralStateCleaner(reset)
  onScopeDispose(() => {
    disposed = true
    reset()
    unregister()
  })

  return { copy, fallbackText, pending, reset }
}
