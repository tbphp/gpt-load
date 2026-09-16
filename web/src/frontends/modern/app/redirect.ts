import type { Router } from 'vue-router'
import { pagePath } from './navigation'

export function loginLocation(redirect?: string) {
  return { name: 'modern-login', query: redirect ? { redirect } : {} }
}

export function safeRedirect(raw: unknown, router: Router): string {
  const fallback = pagePath('home')
  if (
    typeof raw !== 'string' ||
    !raw.startsWith('/') ||
    raw.startsWith('//') ||
    raw.includes('\\')
  ) {
    return fallback
  }
  let decoded: string
  try {
    decoded = decodeURIComponent(raw)
  } catch {
    return fallback
  }
  if (
    decoded.startsWith('//') ||
    decoded.includes('\\') ||
    /[\u0000-\u001f\u007f]/u.test(decoded)
  ) {
    return fallback
  }
  const resolved = router.resolve(raw)
  const matchedPath = resolved.matched.at(-1)?.path
  let segments: string[]
  try {
    segments = decodeURIComponent(resolved.path).split('/')
  } catch {
    return fallback
  }
  const pattern = matchedPath?.split('/') ?? []
  if (
    !resolved.matched.length ||
    segments.length !== pattern.length ||
    !pattern.every((segment, index) =>
      segment.startsWith(':') ? segments[index] !== '' : segment === segments[index],
    ) ||
    resolved.name === 'modern-login' ||
    resolved.name === 'modern-unavailable' ||
    resolved.meta.requiresAuth !== true
  )
    return fallback
  return resolved.fullPath
}
