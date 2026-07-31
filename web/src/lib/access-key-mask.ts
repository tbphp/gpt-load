const accessKeyMaskPrefix = 'sk-gl-****'
const accessKeySuffixPattern = /^[0-9a-f]{4}$/u

export function isCanonicalMaskedAccessKey(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.startsWith(accessKeyMaskPrefix) &&
    accessKeySuffixPattern.test(value.slice(accessKeyMaskPrefix.length))
  )
}

export function formatMaskedAccessKey(suffix: string): string {
  if (!accessKeySuffixPattern.test(suffix)) {
    throw new RangeError('Access key suffix must be four lowercase hexadecimal characters')
  }
  return `${accessKeyMaskPrefix}${suffix}`
}
