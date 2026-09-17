import { QueryClient } from '@tanstack/vue-query'
import { concurrencyQueryKey } from '@modern/api/concurrency'

export function createModernQueryClient() {
  const client = new QueryClient({
    defaultOptions: {
      // 使用查询库默认的 visibilitychange 监听与页面挂载刷新，不监听窗口 focus。
      // staleTime 只决定事件触发时是否更新缓存，不会产生定时请求。
      queries: {
        retry: false,
        staleTime: 0,
        refetchInterval: false,
        refetchIntervalInBackground: false,
        refetchOnWindowFocus: true,
        refetchOnReconnect: false,
        refetchOnMount: true,
      },
      mutations: { retry: false },
    },
  })
  // Active concurrency changes while requests are running. Only these small,
  // batched reads poll; the shared background/visibility policy still applies.
  client.setQueryDefaults(concurrencyQueryKey, { refetchInterval: 3000 })
  return client
}
