<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, useRoute, useRouter } from 'vue-router'

import AppButton from '@modern/components/ui/AppButton.vue'
import AppExternalLink from '@modern/components/ui/AppExternalLink.vue'
import AppIconButton from '@modern/components/ui/AppIconButton.vue'
import AppNotice from '@modern/components/ui/AppNotice.vue'
import AppTextField from '@modern/components/ui/AppTextField.vue'
import { safeRedirect } from '@modern/router'
import { RequestCancelledError } from '@shared/http/errors'
import AuthCard from './AuthCard.vue'
import { authFailure, authRetrySeconds, type AuthFailure } from './auth-errors'
import { useAuthSession } from './auth-session'
import { useCountdown } from './use-countdown'

const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const candidate = ref('')
const input = ref<InstanceType<typeof AppTextField>>()
const visible = ref(false)
const submitting = ref(false)
const fieldError = ref<'required' | 'invalidFormat'>()
const feedback = ref<AuthFailure>()
const authenticated = ref(false)
const navigationFailed = ref(false)
const controller = new AbortController()
const countdown = useCountdown()
const helpOpen = computed(() => route.query.help === 'auth')
const locked = computed(() => feedback.value === 'locked' && countdown.active.value)
const disabled = computed(() => submitting.value || locked.value)
const instanceOrigin = window.location.origin
const message = computed(() => {
  if (navigationFailed.value) return t('shell.navigationFailed')
  if (feedback.value === 'locked') return t('auth.locked', { seconds: countdown.seconds.value })
  if (feedback.value === 'invalid-response') return t('auth.invalidResponse')
  return feedback.value ? t(`auth.${feedback.value}`) : ''
})

watch(
  () => route.query,
  (query) => {
    const canonical: Record<string, string> = {}
    if (typeof query.redirect === 'string') canonical.redirect = query.redirect
    if (query.help === 'auth') canonical.help = 'auth'
    if (
      Object.keys(query).length !== Object.keys(canonical).length ||
      Object.keys(canonical).some((key) => query[key] !== canonical[key])
    ) {
      void router.replace({ name: 'modern-login', query: canonical })
    }
  },
  { immediate: true },
)
watch(candidate, () => {
  fieldError.value = undefined
  if (feedback.value !== 'locked') feedback.value = undefined
})
watch(countdown.active, (active) => {
  if (!active && feedback.value === 'locked') {
    feedback.value = undefined
    void focusInput()
  }
})
onMounted(() => {
  void focusInput()
})
onScopeDispose(() => controller.abort())

async function focusInput(): Promise<void> {
  await nextTick()
  input.value?.focus()
}

function toggleHelp(event: Event): void {
  const open = (event.currentTarget as HTMLDetailsElement).open
  if (open === helpOpen.value) return
  const query: Record<string, string> = {}
  if (typeof route.query.redirect === 'string') query.redirect = route.query.redirect
  if (open) query.help = 'auth'
  void router.push({ name: 'modern-login', query })
}

async function continueToPage(): Promise<void> {
  navigationFailed.value = false
  try {
    const failure = await router.replace(safeRedirect(route.query.redirect, router))
    navigationFailed.value = isNavigationFailure(failure)
  } catch {
    navigationFailed.value = true
  }
}

async function submit(): Promise<void> {
  if (disabled.value) return
  if (candidate.value === '' || /\s/u.test(candidate.value)) {
    fieldError.value = candidate.value === '' ? 'required' : 'invalidFormat'
    await focusInput()
    return
  }
  submitting.value = true
  feedback.value = undefined
  fieldError.value = undefined
  try {
    await session.login(candidate.value, controller.signal)
    authenticated.value = true
    candidate.value = ''
    visible.value = false
    await continueToPage()
  } catch (error: unknown) {
    if (error instanceof RequestCancelledError || controller.signal.aborted) return
    feedback.value = authFailure(error)
    if (feedback.value === 'locked') countdown.start(authRetrySeconds(error))
  } finally {
    submitting.value = false
  }
  if (feedback.value && feedback.value !== 'locked') await focusInput()
}
</script>

