// 展示别名独立于原有凭据身份；这里仅负责呈现，不用于筛选标识或请求参数。
export function maskSubscriptionAccount(value: string): string {
  if (!value) return value
  const at = value.lastIndexOf('@')
  if (at > 0) {
    const local = Array.from(value.slice(0, at))
    const prefix = local.length > 1 ? local[0] : ''
    const suffix = local.length > 3 ? local.at(-1) : ''
    return `${prefix}***${suffix}${value.slice(at)}`
  }
  const chars = Array.from(value)
  return chars.length > 4 ? `${chars[0]}***${chars.at(-1)}` : '***'
}

export function credentialDisplayText(
  name: string | null | undefined,
  value: string,
  connectionType?: string,
): string {
  return (
    name?.trim() || (connectionType === 'subscription' ? maskSubscriptionAccount(value) : value)
  )
}
