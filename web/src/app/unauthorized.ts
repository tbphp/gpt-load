import type { Router } from 'vue-router'

import type { AuthSession } from '@/features/auth/auth-session'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import type { DirtyNavigationController } from '@/features/import/use-dirty-navigation'

export interface GlobalUnauthorizedDependencies {
  recovery: Pick<ImportRecoveryService, 'captureForUnauthorized'>
  dirtyNavigation: Pick<DirtyNavigationController, 'bypassNext' | 'consumeBypass'>
  session: Pick<AuthSession, 'clear'>
  router: Pick<Router, 'replace'>
  redirect: string
}

export async function handleGlobalUnauthorized(
  deps: GlobalUnauthorizedDependencies,
): Promise<void> {
  deps.recovery.captureForUnauthorized()
  deps.dirtyNavigation.bypassNext()
  deps.session.clear()
  try {
    await deps.router.replace({
      name: 'login',
      query: { redirect: deps.redirect },
    })
  } finally {
    deps.dirtyNavigation.consumeBypass()
  }
}