<template>
  <AuthCard :title="t('auth.title')" :description="t('auth.description')">
    <form class="modern-login-form" novalidate @submit.prevent="submit">
      <template v-if="!authenticated">
        <AppTextField
          ref="input"
          v-model="candidate"
          :label="t('auth.keyLabel')"
          name="auth-key"
          :type="visible ? 'text' : 'password'"
          autocomplete="current-password"
          autocapitalize="none"
          :spellcheck="false"
          :placeholder="t('auth.keyPlaceholder')"
          :disabled="disabled"
          :error="fieldError ? t(`auth.${fieldError}`) : undefined"
          :invalid="feedback === 'invalid'"
          :described-by="message ? 'modern-auth-feedback' : undefined"
        >
          <template #suffix>
            <AppIconButton
              :icon="visible ? EyeOff : Eye"
              :label="t(visible ? 'auth.conceal' : 'auth.reveal')"
              size="xs"
              :aria-pressed="visible"
              :disabled="disabled"
              @click="visible = !visible"
            />
          </template>
        </AppTextField>
        <AppNotice
          v-if="message"
          id="modern-auth-feedback"
          :tone="feedback === 'locked' ? 'warning' : 'danger'"
          >{{ message }}</AppNotice
        >
        <AppButton
          class="modern-login-submit"
          variant="primary"
          type="submit"
          :loading="submitting"
          :disabled="disabled"
        >
          {{ t(submitting ? 'auth.submitting' : 'auth.submit') }}
        </AppButton>
      </template>
      <template v-else>
        <AppNotice :tone="navigationFailed ? 'danger' : 'info'">{{
          navigationFailed ? message : t('auth.signedIn')
        }}</AppNotice>
        <AppButton v-if="navigationFailed" variant="primary" @click="continueToPage">{{
          t('auth.continue')
        }}</AppButton>
      </template>
      <p class="modern-login-note">{{ t('auth.sessionNote') }}</p>
    </form>

    <details class="modern-login-help" :open="helpOpen" @toggle="toggleHelp">
      <summary>{{ t('auth.help.title') }}</summary>
      <div class="modern-login-help-content">
        <p>
          <strong>{{ t('auth.help.accessKeyTitle') }}</strong
          >{{ t('auth.help.accessKey') }}
        </p>
        <p>
          <strong>{{ t('auth.help.adminTitle') }}</strong
          >{{ t('auth.help.admin') }}
        </p>
        <p>
          {{
            t('auth.help.file', {
              path: '${DATA_DIR}/auth.key',
              containerPath: '/app/data/auth.key',
            })
          }}
        </p>
        <p>{{ t('auth.help.docker') }}<code>docker exec -it gpt-load sh</code></p>
        <AppExternalLink href="https://www.gpt-load.com/docs">{{
          t('shell.documentation')
        }}</AppExternalLink>
      </div>
    </details>
    <p class="modern-login-instance">{{ instanceOrigin }}</p>
  </AuthCard>
</template>

<style scoped>
.modern-login-form {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-login-form .modern-login-submit {
  width: 100%;
}
.modern-login-note,
.modern-login-instance {
  color: var(--modern-muted);
  font-size: var(--modern-text-small);
  overflow-wrap: anywhere;
}
.modern-login-instance {
  font-family: var(--modern-font-mono);
  text-align: center;
}
.modern-login-help {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-4);
  font-size: var(--modern-text-small);
}
.modern-login-help summary {
  width: fit-content;
  color: var(--modern-muted);
  cursor: pointer;
}
.modern-login-help-content {
  display: grid;
  gap: var(--modern-space-3);
  margin-top: var(--modern-space-4);
  color: var(--modern-muted);
  overflow-wrap: anywhere;
}
.modern-login-help-content strong {
  display: block;
  margin-bottom: var(--modern-space-1);
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-login-help-content code {
  display: block;
  margin-top: var(--modern-space-1);
  font-family: var(--modern-font-mono);
}
.modern-login-help-content a {
  width: fit-content;
  color: var(--modern-accent);
  text-decoration: underline;
}
</style>
