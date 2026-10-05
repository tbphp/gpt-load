import { getPreferredFrontend } from '@shared/frontend/preference'

const startupRecoveryKey = 'gpt-load.startup-recovery'

async function bootstrap(): Promise<void> {
  const frontend = await getPreferredFrontend()
  document.documentElement.dataset.frontend = frontend
  const module =
    frontend === 'classic'
      ? await import('./frontends/classic/bootstrap')
      : await import('./frontends/modern/bootstrap')
  await module.bootstrap()
  try {
    window.sessionStorage.removeItem(startupRecoveryKey)
  } catch {
    // 恢复标记不可用时，不影响已启动的界面。
  }
}

function recoverStartup(error: unknown): void {
  console.error('Failed to start the interface.', error)
  try {
    // 标记跨导航保留；只有成功启动才清除，避免资源持续不可用时循环刷新。
    if (window.sessionStorage.getItem(startupRecoveryKey) === '1') return
    window.sessionStorage.setItem(startupRecoveryKey, '1')
  } catch {
    // 无法保存标记时，只从其他页面跳到主页，主页失败不再刷新。
    if (window.location.pathname === '/') return
  }
  window.location.replace('/')
}

void bootstrap().catch(recoverStartup)
