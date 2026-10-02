type OutputTiming = {
  protocol: string
  status_code: number
  duration_ms: number
  output_tokens: string
  usage_state: string
}

// 两套前端共用请求平均输出速度，包含等待、重试、凭据轮换及思考耗时。
export function outputTokensPerSecond(row: OutputTiming): number | null {
  const tokens = Number(row.output_tokens)
  if (
    row.status_code < 200 ||
    row.status_code >= 300 ||
    !['openai-completions', 'openai-responses', 'anthropic', 'gemini'].includes(row.protocol) ||
    (row.usage_state !== 'complete' && row.usage_state !== 'partial') ||
    !Number.isSafeInteger(tokens) ||
    tokens <= 0
  ) {
    return null
  }
  return Number.isSafeInteger(row.duration_ms) && row.duration_ms > 0
    ? tokens / (row.duration_ms / 1000)
    : null
}
