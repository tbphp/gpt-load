import { ref, watch, type Ref } from 'vue'
import {
  isNavigationFailure,
  useRoute,
  useRouter,
  type LocationQuery,
  type LocationQueryRaw,
} from 'vue-router'

// 只保存明确声明的展示状态；密钥、授权信息和编辑草稿不进入 URL。
export function useURLState<T extends object>(
  keys: readonly string[],
  parse: (query: LocationQuery) => T,
  serialize: (value: T) => LocationQueryRaw,
): Ref<T> {
  const route = useRoute()
  const router = useRouter()
  const path = route.path
  const state = ref(parse(route.query)) as Ref<T>
  watch(
    () => route.fullPath,
    () => {
      if (route.path !== path) return
      const next = parse(route.query)
      if (JSON.stringify(next) !== JSON.stringify(state.value)) state.value = next
    },
    { flush: 'sync' },
  )
  watch(
    state,
    async (value) => {
      if (route.path !== path || JSON.stringify(parse(route.query)) === JSON.stringify(value))
        return
      const query: LocationQueryRaw = { ...route.query }
      keys.forEach((key) => delete query[key])
      Object.assign(query, serialize(value))
      try {
        const failure = await router.replace({ path, query, hash: route.hash })
        if (isNavigationFailure(failure)) state.value = parse(route.query)
      } catch {
        state.value = parse(route.query)
      }
    },
    { deep: true },
  )
  return state
}
export function positivePage(value: unknown, fallback = 1): number {
  const number = typeof value === 'string' ? Number(value) : NaN
  return Number.isSafeInteger(number) && number > 0 ? number : fallback
}
