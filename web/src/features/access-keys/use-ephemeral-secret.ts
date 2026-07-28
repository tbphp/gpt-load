import { onBeforeUnmount, readonly, ref, type Ref } from 'vue'

export const ephemeralSecretTtlMs = 60_000

export interface EphemeralSecretController {
  epoch: Readonly<Ref<number>>
  owner: Readonly<Ref<string | null>>
  secret: Readonly<Ref<string | null>>
  expose(owner: string, secret: string): void
  read(owner: string): string | null
  clear(): void
  dispose(): void
}

export function createEphemeralSecretController(): EphemeralSecretController {
  const owner = ref<string | null>(null)
  const secret = ref<string | null>(null)
  const epoch = ref(0)
  let expiryTimer: ReturnType<typeof setTimeout> | undefined

  function cancelTimer(): void {
    if (expiryTimer !== undefined) {
      clearTimeout(expiryTimer)
      expiryTimer = undefined
    }
  }

  function clear(): void {
    cancelTimer()
    epoch.value += 1
    owner.value = null
    secret.value = null
  }

  function expose(nextOwner: string, nextSecret: string): void {
    clear()
    if (!nextOwner || !nextSecret) return
    owner.value = nextOwner
    secret.value = nextSecret
    expiryTimer = setTimeout(clear, ephemeralSecretTtlMs)
  }

  return {
    epoch: readonly(epoch),
    owner: readonly(owner),
    secret: readonly(secret),
    expose,
    read(candidateOwner) {
      return owner.value === candidateOwner ? secret.value : null
    },
    clear,
    dispose: clear,
  }
}

export function useEphemeralSecret(): EphemeralSecretController {
  const controller = createEphemeralSecretController()
  const handleVisibilityChange = () => {
    if (document.visibilityState === 'hidden') controller.clear()
  }
  document.addEventListener('visibilitychange', handleVisibilityChange)
  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    controller.dispose()
  })
  return controller
}
