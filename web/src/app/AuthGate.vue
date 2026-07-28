<script setup lang="ts">
import { nextTick, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useImportRecovery } from '@/features/import/import-recovery'
import { useCountdown } from '@/features/auth/use-countdown'

const session = useAuthSession()
const recovery = useImportRecovery()
const router = useRouter()
const { t } = useI18n()
const countdown = useCountdown(toRef(session.state, 'retryAfterSeconds'))
const gateRoot = ref<HTMLElement | null>(null)

if (session.state.phase !== 'validated') {
  void session.ensureValidated().catch(() => {})
}

watch(
  () => session.state.phase,
  async (phase) => {
    if (phase !== 'invalid-response') return

    await nextTick()
    if (session.state.phase !== 'invalid-response') return

    const retry = gateRoot.value?.querySelector<HTMLButtonElement>(
      'button[data-test="invalid-response-retry"]',
    )
    if (retry instanceof HTMLButtonElement && retry.isConnected) {
      retry.focus()
    }
  },
  { immediate: true },
)

async function retryValidation(): Promise<void> {
  try {
    await session.retryValidation()
  } catch {
    // The session state machine maps validation failures to a renderable phase.
  }
}

function changeAuthKey(): void {
  recovery.clear()
  session.clear()
  void router.replace({ name: 'login' })
}
</script>

<template>
  <slot v-if="session.state.phase === 'validated'" />

  <main v-else ref="gateRoot" class="auth-gate-shell">
    <SurfaceCard class="auth-gate-card" aria-labelledby="auth-gate-title">
      <h1 id="auth-gate-title" class="auth-gate-title">{{ t('common.appName') }}</h1>

      <InlineFeedback v-if="session.state.phase === 'validating'" tone="info">
        {{ t('auth.checking') }}
      </InlineFeedback>

      <template v-else-if="session.state.phase === 'locked'">
        <InlineFeedback tone="warning">
          {{ t('auth.locked', { seconds: countdown.seconds.value }) }}
        </InlineFeedback>
        <div class="auth-gate-actions">
          <AppButton
            type="button"
            variant="secondary"
            :disabled="countdown.active.value"
            @click="retryValidation"
          >
            {{ t('common.retry') }}
          </AppButton>
          <AppButton type="button" variant="ghost" @click="changeAuthKey">
            {{ t('common.changeKey') }}
          </AppButton>
        </div>
      </template>

      <template v-else-if="session.state.phase === 'network-error'">
        <InlineFeedback tone="danger">
          {{ t('auth.network') }}
        </InlineFeedback>
        <div class="auth-gate-actions">
          <AppButton type="button" variant="secondary" @click="retryValidation">
            {{ t('common.retry') }}
          </AppButton>
        </div>
      </template>

      <template v-else-if="session.state.phase === 'invalid-response'">
        <InlineFeedback tone="danger">
          {{ t('auth.invalidResponse') }}
        </InlineFeedback>
        <div class="auth-gate-actions">
          <AppButton
            data-test="invalid-response-retry"
            type="button"
            variant="secondary"
            @click="retryValidation"
          >
            {{ t('common.retry') }}
          </AppButton>
          <AppButton type="button" variant="ghost" @click="changeAuthKey">
            {{ t('common.changeKey') }}
          </AppButton>
        </div>
      </template>
    </SurfaceCard>
  </main>
</template>
