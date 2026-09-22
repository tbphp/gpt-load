import { computed, onScopeDispose, ref, watch } from 'vue'
import { useApiClient } from '@shared/http/client-context'
import {
  validateRedaction,
  type RedactionRule,
  type RedactionIssue,
} from '@modern/api/request-redaction'

function localIssues(rules: RedactionRule[]): RedactionIssue[] {
  if (rules.length > 64) return [{ index: -1, error: 'too_many_rules' }]
  const issues: RedactionIssue[] = []
  const encoder = new TextEncoder()
  rules.forEach((rule, index) => {
    if (!rule.pattern) issues.push({ index, error: 'empty_pattern' })
    else if (
      encoder.encode(rule.pattern).length > 4096 ||
      encoder.encode(rule.replacement).length > 4096
    )
      issues.push({ index, error: 'invalid_length' })
  })
  return issues
}

export function useRedactionValidation(rules: () => RedactionRule[]) {
  const client = useApiClient()
  const serverIssues = ref<RedactionIssue[]>([])
  const validatedRules = ref<RedactionRule[]>([])
  const checking = ref(false)
  const failed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined

  const issues = computed(() => {
    const current = rules()
    const local = localIssues(current)
    const unchanged = (index: number) =>
      validatedRules.value[index]?.pattern === current[index]?.pattern &&
      validatedRules.value[index]?.replacement === current[index]?.replacement
    const remote = serverIssues.value.filter((issue) =>
      issue.index < 0
        ? validatedRules.value.length === current.length &&
          current.every((_, index) => unchanged(index))
        : unchanged(issue.index),
    )
    return [...local, ...remote]
  })

  watch(
    rules,
    (value) => {
      clearTimeout(timer)
      controller?.abort()
      failed.value = false
      const copy = value.map((rule) => ({ ...rule }))
      checking.value = copy.length > 0 && localIssues(copy).length === 0
      if (!checking.value) return
      const current = new AbortController()
      controller = current
      timer = setTimeout(async () => {
        try {
          const result = await validateRedaction(client, copy, current.signal)
          if (!current.signal.aborted) {
            validatedRules.value = copy
            serverIssues.value = result
          }
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
