import { inject, onScopeDispose, provide, ref, watchEffect, type InjectionKey, type Ref } from 'vue'

export interface PolicyDraftItem {
  id: string
  valid: boolean
  pending: boolean
}

export interface PolicyDraftSummary {
  valid: boolean
  pending: boolean
}

export interface PolicyDraftContext {
  report(item: PolicyDraftItem): void
  remove(id: string): void
}

export const policyDraftContextKey: InjectionKey<PolicyDraftContext> = Symbol('policyDraftContext')

export function providePolicyDraftCollector(): {
  summary: Ref<PolicyDraftSummary>
} {
  const items = new Map<string, PolicyDraftItem>()
  const summary = ref<PolicyDraftSummary>({
    valid: true,
    pending: false,
  })

  // 仅在聚合结果真正变化时替换 ref，避免无谓触发上层 watch。
  function updateSummary(): void {
    let valid = true
    let pending = false
    for (const item of items.values()) {
      if (!item.valid) valid = false
      if (item.pending) pending = true
    }
    if (summary.value.valid === valid && summary.value.pending === pending) return
    summary.value = { valid, pending }
  }

  const context: PolicyDraftContext = {
    report(item: PolicyDraftItem): void {
      items.set(item.id, item)
      updateSummary()
    },
    remove(id: string): void {
      if (!items.delete(id)) return
      updateSummary()
    },
  }

  provide(policyDraftContextKey, context)
  return { summary }
}

// PolicyDraftStatus 是控件草稿在 collector 中的可见状态。
// active=false 表示该控件当前不参与校验（禁用、JSON-only、无额度窗口等），上报中性结果。
export interface PolicyDraftStatus {
  active: boolean
  valid: boolean
  pending: boolean
}

let nextDraftId = 0

// usePolicyDraftStatus 只共用“上报协议”：getStatus 读取纯状态，写入 AST 的同步策略仍留在各控件。
export function usePolicyDraftStatus(category: string, getStatus: () => PolicyDraftStatus): void {
  const context = inject(policyDraftContextKey, null)
  const id = `${category}-${++nextDraftId}`

  watchEffect(() => {
    if (!context) return
    const status = getStatus()
    context.report({
      id,
      valid: !status.active || status.valid,
      pending: status.active && status.pending,
    })
  })

  onScopeDispose(() => {
    context?.remove(id)
  })
}
