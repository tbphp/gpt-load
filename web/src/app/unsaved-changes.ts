import { inject, onBeforeUnmount, onMounted, type InjectionKey, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'

export interface UnsavedChangesController {
  bypassNext(): void
  consumeBypass(): boolean
}

export interface UnsavedChangesOptions {
  blocked?: Readonly<Ref<boolean>>
}

export interface UnsavedChangesGuard {
  confirmDiscard(): boolean
  runWithoutPrompt<T>(navigate: () => Promise<T>): Promise<T>
}

export function createUnsavedChangesController(): UnsavedChangesController {
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

export const unsavedChangesKey: InjectionKey<UnsavedChangesController> = Symbol('unsaved-changes')

export function useUnsavedChanges(
  dirty: Readonly<Ref<boolean>>,
  options: UnsavedChangesOptions = {},
): UnsavedChangesGuard {
  const controller = useUnsavedChangesController()
  const { t } = useI18n()

  const beforeUnload = (event: BeforeUnloadEvent | Event) => {
    if (!dirty.value && !options.blocked?.value) return
    event.preventDefault()
    if ('returnValue' in event) event.returnValue = ''
  }

  onMounted(() => window.addEventListener('beforeunload', beforeUnload))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))

  function confirmDiscard(): boolean {
    if (options.blocked?.value) return false
    return !dirty.value || window.confirm(t('common.unsavedChanges'))
  }

  const confirmNavigation = () => {
    if (controller.consumeBypass()) return true
    return confirmDiscard()
  }

  onBeforeRouteLeave(confirmNavigation)
  onBeforeRouteUpdate(confirmNavigation)

  async function runWithoutPrompt<T>(navigate: () => Promise<T>): Promise<T> {
    controller.bypassNext()
    try {
      return await navigate()
    } finally {
      controller.consumeBypass()
    }
  }

  return { confirmDiscard, runWithoutPrompt }
}

export function useUnsavedChangesController(): UnsavedChangesController {
  const controller = inject(unsavedChangesKey)
  if (!controller) throw new Error('UNSAVED_CHANGES_NOT_PROVIDED')
  return controller
}
