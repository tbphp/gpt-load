import { inject, readonly, ref, type InjectionKey, type Ref } from 'vue'

export type ToastTone = 'info' | 'success' | 'warning' | 'danger'

export interface ToastMessage {
  id: number
  message: string
  tone: ToastTone
}

export interface ToastInput {
  message: string
  tone?: ToastTone
  duration?: number
}

export interface ToastController {
  readonly current: Readonly<Ref<ToastMessage | null>>
  show(input: ToastInput): void
  dismiss(): void
  dispose(): void
}

interface ToastControllerDependencies {
  setTimer(callback: () => void, duration: number): number
  clearTimer(timer: number): void
}

export function createToastController(deps: ToastControllerDependencies): ToastController {
  const current = ref<ToastMessage | null>(null)
  let sequence = 0
  let timer: number | undefined

  function dismiss(): void {
    if (timer !== undefined) deps.clearTimer(timer)
    timer = undefined
    current.value = null
  }

  return {
    current: readonly(current),
    show(input) {
      if (timer !== undefined) deps.clearTimer(timer)
      const id = ++sequence
      current.value = {
        id,
        message: input.message,
        tone: input.tone ?? 'success',
      }
      timer = deps.setTimer(() => {
        if (current.value?.id === id) current.value = null
        timer = undefined
      }, input.duration ?? 2_000)
    },
    dismiss,
    dispose: dismiss,
  }
}

export const toastKey: InjectionKey<ToastController> = Symbol('toast')

export function useToast(): ToastController {
  const controller = inject(toastKey)
  if (!controller) throw new Error('TOAST_NOT_PROVIDED')
  return controller
}
