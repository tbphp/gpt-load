<script setup lang="ts">
import { ExternalLink, FileJson, Plus, Send, Trash2 } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  beginCredentialAuthorization,
  cancelCredentialStage,
  completeCredentialAuthorization,
  getCredentialStage,
  importCredentialStage,
  type CredentialStage,
} from '@/app/resources/credential-stages'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppButton from '@/components/ui/AppButton.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { presentSubscriptionErrorKey } from '@/features/subscription-error-presenter'

const OAUTH_JSON_PLACEHOLDER = '{"type":"codex","access_token":"...","refresh_token":"..."}'

const props = withDefaults(
  defineProps<{
    modelValue: CredentialStage[]
    disabled?: boolean
    single?: boolean
    compact?: boolean
    hideHeader?: boolean
  }>(),
  { disabled: false, single: false, compact: false, hideHeader: false },
)
const emit = defineEmits<{ 'update:modelValue': [stages: CredentialStage[]] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const busyAction = ref<'authorize' | 'import' | `callback:${string}` | ''>('')
const feedbackKey = ref('')
const oauthJSON = ref('')
const callbackURLs = ref<Record<string, string>>({})
const callbackErrorKeys = ref<Record<string, string>>({})
const polling = new Map<string, { timer?: number; controller?: AbortController }>()
const expiryTimers = new Map<string, number>()
const readyCount = computed(
  () => props.modelValue.filter(({ status }) => status === 'ready').length,
)
const canAdd = computed(() => !props.single || props.modelValue.length === 0)

function replaceStage(stage: CredentialStage): void {
  const existing = props.modelValue.find((item) => item.stage_id === stage.stage_id)
  const merged = existing?.authorization_url
    ? { ...stage, authorization_url: stage.authorization_url ?? existing.authorization_url }
    : stage
  const index = props.modelValue.findIndex((item) => item.stage_id === stage.stage_id)
  emit(
    'update:modelValue',
    index === -1
      ? [...props.modelValue, merged]
      : props.modelValue.map((item, itemIndex) => (itemIndex === index ? merged : item)),
  )
  if (!shouldPoll(stage)) {
    clearCallbackURL(stage.stage_id)
    clearCallbackError(stage.stage_id)
  }
}

function clearCallbackURL(stageID: string): void {
  if (!(stageID in callbackURLs.value)) return
  const next = { ...callbackURLs.value }
  delete next[stageID]
  callbackURLs.value = next
}

function clearCallbackError(stageID: string): void {
  if (!(stageID in callbackErrorKeys.value)) return
  const next = { ...callbackErrorKeys.value }
  delete next[stageID]
  callbackErrorKeys.value = next
}

function stopPolling(stageID: string): void {
  const state = polling.get(stageID)
  if (!state) return
  if (state.timer !== undefined) window.clearTimeout(state.timer)
  state.controller?.abort()
  polling.delete(stageID)
}

function stopExpiryTimer(stageID: string): void {
  const timer = expiryTimers.get(stageID)
  if (timer !== undefined) window.clearTimeout(timer)
  expiryTimers.delete(stageID)
}

function shouldPoll(stage: CredentialStage): boolean {
  return stage.status === 'pending_authorization' || stage.status === 'exchanging'
}

function expireReadyStage(stageID: string): void {
  stopExpiryTimer(stageID)
  const stage = props.modelValue.find((item) => item.stage_id === stageID)
  if (!stage || stage.status !== 'ready') return
  if (stage.expires_at_ms > Date.now()) {
    scheduleExpiry(stage)
    return
  }
  replaceStage({ ...stage, status: 'expired' })
}

function scheduleExpiry(stage: CredentialStage): void {
  stopExpiryTimer(stage.stage_id)
  if (stage.status !== 'ready') return
  const delay = Math.max(0, Math.min(stage.expires_at_ms - Date.now() + 10, 2_147_483_647))
  expiryTimers.set(
    stage.stage_id,
    window.setTimeout(() => expireReadyStage(stage.stage_id), delay),
  )
}

function schedulePoll(stage: CredentialStage): void {
  if (!shouldPoll(stage) || polling.has(stage.stage_id)) return
  const state: { timer?: number; controller?: AbortController } = {}
  polling.set(stage.stage_id, state)
  const poll = async () => {
    const controller = new AbortController()
    state.controller = controller
    try {
      const next = await getCredentialStage(client, stage.stage_id, controller.signal)
      if (feedbackKey.value === 'import.subscription.pollFailed') feedbackKey.value = ''
      replaceStage(next)
      if (!shouldPoll(next)) {
        stopPolling(stage.stage_id)
        return
      }
    } catch {
      if (!controller.signal.aborted) feedbackKey.value = 'import.subscription.pollFailed'
    } finally {
      state.controller = undefined
    }
    if (polling.has(stage.stage_id)) state.timer = window.setTimeout(poll, 1_200)
  }
  state.timer = window.setTimeout(poll, 800)
}

watch(
  () => props.modelValue,
  (stages) => {
    for (const stage of stages) {
      schedulePoll(stage)
      scheduleExpiry(stage)
    }
    const live = new Set(stages.map(({ stage_id }) => stage_id))
    for (const stageID of polling.keys()) if (!live.has(stageID)) stopPolling(stageID)
    for (const stageID of expiryTimers.keys()) if (!live.has(stageID)) stopExpiryTimer(stageID)
  },
  { deep: true, immediate: true },
)

async function beginAuthorization(): Promise<void> {
  if (props.disabled || busyAction.value || !canAdd.value) return
  feedbackKey.value = ''
  const popup = window.open(
    'about:blank',
    `gpt-load-codex-oauth-${Date.now()}`,
    'popup,width=720,height=820,resizable=yes,scrollbars=yes',
  )
  busyAction.value = 'authorize'
  try {
    const stage = await beginCredentialAuthorization(client)
    replaceStage(stage)
    schedulePoll(stage)
    if (popup && stage.authorization_url) popup.location.replace(stage.authorization_url)
    else feedbackKey.value = 'import.subscription.popupBlocked'
  } catch (cause) {
    popup?.close()
    feedbackKey.value = presentSubscriptionErrorKey(cause, 'import.subscription.authorizeFailed')
  } finally {
    busyAction.value = ''
  }
}

async function importFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  await importOAuthJSON(file)
}

