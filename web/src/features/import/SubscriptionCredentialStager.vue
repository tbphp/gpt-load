<script setup lang="ts">
import { ExternalLink, FileJson, Plus, Send } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import CopyChip from '@/components/ui/CopyChip.vue'
import DisclosurePanel from '@/components/ui/DisclosurePanel.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { presentSubscriptionErrorKey } from '@/features/subscription-error-presenter'

const OAUTH_JSON_PLACEHOLDER = '{"type":"codex","access_token":"...","refresh_token":"..."}'

const props = withDefaults(
  defineProps<{
    modelValue: CredentialStage[]
    channelId: string
    disabled?: boolean
    single?: boolean
    compact?: boolean
    hideHeader?: boolean
    step?: number
    /**
     * create：账号在创建分组时写入；connect：账号确认后加入已有分组。
     * 只影响面向用户的措辞，不改变暂存行为。
     */
    context?: 'create' | 'connect'
  }>(),
  {
    disabled: false,
    single: false,
    compact: false,
    hideHeader: false,
    step: undefined,
    context: 'create',
  },
)
const emit = defineEmits<{ 'update:modelValue': [stages: CredentialStage[]] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const busyAction = ref<'authorize' | 'import' | `callback:${string}` | ''>('')
const feedbackKey = ref('')
const oauthJSON = ref('')
const callbackURLs = ref<Record<string, string>>({})
const callbackErrorKeys = ref<Record<string, string>>({})
// 弹窗被拦截时授权已经开始，唯一出路是手动打开链接，这里额外提示一句。
const popupBlockedStages = ref<Record<string, boolean>>({})
const jsonImportOpen = ref(false)
const nowMS = ref(Date.now())
const polling = new Map<
  string,
  { timer?: number; controller?: AbortController; failures: number }
>()
const expiryTimers = new Map<string, number>()
let countdownTimer: number | undefined
const readyCount = computed(
  () => props.modelValue.filter(({ status }) => status === 'ready').length,
)
const hasAccounts = computed(() => props.modelValue.length > 0)
const canAdd = computed(() => !props.single || props.modelValue.length === 0)
const entryBusy = computed(() => props.disabled || Boolean(busyAction.value))

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
    clearStageFlags(stage.stage_id)
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

function clearStageFlags(stageID: string): void {
  if (!(stageID in popupBlockedStages.value)) return
  const next = { ...popupBlockedStages.value }
  delete next[stageID]
  popupBlockedStages.value = next
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

// 连续失败到这个次数就停止轮询：暂存已经被删除或服务端持续不可用时，
// 无限重试只会一直刷错误，用户反而看不出该重新授权。
const POLL_FAILURE_LIMIT = 5

function schedulePoll(stage: CredentialStage): void {
  if (!shouldPoll(stage) || polling.has(stage.stage_id)) return
  const state: { timer?: number; controller?: AbortController; failures: number } = { failures: 0 }
  polling.set(stage.stage_id, state)
  const poll = async () => {
    const controller = new AbortController()
    state.controller = controller
    try {
      const next = await getCredentialStage(client, stage.stage_id, controller.signal)
      state.failures = 0
      if (feedbackKey.value === 'import.subscription.pollFailed') feedbackKey.value = ''
      replaceStage(next)
      if (!shouldPoll(next)) {
        stopPolling(stage.stage_id)
        return
      }
    } catch {
      if (!controller.signal.aborted) {
        state.failures += 1
        feedbackKey.value =
          state.failures >= POLL_FAILURE_LIMIT
            ? 'import.subscription.pollAbandoned'
            : 'import.subscription.pollFailed'
        if (state.failures >= POLL_FAILURE_LIMIT) {
          stopPolling(stage.stage_id)
          return
        }
      }
    } finally {
      state.controller = undefined
    }
    if (polling.has(stage.stage_id)) {
      state.timer = window.setTimeout(poll, 1_200 * Math.min(state.failures + 1, 4))
    }
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

// 弹窗必须在用户手势所在的那一个任务里打开，任何 await 之后再开都会被拦截。
function openAuthorizationPopup(): Window | null {
  return window.open(
    'about:blank',
    `gpt-load-codex-oauth-${Date.now()}`,
    'popup,width=720,height=820,resizable=yes,scrollbars=yes',
  )
}

async function beginAuthorization(existingPopup?: Window | null): Promise<void> {
  if (props.disabled || busyAction.value || !canAdd.value) {
    existingPopup?.close()
    return
  }
  feedbackKey.value = ''
  const popup = existingPopup === undefined ? openAuthorizationPopup() : existingPopup
  busyAction.value = 'authorize'
  try {
    const stage = await beginCredentialAuthorization(client, props.channelId)
    replaceStage(stage)
    schedulePoll(stage)
    if (popup && stage.authorization_url) popup.location.replace(stage.authorization_url)
    else popupBlockedStages.value = { ...popupBlockedStages.value, [stage.stage_id]: true }
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
  await importOAuthJSON(
    new File([value], `${props.channelId}-credential.json`, { type: 'application/json' }),
  )
}

async function importOAuthJSON(file: File): Promise<void> {
  if (props.disabled || busyAction.value || !canAdd.value) return
  feedbackKey.value = ''
  busyAction.value = 'import'
  try {
    replaceStage(await importCredentialStage(client, props.channelId, file))
    oauthJSON.value = ''
    jsonImportOpen.value = false
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

async function handleCallbackPaste(stage: CredentialStage): Promise<void> {
  await nextTick()
  if (callbackURLs.value[stage.stage_id]?.trim()) await submitCallback(stage)
}

// 授权会话的剩余时间用 m:ss 倒计时，而不是「10 分钟后」这类相对措辞——
// 用户在这一步是在等一个明确的截止点，秒级读数才有参考价值。
function remainingCountdown(stage: CredentialStage): string {
  const remainingSeconds = Math.max(0, Math.floor((stage.expires_at_ms - nowMS.value) / 1_000))
  const minutes = Math.floor(remainingSeconds / 60)
  const seconds = remainingSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

async function restartAuthorization(stage: CredentialStage): Promise<void> {
  if (props.disabled || busyAction.value) return
  const popup = openAuthorizationPopup()
  await removeStage(stage)
  // 等父组件把新的 modelValue 回灌进来，否则 single 模式下 canAdd 还是旧值。
  await nextTick()
  await beginAuthorization(popup)
}

async function removeStage(stage: CredentialStage): Promise<void> {
  if (props.disabled) return
  feedbackKey.value = ''
  if (stage.status === 'pending_authorization' || stage.status === 'ready') {
    try {
      await cancelCredentialStage(client, stage.stage_id)
    } catch (cause) {
      // 取消失败也要把卡片摘掉：暂存自己会到期，留一张点不动的卡片只会把
      // 用户困在这一步。这里只作为提示，不阻断移除。
      feedbackKey.value = presentSubscriptionErrorKey(cause, 'import.subscription.cancelFailed')
    }
  }
  stopPolling(stage.stage_id)
  stopExpiryTimer(stage.stage_id)
  clearCallbackURL(stage.stage_id)
  clearCallbackError(stage.stage_id)
  clearStageFlags(stage.stage_id)
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

function isAwaiting(stage: CredentialStage): boolean {
  return stage.status === 'pending_authorization' || stage.status === 'exchanging'
}

// 这些状态下暂存已经没救了，卡片必须自带「重新授权」出口，
// 否则用户只剩「移除」一个动作，得自己想到再点一次登录。
function isRecoverable(stage: CredentialStage): boolean {
  return (
    stage.status === 'failed' ||
    stage.status === 'cancelled' ||
    stage.status === 'expired' ||
    stage.status === 'outcome_unknown'
  )
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

function stageErrorMessage(stage: CredentialStage): string {
  if (stage.error_code) return t(stageErrorKey(stage.error_code))
  if (stage.status === 'expired') return t('import.subscription.stageError.expired')
  if (stage.status === 'cancelled') return t('import.subscription.stageError.cancelled')
  return t('import.subscription.stageError.unknown')
}

onMounted(() => {
  countdownTimer = window.setInterval(() => {
    nowMS.value = Date.now()
  }, 1_000)
})

onBeforeUnmount(() => {
  for (const stageID of [...polling.keys()]) stopPolling(stageID)
  for (const stageID of [...expiryTimers.keys()]) stopExpiryTimer(stageID)
  if (countdownTimer !== undefined) window.clearInterval(countdownTimer)
  oauthJSON.value = ''
  callbackURLs.value = {}
  callbackErrorKeys.value = {}
  popupBlockedStages.value = {}
})
</script>

<template>
  <section
    class="subscription-stager"
    :class="{ 'subscription-stager--compact': compact }"
    :aria-labelledby="hideHeader ? undefined : 'subscription-stager-title'"
    :aria-label="hideHeader ? t('import.subscription.title') : undefined"
  >
    <PanelHeader
      v-if="!hideHeader"
      heading-id="subscription-stager-title"
      :step="step"
      :title="t('import.subscription.title')"
      :description="t('import.subscription.description')"
    >
      <template v-if="readyCount" #actions>
        <span class="subscription-stager__count">
          {{ t('import.subscription.readyCount', { count: readyCount }) }}
        </span>
      </template>
    </PanelHeader>

    <div v-if="hasAccounts" class="subscription-stager__accounts">
      <article
        v-for="stage in modelValue"
        :key="stage.stage_id"
        class="subscription-stager__account"
        :class="`subscription-stager__account--${statusTone(stage)}`"
      >
        <!-- 等待授权时主行就是等待态本身；再叠一行「等待账号信息」只是重复。 -->
        <div
          v-if="isAwaiting(stage)"
          class="subscription-stager__summary subscription-stager__summary--awaiting"
          role="status"
        >
          <span class="subscription-stager__spinner" aria-hidden="true"></span>
          <div class="subscription-stager__identity">
            <strong>
              {{
                stage.status === 'exchanging'
                  ? t('import.subscription.exchanging')
                  : t('import.subscription.waiting')
              }}
            </strong>
            <span>{{ t('import.subscription.waitingHelp') }}</span>
          </div>
          <span
            v-if="stage.status === 'pending_authorization'"
            class="subscription-stager__countdown"
            :aria-label="t('import.subscription.sessionRemaining')"
          >
            {{ remainingCountdown(stage) }}
          </span>
          <AppButton
            variant="ghost"
            size="compact"
            :disabled="disabled || stage.status === 'exchanging'"
            @click="removeStage(stage)"
          >
            {{ t('common.cancel') }}
          </AppButton>
        </div>

        <div v-else class="subscription-stager__summary">
          <div class="subscription-stager__identity">
            <strong>{{
              stage.account.email_mask || t('import.subscription.pendingAccount')
            }}</strong>
            <span v-if="stage.status === 'ready'">
              {{ t(`import.subscription.readyNotice.${context}`) }} ·
              {{ t('import.subscription.expires') }}
              <AppRelativeTime
                :instant="stage.expires_at_ms"
                :locale="locale"
                :empty-label="t('import.subscription.unknown')"
                hint
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
            :disabled="disabled"
            @click="removeStage(stage)"
          >
            {{ t('import.subscription.remove') }}
          </AppButton>
        </div>

        <div v-if="isRecoverable(stage)" class="subscription-stager__recover" role="alert">
          <span>{{ stageErrorMessage(stage) }}</span>
          <AppButton
            size="compact"
            :disabled="disabled || Boolean(busyAction)"
            :busy="busyAction === 'authorize'"
            @click="restartAuthorization(stage)"
          >
            {{ t('import.subscription.restart') }}
          </AppButton>
        </div>

        <!-- 远程部署时浏览器到不了服务端的 localhost:1455，手动授权是常规路径而
             不是异常兜底，因此授权链接与回调输入始终展开，不做折叠。 -->
        <div
          v-if="stage.status === 'pending_authorization' && stage.authorization_url"
          class="subscription-stager__authorization"
        >
          <InlineFeedback
            v-if="popupBlockedStages[stage.stage_id]"
            tone="warning"
            appearance="ledger"
          >
            {{ t('import.subscription.popupBlocked') }}
          </InlineFeedback>
          <p class="subscription-stager__manual-hint">
            {{ t('import.subscription.manualHint') }}
          </p>

          <div class="subscription-stager__link-field">
            <span class="subscription-stager__field-label">
              {{ t('import.subscription.authorizationLink') }}
            </span>
            <div class="subscription-stager__authorization-link">
              <code>{{ stage.authorization_url }}</code>
              <CopyChip
                layout="icon"
                :value="stage.authorization_url"
                :label="t('import.subscription.copyAuthorization')"
                :success-label="t('common.copied')"
                :failure-label="t('common.copyFailed')"
              />
              <a :href="stage.authorization_url" target="_blank" rel="noopener noreferrer">
                <ExternalLink :size="15" aria-hidden="true" />
                <span>{{ t('import.subscription.openAuthorization') }}</span>
              </a>
            </div>
          </div>

          <form class="subscription-stager__callback" @submit.prevent="submitCallback(stage)">
            <FormField
              :id="`oauth-callback-${stage.stage_id}`"
              :label="t('import.subscription.callbackLabel')"
              :description="t('import.subscription.callbackHelp')"
              :error="
                callbackErrorKeys[stage.stage_id] ? t(callbackErrorKeys[stage.stage_id]) : undefined
              "
              size="compact"
            >
              <template #default="field">
                <input
                  :id="`oauth-callback-${stage.stage_id}`"
                  v-model="callbackURLs[stage.stage_id]"
                  type="url"
                  autocomplete="off"
                  autocapitalize="none"
                  spellcheck="false"
                  :disabled="disabled || Boolean(busyAction)"
                  :placeholder="t('import.subscription.callbackPlaceholder')"
                  :aria-invalid="field.invalid || undefined"
                  :aria-describedby="field.describedBy"
                  @paste="handleCallbackPaste(stage)"
                />
              </template>
            </FormField>
            <AppButton
              type="submit"
              variant="secondary"
              size="sm"
              :busy="busyAction === `callback:${stage.stage_id}`"
              :disabled="disabled || Boolean(busyAction) || !callbackURLs[stage.stage_id]?.trim()"
            >
              <Send :size="15" aria-hidden="true" />{{ t('import.subscription.submitCallback') }}
            </AppButton>
          </form>

          <AppButton
            class="subscription-stager__restart"
            variant="link"
            size="inline"
            :disabled="disabled || Boolean(busyAction)"
            @click="restartAuthorization(stage)"
          >
            {{ t('import.subscription.restart') }}
          </AppButton>
        </div>
      </article>
    </div>

    <div v-if="canAdd" class="subscription-stager__entry">
      <div class="subscription-stager__entry-primary">
        <AppButton
          class="subscription-stager__primary"
          :variant="hasAccounts ? 'secondary' : 'primary'"
          :size="hasAccounts ? 'sm' : 'lg'"
          :busy="busyAction === 'authorize'"
          :disabled="entryBusy"
          @click="beginAuthorization()"
        >
          <Plus v-if="hasAccounts" :size="15" aria-hidden="true" />
          {{
            hasAccounts ? t('import.subscription.addAnother') : t('import.subscription.authorize')
          }}
        </AppButton>
        <span v-if="!hasAccounts" class="subscription-stager__entry-hint">
          {{ t('import.subscription.authorizeHint') }}
        </span>
      </div>

      <DisclosurePanel
        :summary="t('import.subscription.orImport')"
        :open="jsonImportOpen"
        @update:open="jsonImportOpen = $event"
      >
        <div class="subscription-stager__json">
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
                :disabled="entryBusy"
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
              :disabled="entryBusy || !oauthJSON.trim()"
              @click="importText"
            >
              <FileJson :size="16" aria-hidden="true" />{{ t('import.subscription.importText') }}
            </AppButton>
            <label class="subscription-stager__file" :class="{ 'is-disabled': entryBusy }">
              <FileJson :size="16" aria-hidden="true" />
              {{
                busyAction === 'import'
                  ? t('import.subscription.importing')
                  : t('import.subscription.importFile')
              }}
              <input
                type="file"
                accept="application/json,.json"
                :disabled="entryBusy"
                @change="importFile"
              />
            </label>
          </div>
        </div>
      </DisclosurePanel>
    </div>

    <!-- 安全说明只在还没连上账号时引导；已有账号后它就只是噪音 -->
    <InlineFeedback v-if="!hasAccounts" tone="neutral" appearance="ledger-hint" glyph="i">
      {{ t(`import.subscription.securityNotice.${context}`) }}
    </InlineFeedback>

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
/* compact 用于抽屉内部：外层容器已提供内边距与边界，这里不再叠加 */
.subscription-stager--compact {
  border-bottom: 0;
  padding: 0;
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
  gap: var(--space-2);
  min-width: 0;
}
.subscription-stager__account {
  display: grid;
  gap: 10px;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-left: 3px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-3) var(--space-3-5);
}
.subscription-stager__account--success {
  border-left-color: var(--color-success);
}
.subscription-stager__account--warning {
  border-left-color: var(--color-warning);
}
.subscription-stager__account--danger {
  border-left-color: var(--color-danger);
}
.subscription-stager__summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}
.subscription-stager__summary--awaiting {
  grid-template-columns: auto minmax(0, 1fr) auto auto;
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
/* 等待态的主行是一句话而不是账号地址，用正文字体读起来更自然 */
.subscription-stager__summary--awaiting .subscription-stager__identity strong {
  font-family: inherit;
  font-size: var(--text-body);
  font-weight: 620;
}
.subscription-stager__identity span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-stager__countdown {
  flex: none;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
  font-weight: 620;
}
.subscription-stager__recover {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-2) var(--space-3);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  padding: 8px 10px;
  color: var(--color-text);
  font-size: var(--text-meta);
}
.subscription-stager__recover > span {
  min-width: 0;
  flex: 1 1 auto;
}
.subscription-stager__restart {
  justify-self: start;
  font-size: var(--text-sm);
  font-weight: 560;
}
.subscription-stager__spinner {
  width: 14px;
  height: 14px;
  flex: none;
  border: 2px solid var(--color-border-control);
  border-top-color: var(--color-action);
  border-radius: 50%;
  animation: subscription-stager-spin 0.9s linear infinite;
}
@keyframes subscription-stager-spin {
  to {
    transform: rotate(360deg);
  }
}
.subscription-stager__authorization {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.subscription-stager__manual-hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.6;
}
.subscription-stager__link-field {
  display: grid;
  gap: 5px;
  min-width: 0;
}
.subscription-stager__field-label {
  display: block;
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
  height: var(--control-compact);
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
  align-items: start;
  gap: var(--space-2);
  min-width: 0;
}
.subscription-stager__callback :deep(input) {
  font-family: var(--font-mono);
}
/* 提交按钮与输入框顶端对齐：label 占一行，按钮下移同样的高度 */
.subscription-stager__callback :deep(.app-button) {
  margin-top: 22px;
}
.subscription-stager__entry {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}
.subscription-stager__entry-primary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
}
.subscription-stager__primary {
  flex: none;
}
.subscription-stager__entry-hint {
  min-width: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-stager__json {
  display: grid;
  gap: var(--space-3);
}
.subscription-stager__json textarea {
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
  outline: 2px solid var(--color-focus);
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
  .subscription-stager__account {
    padding: var(--space-2-5);
  }
  .subscription-stager__summary {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .subscription-stager__summary--awaiting {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }
  .subscription-stager__summary :deep(.app-button) {
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
  .subscription-stager__primary,
  .subscription-stager__import-actions :deep(.app-button),
  .subscription-stager__file {
    width: 100%;
    min-height: var(--touch-target);
  }
}
@media (prefers-reduced-motion: reduce) {
  .subscription-stager__spinner {
    animation: none;
  }
}
</style>
