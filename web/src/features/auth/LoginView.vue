<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, InvalidResponseError, NetworkError } from '@/api/errors'
import { pageRouteNames } from '@/app/route-locations'
import { safeRedirect } from '@/app/router'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import DisclosurePanel from '@/components/ui/DisclosurePanel.vue'
import FormField from '@/components/ui/FormField.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useCountdown } from '@/features/auth/use-countdown'
import { useImportRecovery } from '@/features/import/import-recovery'

type Feedback = 'invalid' | 'locked' | 'network' | 'invalid-response'

const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const candidate = ref('')
const input = ref<HTMLInputElement>()
const visible = ref(false)
const submitting = ref(false)
const fieldError = ref('')
const feedback = ref<Feedback>()
const countdown = useCountdown(1)
const recovery = useImportRecovery()
const lockActive = computed(() => feedback.value === 'locked' && countdown.active.value)
const controlsDisabled = computed(() => submitting.value || lockActive.value)
const instanceOrigin = window.location.origin
const feedbackMessage = computed(() => {
  if (fieldError.value) return fieldError.value
  if (feedback.value === 'invalid') return t('auth.invalid')
  if (feedback.value === 'locked') {
    return t('auth.locked', { seconds: countdown.seconds.value })
  }
  if (feedback.value === 'network') return t('auth.network')
  if (feedback.value === 'invalid-response') return t('auth.invalidResponse')
  return ''
})
const feedbackTone = computed(() => (feedback.value === 'locked' ? 'warning' : 'danger'))
const keyInvalid = computed(() => Boolean(fieldError.value) || feedback.value === 'invalid')
const helpOpen = computed(() => route.query.help === 'auth')

watch(
  () => route.query,
  (query) => {
    const canonical: Record<string, string> = {}
    if (typeof query.redirect === 'string') canonical.redirect = query.redirect
    if (query.help === 'auth') canonical.help = 'auth'
    const keys = Object.keys(canonical)
    if (
      Object.keys(query).length !== keys.length ||
      keys.some((key) => query[key] !== canonical[key])
    ) {
      void router.replace({ name: pageRouteNames.login, query: canonical })
    }
  },
  { deep: true, immediate: true },
)

watch(countdown.active, (active) => {
  if (!active && feedback.value === 'locked') {
    feedback.value = undefined
    void focusInput()
  }
})

onMounted(() => {
  void focusInput()
})

async function focusInput(): Promise<void> {
  await nextTick()
  try {
    input.value?.focus({ preventScroll: true })
  } catch {
    input.value?.focus()
  }
}

function handleInput(): void {
  fieldError.value = ''
  if (feedback.value !== 'locked') {
    feedback.value = undefined
  }
}

async function retryFromHelp(): Promise<void> {
  if (candidate.value === '') {
    await focusInput()
    return
  }
  await submit()
}

function setHelpOpen(open: boolean): void {
  if (open === helpOpen.value) return
  const query: Record<string, string> = {}
  if (typeof route.query.redirect === 'string') query.redirect = route.query.redirect
  if (open) query.help = 'auth'
  void router.push({ name: pageRouteNames.login, query })
}

