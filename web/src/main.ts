import { getPreferredFrontend, switchFrontend } from '@shared/frontend/preference'
import { getBrowserLocale } from '@shared/preferences/locale'

async function bootstrap(): Promise<void> {
  const frontend = getPreferredFrontend()
  document.documentElement.dataset.frontend = frontend
  const module =
    frontend === 'classic'
      ? await import('./frontends/classic/bootstrap')
      : await import('./frontends/modern/bootstrap')
  await module.bootstrap()
}

function showStartupFailure(): void {
  const labels = {
    'zh-CN': {
      message: '无法加载界面，请重试或切换至经典版。',
      retry: '重新加载',
      classic: '使用经典版',
      storage: '无法保存界面偏好，请允许本站使用浏览器存储后重试。',
    },
    'en-US': {
      message: 'Unable to load the interface. Retry or switch to Classic.',
      retry: 'Reload',
      classic: 'Use Classic',
      storage: 'Unable to save your preference. Allow browser storage for this site and retry.',
    },
    'ja-JP': {
      message: '画面を読み込めません。再試行するかクラシック版に切り替えてください。',
      retry: '再読み込み',
      classic: 'クラシック版を使用',
      storage: '設定を保存できません。このサイトのブラウザストレージを許可してください。',
    },
  }[getBrowserLocale()]
  const root = document.getElementById('app')
  if (!root) return
  const message = document.createElement('p')
  message.setAttribute('role', 'alert')
  message.textContent = labels.message
  const retry = document.createElement('button')
  retry.type = 'button'
  retry.textContent = labels.retry
  retry.addEventListener('click', () => window.location.reload())
  const classic = document.createElement('button')
  classic.type = 'button'
  classic.textContent = labels.classic
  classic.addEventListener('click', () => {
    try {
      switchFrontend('classic')
    } catch {
      message.textContent = labels.storage
    }
  })
  root.replaceChildren(message, retry, classic)
}

void bootstrap().catch(showStartupFailure)
