export function isValidUpstreamBaseURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim())
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname !== '' &&
      parsed.username === '' &&
      parsed.password === '' &&
      parsed.search === '' &&
      !value.trim().endsWith('?') &&
      parsed.hash === ''
    )
  } catch {
    return false
  }
}

const versionPathSegmentPattern = /^v\d+[a-z]*$/i

function versionPathSegment(value: string): string | null {
  try {
    const parsed = new URL(value.trim())
    const segments = parsed.pathname.split('/').filter(Boolean)
    for (let index = segments.length - 1; index >= 0; index -= 1) {
      const segment = segments[index]
      if (versionPathSegmentPattern.test(segment)) return segment.toLowerCase()
    }
  } catch {
    return null
  }
  return null
}

/**
 * Compares only the version-like path shape of two valid upstream URLs.
 * A missing version and a different version are both advisory mismatches.
 */
export function hasUpstreamBaseURLVersionMismatch(reference: string, value: string): boolean {
  if (!isValidUpstreamBaseURL(reference) || !isValidUpstreamBaseURL(value)) return false
  const referenceVersion = versionPathSegment(reference)
  const valueVersion = versionPathSegment(value)
  return referenceVersion !== valueVersion && (referenceVersion !== null || valueVersion !== null)
}
