import type { Router } from 'vue-router'

import { loginLocation } from '@/app/route-locations'
import type { AuthSession } from '@/features/auth/auth-session'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import type { UnsavedChangesController } from '@/app/unsaved-changes'

export interface GlobalUnauthorizedDependencies {
  recovery: Pick<ImportRecoveryService, 'captureForUnauthorized'>
  unsavedChanges: Pick<UnsavedChangesController, 'bypassNext' | 'consumeBypass'>
  session: Pick<AuthSession, 'clear' | 'hasCredential'>
  router: Pick<Router, 'replace'>
  redirect: string
}

export async function handleGlobalUnauthorized(
  deps: GlobalUnauthorizedDependencies,
): Promise<void> {
  if (!deps.session.hasCredential()) return
  deps.recovery.captureForUnauthorized()
  deps.unsavedChanges.bypassNext()
  deps.session.clear()
  try {
    await deps.router.replace(loginLocation(deps.redirect))
  } finally {
    deps.unsavedChanges.consumeBypass()
  }
}
