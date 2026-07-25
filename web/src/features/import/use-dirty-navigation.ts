import { inject, onBeforeUnmount, onMounted, type InjectionKey, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'

export interface DirtyNavigationController {
  bypassNext(): void
  consumeBypass(): boolean
}

export function createDirtyNavigationController(): DirtyNavigationController {
  let bypass = false
  return {
    bypassNext() {
      bypass = true
    },
    consumeBypass() {
      const result = bypass
      bypass = false
      return result
    },
  }
}

export const dirtyNavigationKey: InjectionKey<DirtyNavigationController> =
  Symbol('dirty-navigation')

export function useDirtyNavigation(dirty: Readonly<Ref<boolean>>): void {
  const controller = inject(dirtyNavigationKey)
  if (!controller) throw new Error('DIRTY_NAVIGATION_NOT_PROVIDED')
  const { t } = useI18n()

  const beforeUnload = (event: BeforeUnloadEvent | Event) => {
    if (!dirty.value) return
    event.preventDefault()
    if ('returnValue' in event) event.returnValue = ''
  }

  onMounted(() => window.addEventListener('beforeunload', beforeUnload))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
  onBeforeRouteLeave(() => {
    if (!dirty.value || controller.consumeBypass()) return true
    return window.confirm(t('import.unsavedConfirm'))
  })
}

export function useDirtyNavigationController(): DirtyNavigationController {
  const controller = inject(dirtyNavigationKey)
  if (!controller) throw new Error('DIRTY_NAVIGATION_NOT_PROVIDED')
  return controller
}
