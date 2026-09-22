import {
  computed,
  inject,
  onMounted,
  onScopeDispose,
  provide,
  readonly,
  ref,
  type InjectionKey,
} from 'vue'

import { getCurrentVersion, getReleaseUpdate, type ReleaseUpdate } from '@modern/api/system'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { useApiClient } from '@shared/http/client-context'
import { ApiError, RequestCancelledError } from '@shared/http/errors'

type CheckState = 'idle' | 'checking' | 'latest' | 'available' | 'failed' | 'authRequired'

function isDevelopmentVersion(value: string): boolean {
  return /^v?\d+\.\d+\.\d+-dev(?:\.|$)/.test(value)
}

function createSystemStatus() {
  const session = useAuthSession()
  const client = useApiClient()
  const version = ref<string | null>(null)
  const versionLoading = ref(false)
  const checkState = ref<CheckState>('idle')
  const update = ref<ReleaseUpdate | null>(null)
  const controller = new AbortController()
  const canCheckUpdate = computed(
    () =>
      session.state.phase === 'validated' &&
      session.state.principalType === 'admin' &&
      version.value !== null &&
      !isDevelopmentVersion(version.value),
  )

  async function loadVersion(): Promise<void> {
    if (versionLoading.value) return
    versionLoading.value = true
    try {
      version.value = await getCurrentVersion(controller.signal)
    } catch (error) {
      if (error instanceof RequestCancelledError) return
      version.value = null
    } finally {
      versionLoading.value = false
    }
  }

  async function loadUpdate(force: boolean): Promise<CheckState | undefined> {
    if (checkState.value === 'checking') return
    if (!canCheckUpdate.value) {
      update.value = null
      checkState.value = 'authRequired'
      return checkState.value
    }
    checkState.value = 'checking'
    try {
      update.value = await getReleaseUpdate(client, force, controller.signal)
      checkState.value = update.value ? 'available' : 'latest'
    } catch (error) {
      if (error instanceof RequestCancelledError || controller.signal.aborted) return
      checkState.value =
        error instanceof ApiError && (error.status === 401 || error.status === 403)
          ? 'authRequired'
          : 'failed'
    }
    return checkState.value
  }

  function checkForUpdate(): Promise<CheckState | undefined> {
    return loadUpdate(true)
  }

  onMounted(() => {
    void loadVersion().then(() => {
      if (canCheckUpdate.value) void loadUpdate(false)
    })
  })
  onScopeDispose(() => controller.abort())

  return {
    canCheckUpdate,
    version: readonly(version),
    versionLoading: readonly(versionLoading),
    checkState: readonly(checkState),
    update: readonly(update),
    checkForUpdate,
  }
}

const systemStatusKey: InjectionKey<ReturnType<typeof createSystemStatus>> =
  Symbol('modern-system-status')

export function provideSystemStatus(): void {
  provide(systemStatusKey, createSystemStatus())
}

export function useSystemStatus() {
  const status = inject(systemStatusKey)
  if (!status) throw new Error('MODERN_SYSTEM_STATUS_NOT_PROVIDED')
  return status
}