async function submit(): Promise<void> {
  if (candidate.value === '') {
    fieldError.value = t('auth.required')
    await focusInput()
    return
  }
  if (/\s/u.test(candidate.value)) {
    fieldError.value = t('auth.invalidFormat')
    await focusInput()
    return
  }
  if (controlsDisabled.value) {
    return
  }

  submitting.value = true
  fieldError.value = ''
  feedback.value = undefined

  try {
    await session.login(candidate.value)
    const target = safeRedirect(route.query.redirect, router)
    recovery.sweep()
    const preserveImportRecovery =
      session.getPrincipalType() === 'admin' &&
      router.resolve(target).name === pageRouteNames.import
    if (!preserveImportRecovery) recovery.clear()
    await router.replace(target)
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
  <PageFrame as="main" centered aria-labelledby="login-intro-title">
    <LedgerSheet class="ledger-login" :padded="false">
      <section class="ledger-login__intro" aria-labelledby="login-intro-title">
        <div>
          <p class="ledger-login__eyebrow">{{ t('auth.eyebrow') }}</p>
          <h1 id="login-intro-title" class="ledger-login__headline">
            {{ t('auth.headline') }}
          </h1>
          <p class="ledger-login__lead">{{ t('auth.lead') }}</p>

          <ol class="ledger-login__capabilities">
            <li v-for="number in 3" :key="number" class="ledger-login__capability">
              <span aria-hidden="true">{{ String(number).padStart(2, '0') }}</span>
              <div>
                <strong>{{ t(`auth.capabilities.${number}.title`) }}</strong>
                <p>{{ t(`auth.capabilities.${number}.description`) }}</p>
              </div>
            </li>
          </ol>
        </div>
      </section>

      <section class="ledger-login__auth" aria-labelledby="login-title">
        <div class="ledger-login__auth-inner">
          <h2 id="login-title">{{ t('auth.loginTitle') }}</h2>
          <p class="ledger-login__description">{{ t('auth.loginDescription') }}</p>

          <form class="ledger-login__form" novalidate @submit.prevent="submit">
            <FormField id="auth-key" :label="t('auth.keyLabel')">
              <div class="ledger-login__input-wrap">
                <input
                  id="auth-key"
                  ref="input"
                  v-model="candidate"
                  name="auth-key"
                  :type="visible ? 'text' : 'password'"
                  autocomplete="current-password"
                  autocapitalize="none"
                  spellcheck="false"
                  autofocus
                  :placeholder="t('auth.keyPlaceholder')"
                  aria-describedby="auth-feedback"
                  :aria-invalid="keyInvalid ? 'true' : undefined"
                  :disabled="controlsDisabled"
                  @input="handleInput"
                />
                <IconButton
                  class="ledger-login__reveal"
                  :label="visible ? t('auth.conceal') : t('auth.reveal')"
                  :pressed="visible"
                  :disabled="controlsDisabled"
                  variant="ghost"
                  size="xs"
                  @click="visible = !visible"
                >
                  <EyeOff v-if="visible" :size="15" aria-hidden="true" />
                  <Eye v-else :size="15" aria-hidden="true" />
                </IconButton>
              </div>
            </FormField>

            <div v-if="feedbackMessage" id="auth-feedback" class="ledger-login__feedback">
              <InlineFeedback :tone="feedbackTone" appearance="auth">
                {{ feedbackMessage }}
              </InlineFeedback>
            </div>

            <AppButton
              class="ledger-login__submit"
              type="submit"
              :busy="submitting"
              :disabled="controlsDisabled"
            >
              {{ submitting ? t('auth.submitting') : t('auth.submit') }}
            </AppButton>

            <p id="auth-session-note" class="ledger-login__session-note">
              {{ t('auth.sessionNotePrefix') }}<code>localStorage</code
              >{{ t('auth.sessionNoteSuffix') }}
            </p>
          </form>

          <DisclosurePanel
            class="ledger-login__auth-help"
            :summary="t('auth.help.title')"
            :open="helpOpen"
            @update:open="setHelpOpen"
          >
            <div class="ledger-login__auth-sources">
              <div class="ledger-login__auth-source">
                <strong>{{ t('auth.help.accessKeyTitle') }}</strong>
                <p>{{ t('auth.help.accessKeyDescription') }}</p>
              </div>
              <div class="ledger-login__auth-source">
                <strong>{{ t('auth.help.environmentTitle') }}</strong>
                <i18n-t keypath="auth.help.environmentDescription" tag="p">
                  <template #key>
                    <OverflowTooltip as="code" content="AUTH_KEY">AUTH_KEY</OverflowTooltip>
                  </template>
                </i18n-t>
              </div>
              <div class="ledger-login__auth-source">
                <strong>{{ t('auth.help.fileTitle') }}</strong>
                <i18n-t keypath="auth.help.fileDescription" tag="p">
                  <template #path>
                    <OverflowTooltip as="code" content="${DATA_DIR}/auth.key">
                      ${DATA_DIR}/auth.key
                    </OverflowTooltip>
                  </template>
                  <template #containerPath>
                    <OverflowTooltip as="code" content="/app/data/auth.key">
                      /app/data/auth.key
                    </OverflowTooltip>
                  </template>
                </i18n-t>
              </div>
              <div class="ledger-login__auth-source">
                <strong>{{ t('auth.help.dockerTitle') }}</strong>
                <i18n-t keypath="auth.help.dockerDescription" tag="p">
                  <template #command>
                    <OverflowTooltip as="code" content="docker exec -it gpt-load sh">
                      docker exec -it gpt-load sh
                    </OverflowTooltip>
                  </template>
                </i18n-t>
              </div>
            </div>
          </DisclosurePanel>

          <p class="ledger-login__recovery">
            {{ t('auth.recoveryPrefix') }}
            <AppButton
              variant="link"
              size="inline"
              :disabled="controlsDisabled"
              @click="retryFromHelp"
            >
              {{ t('auth.recoveryAction') }}
            </AppButton>
            {{ t('auth.recoverySuffix') }}
          </p>

          <p class="ledger-login__instance">
            <span aria-hidden="true"></span>
            {{ t('auth.instance', { origin: instanceOrigin }) }}
          </p>
        </div>
      </section>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.ledger-login {
  display: grid;
  min-height: 500px;
  grid-template-columns: minmax(0, 1fr) 420px;
}

.ledger-login__intro {
  display: flex;
  min-width: 0;
  flex-direction: column;
  justify-content: center;
  border-right: 1px solid var(--color-border-subtle);
  padding: 50px 56px 44px;
}

.ledger-login__eyebrow {
  margin: 0 0 var(--space-3);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.ledger-login__headline {
  max-width: 100%;
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-login);
  font-weight: 500;
  letter-spacing: -0.025em;
  line-height: 1.22;
  text-wrap: balance;
}

.ledger-login__lead {
  max-width: 570px;
  margin: 15px 0 0;
  color: var(--color-text-muted);
  font-size: 14px;
  line-height: 1.75;
}

.ledger-login__capabilities {
  display: grid;
  margin: 38px 0 0;
  border-top: 1px solid var(--color-border-subtle);
  padding: 0;
  list-style: none;
}

.ledger-login__capability {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr);
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 13px 0;
}

.ledger-login__capability > span {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.ledger-login__capability strong {
  display: block;
  font-size: 12.5px;
  font-weight: 600;
}

.ledger-login__capability p {
  margin: 2px 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  line-height: normal;
}

.ledger-login__auth {
  display: flex;
  min-width: 0;
  flex-direction: column;
  justify-content: center;
  background: var(--color-surface-sunken);
  padding: 44px 42px;
}

.ledger-login__auth-inner {
  width: 100%;
  min-width: 0;
}

.ledger-login__auth h2 {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-panel);
  font-weight: 500;
  letter-spacing: -0.015em;
}

.ledger-login__description {
  margin: 7px 0 0;
  color: var(--color-text-faint);
  font-size: 12.5px;
  line-height: 1.6;
}

.ledger-login__form {
  display: grid;
  gap: 14px;
  margin-top: var(--space-6);
}

.ledger-login__input-wrap {
  position: relative;
}

.ledger-login__input-wrap input {
  padding-right: 42px;
  font-family: var(--font-mono);
  font-size: 12.5px;
}

.ledger-login__reveal {
  position: absolute;
  top: 50%;
  right: 7px;
  transform: translateY(-50%);
}

.ledger-login__feedback {
  min-width: 0;
}

.ledger-login__submit {
  width: 100%;
}

.ledger-login__session-note {
  margin: 0;
  color: var(--color-text-faint);
  font-size: 11px;
  line-height: 1.6;
}

.ledger-login__session-note code {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}

.ledger-login__auth-help {
  margin-top: 17px;
}

.ledger-login__auth-sources {
  display: grid;
  gap: var(--space-2);
}

.ledger-login__auth-source {
  border-left: 2px solid var(--color-border-control);
  padding-left: 9px;
}

.ledger-login__auth-source strong {
  display: block;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.ledger-login__auth-source p {
  margin: 2px 0 0;
  color: var(--color-text-faint);
  font-size: 10px;
  line-height: 1.55;
}

.ledger-login__auth-source code {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  vertical-align: bottom;
  white-space: nowrap;
}

.ledger-login__recovery {
  margin: var(--space-3) 0 0;
  color: var(--color-text-faint);
  font-size: 10px;
  line-height: 1.55;
}

.ledger-login__instance {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  margin: 22px 0 0;
  border-top: 1px solid var(--color-border-subtle);
  color: var(--color-text-faint);
  padding-top: var(--space-4);
  font-family: var(--font-mono);
  font-size: 11px;
  overflow-wrap: anywhere;
}

.ledger-login__instance > span {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: var(--color-success);
}

@media (max-width: 860px) {
  .ledger-login {
    min-height: 0;
    grid-template-columns: 1fr;
  }

  .ledger-login__intro {
    border-right: 0;
    border-bottom: 1px solid var(--color-border-subtle);
    padding: 34px 30px 30px;
  }

  .ledger-login__headline {
    font-size: 29px;
  }

  .ledger-login__capabilities {
    margin-top: 26px;
  }

  .ledger-login__auth {
    padding: 34px 30px 38px;
  }
}

@media (max-width: 520px) {
  .ledger-login__intro,
  .ledger-login__auth {
    padding-inline: 22px;
  }

  .ledger-login__headline {
    font-size: var(--title-lede);
  }

  .ledger-login__capability p {
    line-height: var(--line-normal);
  }
}
</style>
