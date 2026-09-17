import {
  inject,
  onScopeDispose,
  provide,
  shallowRef,
  watch,
  type InjectionKey,
  type ShallowRef,
} from 'vue'
import { useRoute } from 'vue-router'
import type { AppMessage } from '../components/ui/message'

interface MessageService {
  current: ShallowRef<AppMessage | undefined>
  show: (value: Omit<AppMessage, 'id'>) => number
  close: (id?: number) => void
}
const key: InjectionKey<MessageService> = Symbol('modern-messages')
export function provideMessages(): MessageService {
  const current = shallowRef<AppMessage>()
  let nextID = 0
  const service = {
    current,
    show(value: Omit<AppMessage, 'id'>) {
      current.value = { ...value, id: ++nextID }
      return nextID
    },
    close(id?: number) {
      if (id === undefined || current.value?.id === id) current.value = undefined
    },
  }
  provide(key, service)
  const route = useRoute()
  watch(
    () => route.fullPath,
    () => service.close(),
    { flush: 'sync' },
  )
  return service
}
export function useMessages() {
  const service = inject(key)
  if (!service) throw new Error('MODERN_MESSAGES_NOT_PROVIDED')
  return service
}
export function useMessageSource(source: () => Omit<AppMessage, 'id'> | undefined) {
  const service = useMessages()
  let owned: number | undefined
  watch(
    source,
    (value) => {
      if (value?.text) owned = service.show(value)
      else if (owned !== undefined) service.close(owned)
    },
    { immediate: true },
  )
  onScopeDispose(() => {
    if (owned !== undefined) service.close(owned)
  })
}
