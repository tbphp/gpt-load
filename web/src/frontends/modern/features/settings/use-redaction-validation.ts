import { computed, onScopeDispose, ref, watch } from 'vue'
import { useApiClient } from '@shared/http/client-context'
import {
  validateRedaction,
  type RedactionRule,
  type RedactionIssue,
} from '@modern/api/request-redaction'

export function useRedactionValidation(rules: () => RedactionRule[]) {
  const client = useApiClient()
  const issues = ref<RedactionIssue[]>([])
  const checking = ref(false)
  const failed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined
  watch(
    rules,
    (value) => {
      clearTimeout(timer)
      controller?.abort()
      const current = new AbortController()
      controller = current
      issues.value = []
      failed.value = false
      checking.value = value.length > 0
      if (!value.length) return
      const copy = value.map((rule) => ({ ...rule }))
      timer = setTimeout(async () => {
        try {
          const result = await validateRedaction(client, copy, current.signal)
          if (!current.signal.aborted) issues.value = result
        } catch {
          if (!current.signal.aborted) failed.value = true
        } finally {
          if (!current.signal.aborted) checking.value = false
        }
      }, 300)
    },
    { deep: true, immediate: true },
  )
  onScopeDispose(() => {
    clearTimeout(timer)
    controller?.abort()
  })
  return {
    issues,
    checking,
    failed,
    invalid: computed(
      () => checking.value || failed.value || issues.value.some((issue) => Boolean(issue.error)),
    ),
  }
}