async function importText(): Promise<void> {
  const value = oauthJSON.value.trim()
  if (!value) return
  await importOAuthJSON(new File([value], 'codex-oauth.json', { type: 'application/json' }))
}

async function importOAuthJSON(file: File): Promise<void> {
  if (props.disabled || busyAction.value || !canAdd.value) return
  feedbackKey.value = ''
  busyAction.value = 'import'
  try {
    replaceStage(await importCredentialStage(client, file))
    oauthJSON.value = ''
  } catch (cause) {
    feedbackKey.value = presentSubscriptionErrorKey(cause, 'import.subscription.importFailed')
  } finally {
    busyAction.value = ''
  }
}

async function submitCallback(stage: CredentialStage): Promise<void> {
  const callbackURL = callbackURLs.value[stage.stage_id]?.trim() ?? ''
  if (!callbackURL || props.disabled || busyAction.value) return
  feedbackKey.value = ''
  clearCallbackError(stage.stage_id)
  busyAction.value = `callback:${stage.stage_id}`
  try {
    replaceStage(await completeCredentialAuthorization(client, stage.stage_id, callbackURL))
  } catch (cause) {
    callbackErrorKeys.value = {
      ...callbackErrorKeys.value,
      [stage.stage_id]: presentSubscriptionErrorKey(cause, 'import.subscription.callbackFailed'),
    }
  } finally {
    busyAction.value = ''
  }
}

async function removeStage(stage: CredentialStage): Promise<void> {
  if (props.disabled) return
  feedbackKey.value = ''
  if (stage.status === 'pending_authorization' || stage.status === 'ready') {
    try {
      await cancelCredentialStage(client, stage.stage_id)
    } catch (cause) {
      feedbackKey.value = presentSubscriptionErrorKey(cause, 'import.subscription.cancelFailed')
      return
    }
  }
  stopPolling(stage.stage_id)
  stopExpiryTimer(stage.stage_id)
  clearCallbackURL(stage.stage_id)
  clearCallbackError(stage.stage_id)
  emit(
    'update:modelValue',
    props.modelValue.filter(({ stage_id }) => stage_id !== stage.stage_id),
  )
}

