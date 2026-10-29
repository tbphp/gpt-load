import { computed, onScopeDispose, ref, watch } from 'vue'

const resetAt = ref(0)

export function receiveDemoStatus(headers: Headers): void {
  const value = Number(headers.get('X-GPT-Load-Demo-Reset-At'))
  resetAt.value = Number.isSafeInteger(value) && value > 0 ? value : 0
}

// 倒计时只更新本地显示；普通实例不创建定时器，也不增加网络请求。
export function useDemoCountdown() {
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | undefined
  watch(
    resetAt,
    (value) => {
      clearInterval(timer)
      now.value = Date.now()
      if (value) timer = setInterval(() => (now.value = Date.now()), 1000)
    },
    { immediate: true },
  )
  onScopeDispose(() => clearInterval(timer))
  return computed(() => {
    if (!resetAt.value) return ''
    const seconds = Math.max(0, Math.ceil((resetAt.value - now.value) / 1000))
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
  })
}
