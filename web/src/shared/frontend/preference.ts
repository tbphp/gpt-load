export type FrontendID = 'classic' | 'modern'

const frontendStorageKey = 'gpt-load.frontend'

export function getPreferredFrontend(): FrontendID {
  try {
    if (window.localStorage.getItem(frontendStorageKey) === 'classic') return 'classic'
  } catch {
    // 偏好不可用时仍能进入默认界面。
  }
  return 'modern'
}

export function switchFrontend(frontend: FrontendID): void {
  // 先确认偏好成功保存，避免刷新后仍回到原界面。
  window.localStorage.setItem(frontendStorageKey, frontend)
  if (window.localStorage.getItem(frontendStorageKey) !== frontend) {
    throw new Error('FRONTEND_PREFERENCE_NOT_SAVED')
  }
  window.location.assign('/settings')
}