function statusTone(stage: CredentialStage): 'success' | 'warning' | 'danger' | 'neutral' {
  if (stage.status === 'ready') return 'success'
  if (stage.status === 'pending_authorization' || stage.status === 'exchanging') return 'warning'
  if (stage.status === 'consumed') return 'neutral'
  return 'danger'
}

function callbackDescribedBy(stageID: string): string {
  return [
    `oauth-callback-${stageID}-help`,
    callbackErrorKeys.value[stageID] ? `oauth-callback-${stageID}-error` : '',
  ]
    .filter(Boolean)
    .join(' ')
}

function stageErrorKey(code: string): string {
  const known: Readonly<Record<string, string>> = {
    authorization_denied: 'import.subscription.stageError.authorizationDenied',
    authorization_failed: 'import.subscription.stageError.authorizationFailed',
    authorization_exchange_rejected: 'import.subscription.stageError.exchangeRejected',
    authorization_exchange_unknown: 'import.subscription.stageError.exchangeUnknown',
    authorization_exchange_interrupted: 'import.subscription.stageError.exchangeInterrupted',
  }
  return known[code] ?? 'import.subscription.stageError.unknown'
}

onBeforeUnmount(() => {
  for (const stageID of [...polling.keys()]) stopPolling(stageID)
  for (const stageID of [...expiryTimers.keys()]) stopExpiryTimer(stageID)
  oauthJSON.value = ''
  callbackURLs.value = {}
  callbackErrorKeys.value = {}
})
</script>

