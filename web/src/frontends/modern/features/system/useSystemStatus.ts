import { inject, onMounted, onScopeDispose, provide, readonly, ref, type InjectionKey } from 'vue'

import { getCurrentVersion, getReleaseUpdate, type ReleaseUpdate } from '@modern/api/system'
import { usePreferences } from '@modern/app/preferences'
import { createApiClient } from '@shared/http/client'
import { ApiError, RequestCancelledError } from '@shared/http/errors'

type CheckState = 'idle' | 'checking' | 'latest' | 'available' | 'failed' | 'authRequired'

function readAuthKey(): string {
  try {
    return window.localStorage.getItem('gpt-load.auth-key') ?? ''
  } catch {
    return ''
  }
}

function createSystemStatus() {
  const { locale } = usePreferences()
  const version = ref<string | null>(null)
  const versionLoading = ref(false)
  const checkState = ref<CheckState>('idle')
  const update = ref<ReleaseUpdate | null>(null)
  const controller = new AbortController()
  const client = createApiClient({
    fetch: window.fetch.bind(window),
    getAuthKey: readAuthKey,
    getLocale: () => locale.value,
    onUnauthorized: () => {
      update.value = null
    },
  })

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

  async function loadUpdate(force: boolean): Promise<void> {
    if (checkState.value === 'checking') return
    const authKey = readAuthKey()
    if (!authKey) {
      update.value = null
      checkState.value = force ? 'authRequired' : 'idle'
      return
    }
    checkState.value = 'checking'
    try {
      update.value = await getReleaseUpdate(client, authKey, force, controller.signal)
      checkState.value = update.value ? 'available' : force ? 'latest' : 'idle'
    } catch (error) {
      if (error instanceof RequestCancelledError || controller.signal.aborted) return
      update.value = null
      // 自动检查失败保持安静；手动检查给出结果，权限仍由现有管理接口校验。
      checkState.value = !force
        ? 'idle'
        : error instanceof ApiError && (error.status === 401 || error.status === 403)
          ? 'authRequired'
          : 'failed'
    }
  }

  function checkForUpdate(): void {
    void loadVersion()
    void loadUpdate(true)
  }

  onMounted(() => {
    void loadVersion()
    void loadUpdate(false)
  })
  onScopeDispose(() => controller.abort())

  return {
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
