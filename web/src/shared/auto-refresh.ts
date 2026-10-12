import { onScopeDispose, toValue, watch, type MaybeRefOrGetter } from 'vue'

// 自动刷新只提供固定档位，最短 5 秒，避免高频轮询。
export const autoRefreshIntervals = [5_000, 10_000, 30_000, 60_000] as const
const maxBackoffMs = 5 * 60_000
// 选择的档位记在浏览器里，两套前端共用；默认关闭，读取失败或值不在档位内时按关闭处理。
const storageKey = 'gpt-load.auto-refresh'
const legacyStorageKey = 'gpt-load.logs-auto-refresh'

export function readAutoRefreshInterval(): number {
  try {
    // 沿用此前保存的日志页档位，后续只写统一设置。
    const stored = Number(
      window.localStorage.getItem(storageKey) ?? window.localStorage.getItem(legacyStorageKey),
    )
    if ((autoRefreshIntervals as readonly number[]).includes(stored)) return stored
  } catch {
    // 无法读取偏好时保持默认关闭。
  }
  return 0
}

// 返回是否保存成功；存储不可用时本次选择仍然有效，只是不会被记住。
export function saveAutoRefreshInterval(value: number): boolean {
  try {
    if (value > 0) window.localStorage.setItem(storageKey, String(value))
    else window.localStorage.removeItem(storageKey)
    window.localStorage.removeItem(legacyStorageKey)
    return true
  } catch {
    return false
  }
}

export interface AutoRefreshOptions {
  // 0 表示关闭。
  interval: MaybeRefOrGetter<number>
  // 静默刷新一次并返回是否成功；失败后按指数退避重试。
  run: () => Promise<boolean>
  // 已有请求进行中时跳过本轮，等待下一次计时。
  busy?: () => boolean
}

export function useAutoRefresh({ interval, run, busy }: AutoRefreshOptions): void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let generation = 0
  let failures = 0

  function stop(): void {
    clearTimeout(timer)
    timer = undefined
    generation++
  }

  async function tick(): Promise<void> {
    stop()
    const period = toValue(interval)
    // 标签页在后台时暂停，回到前台后立即刷新一次再继续计时。
    if (period <= 0 || document.hidden) return
    // 跳过的轮次不计成败，保留当前退避进度。
    if (!busy?.()) {
      const current = generation
      const ok = await run()
      if (current !== generation) return
      failures = ok ? 0 : failures + 1
    }
    timer = setTimeout(tick, failures ? Math.min(period * 2 ** failures, maxBackoffMs) : period)
  }

  function syncVisibility(): void {
    if (document.hidden) stop()
    else void tick()
  }

  // 开启或切换间隔时立即刷新一次，让用户马上看到生效。
  watch(
    () => toValue(interval),
    () => {
      failures = 0
      void tick()
    },
    { immediate: true },
  )
  document.addEventListener('visibilitychange', syncVisibility)
  onScopeDispose(() => {
    stop()
    document.removeEventListener('visibilitychange', syncVisibility)
  })
}
