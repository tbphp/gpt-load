<script setup lang="ts">
import { toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useCountdown } from '@/features/auth/use-countdown'

const session = useAuthSession()
const router = useRouter()
const { t } = useI18n()
const countdown = useCountdown(toRef(session.state, 'retryAfterSeconds'))

if (session.state.phase !== 'validated') {
  void session.ensureValidated().catch(() => {})
}

async function retryValidation(): Promise<void> {
  try {
    await session.retryValidation()
  } catch {
    // The session state machine maps validation failures to a renderable phase.
  }
}

function changeAuthKey(): void {
  session.clear()
  void router.replace({ name: 'login' })
}
</script>

<template>
  <slot v-if="session.state.phase === 'validated'" />

  <main v-else class="auth-gate-shell">
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

      <InlineFeedback v-else-if="session.state.phase === 'invalid-response'" tone="danger">
        {{ t('auth.invalidResponse') }}
      </InlineFeedback>
    </SurfaceCard>
  </main>
</template>
