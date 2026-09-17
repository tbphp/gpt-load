export type FrontendID = 'classic' | 'modern'

// v2 起仅接受管理员在设置页主动写入的偏好；旧版缓存不再参与启动判断。
const frontendStorageKey = 'gpt-load.frontend.v2'
const legacyFrontendStorageKey = 'gpt-load.frontend'
const authStorageKey = 'gpt-load.auth-key'
const frontendAuthTimeoutMS = 5000

function getStorage(type: 'localStorage' | 'sessionStorage'): Storage | undefined {
  try {
    return window[type]
  } catch {
    return undefined
  }
}

function readStorageKey(type: 'localStorage' | 'sessionStorage', key: string): string {
  try {
    return getStorage(type)?.getItem(key) ?? ''
  } catch {
    return ''
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
  return (
    readStorageKey('sessionStorage', authStorageKey) ||
    readStorageKey('localStorage', authStorageKey)
  )
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
  if (readStorageKey('localStorage', frontendStorageKey) !== 'classic') return 'modern'

  const credential = readAuthKey()
  if (!credential) return 'modern'

  const controller = new AbortController()
  let timeoutID: number | undefined
  try {
    const timeout = new Promise<undefined>((resolve) => {
      timeoutID = window.setTimeout(() => {
        resolve(undefined)
        controller.abort()
      }, frontendAuthTimeoutMS)
    })
    // 截止时间覆盖响应正文读取；迟到结果只返回身份，不再修改浏览器偏好。
    const principal = await Promise.race([
      window
        .fetch('/api/auth/session', {
          cache: 'no-store',
          headers: { Authorization: `Bearer ${credential}` },
          signal: controller.signal,
        })
        .then(async (response) => {
          if (response.status === 401) return 'unauthorized' as const
          if (!response.ok) return undefined
          return sessionPrincipal(await response.json())
        }),
      timeout,
    ])
    if (principal === 'admin') return 'classic'
    if (principal === 'unauthorized' || principal === 'access_key') {
      clearFrontendPreference()
    }
  } catch {
    // 临时网络、存储或响应异常保留管理员选择，由新版认证页提供恢复入口。
  } finally {
    if (timeoutID !== undefined) window.clearTimeout(timeoutID)
  }
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
