import { inject, onScopeDispose, provide, ref, type InjectionKey, type Ref } from 'vue'

export interface PolicyDraftItem {
  id: string
  valid: boolean
  pending: boolean
}

export interface PolicyDraftSummary {
  valid: boolean
  pending: boolean
  hasInvalid: boolean
}

export interface PolicyDraftContext {
  report(item: PolicyDraftItem): void
  remove(id: string): void
}

export const policyDraftContextKey: InjectionKey<PolicyDraftContext> = Symbol('policyDraftContext')

export function providePolicyDraftCollector(): {
  summary: Ref<PolicyDraftSummary>
} {
  const map = new Map<string, PolicyDraftItem>()
  let invalidCount = 0
  let pendingCount = 0
  const summary = ref<PolicyDraftSummary>({
    valid: true,
    pending: false,
    hasInvalid: false,
  })

  function updateSummary(): void {
    const hasInvalid = invalidCount > 0
    const pending = pendingCount > 0
    if (summary.value.hasInvalid === hasInvalid && summary.value.pending === pending) return
    summary.value = {
      valid: !hasInvalid,
      pending,
      hasInvalid,
    }
  }

  const context: PolicyDraftContext = {
    report(item: PolicyDraftItem): void {
      const previous = map.get(item.id)
      invalidCount += Number(!item.valid) - Number(previous !== undefined && !previous.valid)
      pendingCount += Number(item.pending) - Number(previous?.pending ?? false)
      map.set(item.id, item)
      updateSummary()
    },
    remove(id: string): void {
      const item = map.get(id)
      if (!item) return
      invalidCount -= Number(!item.valid)
      pendingCount -= Number(item.pending)
      map.delete(id)
      updateSummary()
    },
  }

  provide(policyDraftContextKey, context)
  return { summary }
}

let nextDraftId = 0
export function usePolicyDraftReporter(category = 'draft'): {
  report(valid: boolean, pending: boolean): void
  clear(): void
} {
  const context = inject(policyDraftContextKey, null)
  const id = `${category}-${++nextDraftId}`

  function report(valid: boolean, pending: boolean): void {
    if (!context) return
    context.report({ id, valid, pending })
  }

  function clear(): void {
    if (!context) return
    context.remove(id)
  }

  onScopeDispose(() => {
    clear()
  })

  return { report, clear }
}
