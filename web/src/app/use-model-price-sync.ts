import { useQueryClient } from '@tanstack/vue-query'
import { onBeforeUnmount, ref } from 'vue'

import { useApiClient } from '@/api/client-context'
import { RequestCancelledError } from '@/api/errors'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { syncModelPrices } from '@/app/resources/providers'
import { useTransientFlag } from '@/app/use-transient-flag'

export function useModelPriceSync() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  const pending = ref(false)
  const failed = ref(false)
  const { value: succeeded, show: showSucceeded } = useTransientFlag(2_000)
  let controller: AbortController | undefined
  let mounted = true

  async function run(): Promise<void> {
    if (pending.value) return
    pending.value = true
    failed.value = false
    controller?.abort()
    controller = new AbortController()
    const activeController = controller
    try {
      await syncModelPrices(client, activeController.signal)
      if (!mounted || controller !== activeController) return
      await applyInvalidationPlan(
        queryClient,
        mutationInvalidationPlans.modelPrice.sync,
        () => mounted && controller === activeController,
      )
      if (!mounted || controller !== activeController) return
      showSucceeded()
    } catch (error: unknown) {
      if (
        mounted &&
        controller === activeController &&
        !activeController.signal.aborted &&
        !(error instanceof RequestCancelledError)
      ) {
        failed.value = true
      }
    } finally {
      if (controller === activeController) {
        controller = undefined
        pending.value = false
      }
    }
  }

  onBeforeUnmount(() => {
    mounted = false
    controller?.abort()
    controller = undefined
  })

  return { pending, failed, succeeded, run }
}
