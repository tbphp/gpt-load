import { getPreferredFrontend } from '@shared/frontend/preference'
import { getBrowserLocale } from '@shared/preferences/locale'
import './startup-recovery.css'

const startupRecoveryKey = 'gpt-load.startup-recovery'

async function bootstrap(): Promise<void> {
  const frontend = await getPreferredFrontend()
  document.documentElement.dataset.frontend = frontend
  const module =
    frontend === 'classic'
      ? await import('./frontends/classic/bootstrap')
      : await import('./frontends/modern/bootstrap')
  await module.bootstrap()
  clearStartupRecovery()
}

function clearStartupRecovery(): void {
  try {
    window.sessionStorage.removeItem(startupRecoveryKey)
  } catch {
    // 存储不可用时，主页路径仍会限制自动恢复次数。
  }
}

function showStartupRecovery(): void {
  const locale = getBrowserLocale()
  const labels = {
    'zh-CN': {
      message: '页面暂时未能打开。',
      retry: '重新打开主页',
    },
    'en-US': {
      message: 'The page is temporarily unavailable.',
      retry: 'Reload home',
    },
    'ja-JP': {
      message: '現在ページを開けません。',
      retry: 'ホームを再読み込み',
    },
  }[locale]
  const root = document.getElementById('app')
  if (!root) return
  document.documentElement.lang = locale
  const main = document.createElement('main')
  main.className = 'startup-recovery'
  const card = document.createElement('section')
  card.className = 'startup-recovery-card'
  const title = document.createElement('h1')
  title.textContent = 'GPT-Load'
  const message = document.createElement('p')
  message.setAttribute('role', 'alert')
  message.textContent = labels.message
  const retry = document.createElement('button')
  retry.type = 'button'
  retry.textContent = labels.retry
  retry.addEventListener('click', () => {
    clearStartupRecovery()
    window.location.replace('/')
  })
  card.append(title, message, retry)
  main.append(card)
  root.replaceChildren(main)
  retry.focus()
}

function recoverStartup(error: unknown): void {
  console.error('Failed to start the interface.', error)
  let shouldReload: boolean
  try {
    // 标记跨导航保留，避免资源持续不可用时循环刷新。
    shouldReload = window.sessionStorage.getItem(startupRecoveryKey) !== '1'
    if (shouldReload) window.sessionStorage.setItem(startupRecoveryKey, '1')
  } catch {
    // 无法保存标记时，只从其他页面跳到主页，主页失败不再刷新。
    shouldReload = window.location.pathname !== '/'
  }
  if (shouldReload) window.location.replace('/')
  else showStartupRecovery()
}

void bootstrap().catch(recoverStartup)
