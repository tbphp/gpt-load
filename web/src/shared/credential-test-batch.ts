import { computed, onScopeDispose, ref, shallowRef } from 'vue'

export interface CredentialTestOptions {
  protocol: string
  model: string
}

export interface CredentialBatchTestResult {
  outcome: 'passed' | 'failed' | 'inconclusive'
  latency: number
  reason: string | null
}

export interface CredentialBatchTestItem {
  id: number
  state: 'queued' | 'running' | 'cancelled' | CredentialBatchTestResult['outcome']
  latency?: number
  reason?: string | null
}

type Probe = (
  id: number,
  options: CredentialTestOptions,
  signal: AbortSignal,
) => Promise<CredentialBatchTestResult>

// 只保存当前页面的一轮测试；停止或重新开始后，旧请求不能再写回结果。
export function useCredentialTestBatch() {
  const report = shallowRef<{
    options: CredentialTestOptions
    items: CredentialBatchTestItem[]
  }>()
  const pending = ref(false)
  let controller: AbortController | undefined
  const itemsByID = computed(() => new Map(report.value?.items.map((item) => [item.id, item])))
  const counts = computed(() => {
    const result = { total: 0, completed: 0, passed: 0, failed: 0, inconclusive: 0, cancelled: 0 }
    for (const item of report.value?.items ?? []) {
      result.total++
      if (item.state === 'queued' || item.state === 'running') continue
      result[item.state]++
      if (item.state !== 'cancelled') result.completed++
    }
    return result
  })
  const retryIDs = computed(() =>
    (report.value?.items ?? [])
      .filter((item) => ['failed', 'inconclusive', 'cancelled'].includes(item.state))
      .map((item) => item.id),
  )

  function stop(): void {
    if (!controller) return
    const previous = controller
    controller = undefined
    previous.abort()
    pending.value = false
    if (report.value) {
      report.value = {
        ...report.value,
        items: report.value.items.map((item) =>
          item.state === 'queued' || item.state === 'running'
            ? { ...item, state: 'cancelled' }
            : item,
        ),
      }
    }
  }

  function reset(): void {
    stop()
    report.value = undefined
  }

  async function run(
    ids: number[],
    options: CredentialTestOptions,
    probe: Probe,
    retry = false,
  ): Promise<void> {
    if (pending.value || !ids.length) return
    const current = new AbortController()
    controller = current
    pending.value = true
    const targets = new Set(ids)
    const batch = {
      options: { ...options },
      items:
        retry && report.value
          ? report.value.items.map((item): CredentialBatchTestItem =>
              targets.has(item.id) ? { id: item.id, state: 'queued' } : item,
            )
          : [...targets].map((id): CredentialBatchTestItem => ({ id, state: 'queued' })),
    }
    // 重测只重新调度目标项，其他已返回结果继续属于同一份报告。
    const queue = batch.items.flatMap((item, index) => (item.state === 'queued' ? [index] : []))
    report.value = { ...batch, items: [...batch.items] }
    let cursor = 0
    function update(index: number, item: CredentialBatchTestItem): void {
      if (controller !== current) return
      batch.items[index] = item
      report.value = { ...batch, items: [...batch.items] }
    }
    async function worker(): Promise<void> {
      while (cursor < queue.length && controller === current) {
        const index = queue[cursor++]!
        const { id } = batch.items[index]!
        update(index, { id, state: 'running' })
        try {
          const result = await probe(id, batch.options, current.signal)
          update(index, {
            id,
            state: result.outcome,
            latency: result.latency,
            reason: result.reason,
          })
        } catch {
          // 请求失败不代表密钥无效；取消由 stop 统一标记。
          update(index, { id, state: 'inconclusive', reason: 'request_failed' })
        }
      }
    }
    try {
      await Promise.all(Array.from({ length: Math.min(10, queue.length) }, worker))
    } finally {
      if (controller === current) {
        controller = undefined
        pending.value = false
      }
    }
  }

  onScopeDispose(stop)
  return { report, pending, itemsByID, counts, retryIDs, run, stop, reset }
}
