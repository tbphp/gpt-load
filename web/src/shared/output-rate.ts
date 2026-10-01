type OutputTiming = {
  protocol: string
  stream: boolean
  duration_ms: number
  first_output_ms: number | null
  last_output_ms: number | null
  output_tokens: string
  usage_state: string
}

// 两套前端共用同一口径；用量包含隐藏思考时仍为估算，不另建 token 计数。
export function outputTokensPerSecond(row: OutputTiming): number | null {
  const tokens = Number(row.output_tokens)
  if (
    !['openai-completions', 'openai-responses', 'anthropic', 'gemini'].includes(row.protocol) ||
    (row.usage_state !== 'complete' && row.usage_state !== 'partial') ||
    !Number.isSafeInteger(tokens) ||
    tokens <= 0
  ) {
    return null
  }
  if (!row.stream) {
    // 非流式没有生成区间，使用包含等待时间的请求平均速度。
    return Number.isSafeInteger(row.duration_ms) && row.duration_ms > 0
      ? tokens / (row.duration_ms / 1000)
      : null
  }
  const first = row.first_output_ms
  const last = row.last_output_ms
  if (
    first == null ||
    last == null ||
    !Number.isSafeInteger(first) ||
    !Number.isSafeInteger(last) ||
    first < 0 ||
    last <= first ||
    tokens <= 1
  ) {
    return null
  }
  const rate = (tokens - 1) / ((last - first) / 1000)
  return Number.isFinite(rate) ? rate : null
}
