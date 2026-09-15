export type FrontendID = 'classic' | 'modern'

// v2 起仅接受管理员在设置页主动写入的偏好；旧版缓存不再参与启动判断。
const frontendStorageKey = 'gpt-load.frontend.v2'
const legacyFrontendStorageKey = 'gpt-load.frontend'
const authStorageKey = 'gpt-load.auth-key'

function getStorage(type: 'localStorage' | 'sessionStorage'): Storage | undefined {
  try {
    return window[type]
  } catch {
    return undefined
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function sessionPrincipal(response: unknown): 'admin' | 'access_key' | undefined {
  if (!isRecord(response) || response.code !== 0 || !isRecord(response.data)) return undefined
  const { authenticated, principal_type: principalType } = response.data
  if (authenticated !== true) return undefined
  return principalType === 'admin' || principalType === 'access_key' ? principalType : undefined
}

function readAuthKey(): string {
  try {
    return (
      getStorage('sessionStorage')?.getItem(authStorageKey) ??
      getStorage('localStorage')?.getItem(authStorageKey) ??
      ''
    )
  } catch {
    return ''
  }
}

function removePreference(key: string): void {
  try {
    getStorage('localStorage')?.removeItem(key)
  } catch {
    // 存储不可用时，默认新版入口仍然生效。
  }
}

export function clearFrontendPreference(): void {
  removePreference(frontendStorageKey)
  removePreference(legacyFrontendStorageKey)
}

export async function getPreferredFrontend(): Promise<FrontendID> {
  // 旧版的全局缓存没有认证上下文，必须直接失效，避免访问密钥进入经典版。
  removePreference(legacyFrontendStorageKey)
  if (getStorage('localStorage')?.getItem(frontendStorageKey) !== 'classic') return 'modern'

  const credential = readAuthKey()
  if (!credential) return 'modern'

  try {
    const response = await window.fetch('/api/auth/session', {
      cache: 'no-store',
      headers: { Authorization: `Bearer ${credential}` },
    })
    if (!response.ok) {
      clearFrontendPreference()
      return 'modern'
    }
    const principal = sessionPrincipal(await response.json())
    if (principal === 'admin') return 'classic'
  } catch {
    // 认证状态暂时不可用时，保守回到新版，由新版认证页呈现具体错误。
  }
  clearFrontendPreference()
  return 'modern'
}

export function switchFrontend(frontend: FrontendID): void {
  const storage = getStorage('localStorage')
  if (!storage) {
    throw new Error('FRONTEND_PREFERENCE_NOT_SAVED')
  }
  storage.removeItem(legacyFrontendStorageKey)
  if (frontend === 'classic') storage.setItem(frontendStorageKey, frontend)
  else storage.removeItem(frontendStorageKey)
  const saved = storage.getItem(frontendStorageKey)
  if ((frontend === 'classic' && saved !== frontend) || (frontend === 'modern' && saved !== null)) {
    throw new Error('FRONTEND_PREFERENCE_NOT_SAVED')
  }
  window.location.assign('/settings')
}
