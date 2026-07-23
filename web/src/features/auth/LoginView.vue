<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, InvalidResponseError, NetworkError } from '@/api/errors'
import { safeRedirect } from '@/app/router'
import AppButton from '@/components/ui/AppButton.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useCountdown } from '@/features/auth/use-countdown'

type Feedback = 'invalid' | 'locked' | 'network' | 'invalid-response'

const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const candidate = ref('')
const submitting = ref(false)
const fieldError = ref('')
const feedback = ref<Feedback>()
const countdown = useCountdown(1)
const lockActive = computed(() => feedback.value === 'locked' && countdown.active.value)

watch(countdown.active, (active) => {
  if (!active && feedback.value === 'locked') {
    feedback.value = undefined
  }
})

async function submit(): Promise<void> {
  if (candidate.value === '') {
    fieldError.value = t('auth.required')
    return
  }
  if (/\s/u.test(candidate.value)) {
    fieldError.value = t('auth.invalidFormat')
    return
  }
  if (submitting.value || lockActive.value) {
    return
  }

  submitting.value = true
  fieldError.value = ''
  feedback.value = undefined

  try {
    await session.login(candidate.value)
    await router.replace(safeRedirect(route.query.redirect, router))
  } catch (error: unknown) {
    if (error instanceof ApiError && error.code === 'UNAUTHORIZED') {
      feedback.value = 'invalid'
    } else if (error instanceof ApiError && error.code === 'AUTH_LOCKED') {
      feedback.value = 'locked'
      countdown.reset(error.retryAfterSeconds ?? 1)
    } else if (error instanceof NetworkError) {
      feedback.value = 'network'
    } else if (error instanceof InvalidResponseError) {
      feedback.value = 'invalid-response'
    } else {
      feedback.value = 'invalid-response'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <SurfaceCard
      class="login-card"
      aria-labelledby="login-title"
      aria-describedby="login-description"
    >
      <header class="login-header">
        <h1 id="login-title" class="login-title">
          <span class="login-brand__mark" aria-hidden="true"></span>
          <span>{{ t('common.appName') }}</span>
          <span>{{ t('auth.loginTitle') }}</span>
        </h1>
        <p id="login-description" class="sr-only">{{ t('auth.loginDescription') }}</p>
      </header>

      <form class="login-form" novalidate @submit.prevent="submit">
        <FormField id="auth-key" :label="t('auth.keyLabel')" :error="fieldError">
          <template #default="{ describedBy }">
            <input
              id="auth-key"
              v-model="candidate"
              name="auth-key"
              type="password"
              autocomplete="off"
              autocapitalize="none"
              spellcheck="false"
              :placeholder="t('auth.keyPlaceholder')"
              :aria-describedby="describedBy"
              :disabled="submitting"
            />
          </template>
        </FormField>

        <InlineFeedback v-if="feedback === 'invalid'" tone="danger">
          {{ t('auth.invalid') }}
        </InlineFeedback>
        <InlineFeedback v-else-if="feedback === 'locked'" tone="warning">
          {{ t('auth.locked', { seconds: countdown.seconds.value }) }}
        </InlineFeedback>
        <InlineFeedback v-else-if="feedback === 'network'" tone="danger">
          {{ t('auth.network') }}
        </InlineFeedback>
        <InlineFeedback v-else-if="feedback === 'invalid-response'" tone="danger">
          {{ t('auth.invalidResponse') }}
        </InlineFeedback>

        <AppButton type="submit" :busy="submitting" :disabled="lockActive">
          {{ submitting ? t('auth.submitting') : t('auth.submit') }}
        </AppButton>
      </form>

      <aside class="login-help" :aria-label="t('auth.whereTitle')">
        <p>{{ t('auth.whereBody') }}</p>
        <p>
          <code>AUTH_KEY</code> · <code>${DATA_DIR}/auth.key</code> · {{ t('auth.dockerHint') }}
        </p>
      </aside>
    </SurfaceCard>
  </main>
</template>
