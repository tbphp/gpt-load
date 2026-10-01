type OutputTiming = {
  stream: boolean
  first_output_ms: number | null
  last_output_ms: number | null
  output_tokens: string
  usage_state: string
}

// 两套前端共用同一口径；用量包含隐藏思考时仍为估算，不另建 token 计数。
export function outputTokensPerSecond(row: OutputTiming): number | null {
  const first = row.first_output_ms
  const last = row.last_output_ms
  const tokens = Number(row.output_tokens)
  if (
    !row.stream ||
    (row.usage_state !== 'complete' && row.usage_state !== 'partial') ||
    first == null ||
    last == null ||
    !Number.isSafeInteger(first) ||
    !Number.isSafeInteger(last) ||
    first < 0 ||
    last <= first ||
    !Number.isSafeInteger(tokens) ||
    tokens <= 1
  ) {
    return null
  }
  const rate = (tokens - 1) / ((last - first) / 1000)
  return Number.isFinite(rate) ? rate : null
}
