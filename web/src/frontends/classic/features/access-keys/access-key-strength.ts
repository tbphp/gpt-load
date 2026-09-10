export type AccessKeyStrength = 'weak' | 'fair' | 'strong'

export function isValidCustomAccessKey(value: string): boolean {
  return value === '' || /^[\x21-\x7e]{1,256}$/.test(value)
}

// 仅用于前端提示，不作为允许创建密钥的强度门槛。
export function estimateAccessKeyStrength(value: string): AccessKeyStrength | null {
  if (value === '') return null
  const content = value.startsWith('sk-gl-') ? value.slice(6) : value
  const lower = content.toLowerCase()
  const sequential = [
    '012345678901234567890',
    'abcdefghijklmnopqrstuvwxyz',
    'qwertyuiopasdfghjklzxcvbnm',
  ].some((sequence) => sequence.includes(lower))
  if (
    content.length < 12 ||
    new Set(content).size < 4 ||
    /^(.{1,4})\1+$/.test(content) ||
    sequential
  ) {
    return 'weak'
  }
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^a-zA-Z0-9]/].filter((pattern) =>
    pattern.test(content),
  ).length
  return content.length >= 20 || (content.length >= 16 && kinds >= 3) ? 'strong' : 'fair'
}
