import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useApiClient } from './http/client-context'
import { ApiError, InvalidResponseError } from './http/errors'
import type { RedactionRule } from './request-redaction'

type PreviewSegment =
  | { kind: 'text'; text: string }
  | { kind: 'match'; text: string; rule_index: number; mode: 'encrypt' }
  | { kind: 'match'; text: string; rule_index: number; mode: 'replace'; replacement: string }

interface RedactionPreview {
  match_count: number
  segments: PreviewSegment[]
}

function readPreview(value: unknown, text: string, ruleCount: number): RedactionPreview {
  if (
    !value ||
    typeof value !== 'object' ||
    !('segments' in value) ||
    !Array.isArray(value.segments) ||
    !('match_count' in value) ||
    typeof value.match_count !== 'number' ||
    !Number.isSafeInteger(value.match_count)
  )
    throw new InvalidResponseError()
  const segments = value.segments.map((row: unknown): PreviewSegment => {
    if (
      !row ||
      typeof row !== 'object' ||
      !('kind' in row) ||
      !('text' in row) ||
      typeof row.text !== 'string'
    )
      throw new InvalidResponseError()
    if (row.kind === 'text') return { kind: 'text', text: row.text }
    if (
      row.kind !== 'match' ||
      !('rule_index' in row) ||
      typeof row.rule_index !== 'number' ||
      !Number.isSafeInteger(row.rule_index) ||
      row.rule_index < 0 ||
      row.rule_index >= ruleCount ||
      !('mode' in row)
    )
      throw new InvalidResponseError()
    const match = { kind: 'match' as const, text: row.text, rule_index: row.rule_index }
    if (row.mode === 'encrypt') return { ...match, mode: 'encrypt' }
    if (row.mode === 'replace' && 'replacement' in row && typeof row.replacement === 'string')
      return { ...match, mode: 'replace', replacement: row.replacement }
    throw new InvalidResponseError()
  })
  if (
    segments.map((part) => part.text).join('') !== text ||
    segments.filter((part) => part.kind === 'match').length !== value.match_count
  )
    throw new InvalidResponseError()
  return { match_count: value.match_count, segments }
}

export function useRedactionPreview(rules: () => readonly RedactionRule[]) {
  const client = useApiClient()
  const text = ref('')
  const result = shallowRef<RedactionPreview | null>(null)
  const pending = ref(false)
  const stale = ref(false)
  const error = ref('')
  let controller: AbortController | undefined
  let revision = 0

  function invalidate() {
    revision++
    controller?.abort()
    pending.value = false
    error.value = ''
    stale.value = result.value !== null
  }
  watch([text, rules], invalidate, { deep: true, flush: 'sync' })

  async function preview() {
    invalidate()
    // Unicode 模式按码点匹配，只拒绝未配对代理字符，保留合法的 emoji。
    if (/[\uD800-\uDFFF]/u.test(text.value)) {
      error.value = 'invalid_unicode'
      return
    }
    if (new TextEncoder().encode(text.value).length > 64 * 1024) {
      error.value = 'invalid_text'
      return
    }
    const current = new AbortController()
    controller = current
    const version = revision
    const sample = text.value
    const draft = rules().map((rule) => ({ ...rule }))
    pending.value = true
    try {
      const value = await client.request('/api/settings/request-redaction/preview', {
        method: 'POST',
        json: { rules: draft, text: sample },
        signal: current.signal,
      })
      if (current.signal.aborted || version !== revision) return
      result.value = readPreview(value, sample, draft.length)
      stale.value = false
    } catch (cause) {
      if (current.signal.aborted || version !== revision) return
      error.value = 'failed'
      if (cause instanceof ApiError && cause.code === 'VALIDATION_FAILED') {
        const data = cause.data
        error.value =
          data &&
          typeof data === 'object' &&
          'preview' in data &&
          (data.preview === 'invalid_text' || data.preview === 'too_many_matches')
            ? data.preview
            : 'invalid_rules'
      }
    } finally {
      if (version === revision) pending.value = false
    }
  }

  function clear() {
    invalidate()
    text.value = ''
    result.value = null
    stale.value = false
  }
  onScopeDispose(clear)
  return { text, result, pending, stale, error, preview, clear }
}
