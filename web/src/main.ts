import { getPreferredFrontend } from '@shared/frontend/preference'
import { getBrowserLocale } from '@shared/preferences/locale'

async function bootstrap(): Promise<void> {
  const frontend = await getPreferredFrontend()
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
      message: '无法加载界面，请重试。',
      retry: '重新加载',
    },
    'en-US': {
      message: 'Unable to load the interface. Please retry.',
      retry: 'Reload',
    },
    'ja-JP': {
      message: '画面を読み込めません。再試行してください。',
      retry: '再読み込み',
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
  root.replaceChildren(message, retry)
}

void bootstrap().catch(showStartupFailure)
