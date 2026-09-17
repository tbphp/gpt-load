import { inject, onScopeDispose, provide, type InjectionKey } from 'vue'

interface DraftGuard {
  pending: () => boolean
  confirm: () => boolean | Promise<boolean>
}
interface DraftGuards {
  guards: Set<DraftGuard>
  leaving: boolean
  run: (action: () => void) => Promise<boolean>
}
const key: InjectionKey<DraftGuards> = Symbol('modern-draft-guards')

// 重载应用前复用各个 AppDraftGuard 的确认，再放行 beforeunload。
export function provideDraftGuards(): void {
  const host: DraftGuards = {
    guards: new Set(),
    leaving: false,
    async run(action: () => void): Promise<boolean> {
      if (host.leaving || [...host.guards].some((guard) => guard.pending())) return false
      for (const guard of host.guards) {
        if (!(await guard.confirm())) return false
      }
      if ([...host.guards].some((guard) => guard.pending())) return false
      host.leaving = true
      try {
        action()
        return true
      } catch (error) {
        host.leaving = false
        throw error
      }
    },
  }
  provide(key, host)
}

export function useDraftGuards() {
  const host = inject(key)
  if (!host) throw new Error('MODERN_DRAFT_GUARDS_NOT_PROVIDED')
  return host
}

export function registerDraftGuard(guard: DraftGuard): () => boolean {
  const host = inject(key)
  host?.guards.add(guard)
  onScopeDispose(() => host?.guards.delete(guard))
  return () => host?.leaving ?? false
}