<template>
  <section
    class="subscription-stager"
    :class="{ 'subscription-stager--compact': compact }"
    :aria-labelledby="hideHeader ? undefined : 'subscription-stager-title'"
    :aria-label="hideHeader ? t('import.subscription.title') : undefined"
  >
    <header v-if="!hideHeader" class="subscription-stager__header">
      <div>
        <h2 id="subscription-stager-title">{{ t('import.subscription.title') }}</h2>
        <p>{{ t('import.subscription.description') }}</p>
      </div>
      <span v-if="readyCount" class="subscription-stager__count">
        {{ t('import.subscription.readyCount', { count: readyCount }) }}
      </span>
    </header>

    <InlineFeedback tone="neutral" appearance="ledger-hint" glyph="i">
      {{ t('import.subscription.securityNotice') }}
    </InlineFeedback>

    <div v-if="modelValue.length" class="subscription-stager__accounts">
      <article
        v-for="stage in modelValue"
        :key="stage.stage_id"
        class="subscription-stager__account"
      >
        <div class="subscription-stager__account-summary">
          <div class="subscription-stager__identity">
            <strong>{{
              stage.account.email_mask || t('import.subscription.pendingAccount')
            }}</strong>
            <span>
              {{ t('import.subscription.expires') }}
              <AppRelativeTime
                :instant="stage.expires_at_ms"
                :locale="locale"
                :empty-label="t('import.subscription.unknown')"
              />
            </span>
            <span v-if="stage.account.expires_at_ms">
              {{ t('import.subscription.tokenExpires') }}
              <AppRelativeTime
                :instant="stage.account.expires_at_ms"
                :locale="locale"
                :empty-label="t('import.subscription.unknown')"
              />
            </span>
            <span v-if="stage.account.last_refresh_at_ms">
              {{ t('import.subscription.lastRefresh') }}
              <AppRelativeTime
                :instant="stage.account.last_refresh_at_ms"
                :locale="locale"
                :empty-label="t('import.subscription.unknown')"
              />
            </span>
          </div>
          <StatusBadge :tone="statusTone(stage)" size="compact">
            {{ t(`import.subscription.status.${stage.status}`) }}
          </StatusBadge>
          <AppButton
            variant="ghost"
            tone="danger"
            size="compact"
            :disabled="disabled || stage.status === 'exchanging'"
            @click="removeStage(stage)"
          >
            <Trash2 :size="14" aria-hidden="true" />{{ t('import.subscription.remove') }}
          </AppButton>
        </div>

        <p v-if="stage.error_code" class="subscription-stager__stage-error" role="alert">
          {{ t(stageErrorKey(stage.error_code)) }}
        </p>

        <div
          v-if="stage.status === 'pending_authorization' && stage.authorization_url"
          class="subscription-stager__authorization"
        >
          <span class="subscription-stager__authorization-label">
            {{ t('import.subscription.authorizationLink') }}
          </span>
          <div class="subscription-stager__authorization-link">
            <code>{{ stage.authorization_url }}</code>
            <CopyButton
              :value="stage.authorization_url"
              :label="t('import.subscription.copyAuthorization')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
            />
            <a
              :href="stage.authorization_url"
              target="_blank"
              rel="noopener noreferrer"
              :aria-label="t('import.subscription.openAuthorization')"
            >
              <ExternalLink :size="16" aria-hidden="true" />
              <span>{{ t('import.subscription.openAuthorization') }}</span>
            </a>
          </div>

          <form class="subscription-stager__callback" @submit.prevent="submitCallback(stage)">
            <label :for="`oauth-callback-${stage.stage_id}`">
              {{ t('import.subscription.callbackLabel') }}
            </label>
            <input
              :id="`oauth-callback-${stage.stage_id}`"
              v-model="callbackURLs[stage.stage_id]"
              type="url"
              autocomplete="off"
              autocapitalize="none"
              spellcheck="false"
              :disabled="disabled || Boolean(busyAction)"
              :placeholder="t('import.subscription.callbackPlaceholder')"
              :aria-invalid="callbackErrorKeys[stage.stage_id] ? 'true' : undefined"
              :aria-describedby="callbackDescribedBy(stage.stage_id)"
            />
            <AppButton
              type="submit"
              variant="secondary"
              :busy="busyAction === `callback:${stage.stage_id}`"
              :disabled="disabled || Boolean(busyAction) || !callbackURLs[stage.stage_id]?.trim()"
            >
              <Send :size="15" aria-hidden="true" />{{ t('import.subscription.submitCallback') }}
            </AppButton>
            <p :id="`oauth-callback-${stage.stage_id}-help`">
              {{ t('import.subscription.callbackHelp') }}
            </p>
            <p
              v-if="callbackErrorKeys[stage.stage_id]"
              :id="`oauth-callback-${stage.stage_id}-error`"
              class="subscription-stager__callback-error"
              role="alert"
            >
              {{ t(callbackErrorKeys[stage.stage_id]) }}
            </p>
          </form>
        </div>
      </article>
    </div>

    <div v-if="canAdd" class="subscription-stager__entry">
      <div class="subscription-stager__actions">
        <AppButton
          :busy="busyAction === 'authorize'"
          :disabled="disabled || Boolean(busyAction)"
          @click="beginAuthorization"
        >
          <Plus :size="16" aria-hidden="true" />{{ t('import.subscription.authorize') }}
        </AppButton>
        <span>{{ t('import.subscription.orImport') }}</span>
      </div>

      <FormField
        id="subscription-oauth-json"
        :label="t('import.subscription.oauthJSONLabel')"
        :description="t('import.subscription.oauthJSONDescription')"
        size="compact"
      >
        <template #default="field">
          <textarea
            id="subscription-oauth-json"
            v-model="oauthJSON"
            rows="5"
            :disabled="disabled || Boolean(busyAction)"
            :aria-describedby="field.describedBy"
            autocomplete="off"
            autocapitalize="none"
            spellcheck="false"
            :placeholder="OAUTH_JSON_PLACEHOLDER"
          ></textarea>
        </template>
      </FormField>

      <div class="subscription-stager__import-actions">
        <AppButton
          variant="secondary"
          :busy="busyAction === 'import'"
          :disabled="disabled || Boolean(busyAction) || !oauthJSON.trim()"
          @click="importText"
        >
          <FileJson :size="16" aria-hidden="true" />{{ t('import.subscription.importText') }}
        </AppButton>
        <label
          class="subscription-stager__file"
          :class="{ 'is-disabled': disabled || Boolean(busyAction) }"
        >
          <FileJson :size="16" aria-hidden="true" />
          {{
            busyAction === 'import'
              ? t('import.subscription.importing')
              : t('import.subscription.importFile')
          }}
          <input
            type="file"
            accept="application/json,.json"
            :disabled="disabled || Boolean(busyAction)"
            @change="importFile"
          />
        </label>
      </div>
    </div>
    <p class="subscription-stager__callback-note">{{ t('import.subscription.callbackNotice') }}</p>
    <InlineFeedback v-if="feedbackKey" tone="danger" appearance="ledger">
      {{ t(feedbackKey) }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.subscription-stager {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}
.subscription-stager--compact {
  border-bottom: 0;
  padding: 18px 0;
}
.subscription-stager__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.subscription-stager__header h2,
.subscription-stager__header p,
.subscription-stager__callback-note {
  margin: 0;
}
.subscription-stager__header h2 {
  font-size: var(--title-section);
  font-weight: 650;
}
.subscription-stager__header p,
.subscription-stager__callback-note {
  margin-top: 4px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  line-height: 1.55;
}
.subscription-stager__count {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-success-bg);
  color: var(--color-text);
  padding: 4px 8px;
  font-size: var(--text-label-xs);
  font-weight: 600;
}
.subscription-stager__accounts {
  display: grid;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  overflow: hidden;
}
.subscription-stager__account {
  display: grid;
  gap: 10px;
  min-height: 60px;
  background: var(--color-surface);
  padding: 11px 12px;
}
.subscription-stager__account + .subscription-stager__account {
  border-top: 1px solid var(--color-border-subtle);
}
.subscription-stager__account-summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
}
.subscription-stager__identity {
  display: grid;
  min-width: 0;
  gap: 3px;
}
.subscription-stager__identity strong {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-stager__identity span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-stager__stage-error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
.subscription-stager__authorization {
  display: grid;
  gap: 7px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 10px;
}
.subscription-stager__authorization-label,
.subscription-stager__callback label {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 600;
}
.subscription-stager__authorization-link {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-1);
}
.subscription-stager__authorization-link code {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 9px 10px;
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-stager__authorization-link a {
  display: inline-flex;
  min-width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  color: var(--color-action);
  padding: 0 10px;
  font-size: var(--text-label-xs);
  font-weight: 600;
  text-decoration: none;
}
.subscription-stager__callback {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 6px var(--space-2);
}
.subscription-stager__callback label,
.subscription-stager__callback p {
  grid-column: 1 / -1;
}
.subscription-stager__callback .subscription-stager__callback-error {
  color: var(--color-danger);
}
.subscription-stager__callback input {
  min-width: 0;
  height: var(--control-md);
  font-family: var(--font-mono);
}
.subscription-stager__callback p {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
.subscription-stager__entry {
  display: grid;
  gap: var(--space-3);
}
.subscription-stager__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.subscription-stager__actions > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-stager__entry textarea {
  width: 100%;
  resize: vertical;
  font-family: var(--font-mono);
  line-height: 1.55;
}
.subscription-stager__import-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.subscription-stager__file {
  display: inline-flex;
  min-height: var(--control-md);
  align-items: center;
  justify-content: center;
  gap: 7px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 0 12px;
  font-size: var(--text-button);
  font-weight: 560;
  cursor: pointer;
}
.subscription-stager__file:hover:not(.is-disabled) {
  border-color: var(--color-text-faint);
  color: var(--color-text);
}
.subscription-stager__file:focus-within {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 2px;
}
.subscription-stager__file.is-disabled {
  cursor: not-allowed;
  opacity: 0.46;
}
.subscription-stager__file input {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
@media (max-width: 640px) {
  .subscription-stager__header,
  .subscription-stager__actions {
    align-items: stretch;
    flex-direction: column;
  }
  .subscription-stager__account {
    padding: 10px;
  }
  .subscription-stager__account-summary {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .subscription-stager__account-summary :deep(.app-button) {
    grid-column: 1 / -1;
    justify-self: start;
    min-height: var(--touch-target);
  }
  .subscription-stager__callback {
    grid-template-columns: 1fr;
  }
  .subscription-stager__callback :deep(.app-button) {
    width: 100%;
    min-height: var(--touch-target);
  }
  .subscription-stager__actions :deep(.app-button),
  .subscription-stager__import-actions :deep(.app-button),
  .subscription-stager__file {
    width: 100%;
    min-height: var(--touch-target);
  }
  .subscription-stager__authorization-link {
    grid-template-columns: minmax(0, 1fr) auto auto;
  }
}
</style>
