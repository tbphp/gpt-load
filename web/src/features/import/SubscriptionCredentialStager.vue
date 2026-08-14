<script setup lang="ts">
import { ExternalLink, FileJson, Plus, Trash2 } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  beginCredentialAuthorization,
  cancelCredentialStage,
  getCredentialStage,
  importCredentialStage,
  type CredentialStage,
} from '@/app/resources/credential-stages'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    modelValue: CredentialStage[]
    disabled?: boolean
    single?: boolean
    compact?: boolean
  }>(),
  { disabled: false, single: false, compact: false },
)
const emit = defineEmits<{ 'update:modelValue': [stages: CredentialStage[]] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const busyAction = ref<'authorize' | 'import' | ''>('')
const feedbackKey = ref('')
const polling = new Map<string, { timer?: number; controller?: AbortController }>()
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
}

function stopPolling(stageID: string): void {
  const state = polling.get(stageID)
  if (!state) return
  if (state.timer !== undefined) window.clearTimeout(state.timer)
  state.controller?.abort()
  polling.delete(stageID)
}

function shouldPoll(stage: CredentialStage): boolean {
  return stage.status === 'pending_authorization' || stage.status === 'exchanging'
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
    for (const stage of stages) schedulePoll(stage)
    const live = new Set(stages.map(({ stage_id }) => stage_id))
    for (const stageID of polling.keys()) if (!live.has(stageID)) stopPolling(stageID)
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
  } catch {
    popup?.close()
    feedbackKey.value = 'import.subscription.authorizeFailed'
  } finally {
    busyAction.value = ''
  }
}

async function importFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || props.disabled || busyAction.value || !canAdd.value) return
  feedbackKey.value = ''
  busyAction.value = 'import'
  try {
    replaceStage(await importCredentialStage(client, file))
  } catch {
    feedbackKey.value = 'import.subscription.importFailed'
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
    } catch {
      feedbackKey.value = 'import.subscription.cancelFailed'
      return
    }
  }
  stopPolling(stage.stage_id)
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

onBeforeUnmount(() => {
  for (const stageID of [...polling.keys()]) stopPolling(stageID)
})
</script>

<template>
  <section
    class="subscription-stager"
    :class="{ 'subscription-stager--compact': compact }"
    aria-labelledby="subscription-stager-title"
  >
    <header class="subscription-stager__header">
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
        <div class="subscription-stager__identity">
          <strong>{{ stage.account.email_mask || t('import.subscription.pendingAccount') }}</strong>
          <span>
            {{ t('import.subscription.expires') }}
            <AppRelativeTime
              :instant="stage.expires_at_ms"
              :locale="locale"
              :empty-label="t('import.subscription.unknown')"
            />
          </span>
        </div>
        <StatusBadge :tone="statusTone(stage)" size="compact">
          {{ t(`import.subscription.status.${stage.status}`) }}
        </StatusBadge>
        <a
          v-if="stage.status === 'pending_authorization' && stage.authorization_url"
          class="subscription-stager__reopen"
          :href="stage.authorization_url"
          target="_blank"
          rel="noopener noreferrer"
        >
          <ExternalLink :size="14" aria-hidden="true" />{{ t('import.subscription.reopen') }}
        </a>
        <AppButton
          variant="ghost"
          tone="danger"
          size="compact"
          :disabled="disabled || stage.status === 'exchanging'"
          @click="removeStage(stage)"
        >
          <Trash2 :size="14" aria-hidden="true" />{{ t('import.subscription.remove') }}
        </AppButton>
      </article>
    </div>

    <div v-if="canAdd" class="subscription-stager__actions">
      <AppButton
        :busy="busyAction === 'authorize'"
        :disabled="disabled || Boolean(busyAction)"
        @click="beginAuthorization"
      >
        <Plus :size="16" aria-hidden="true" />{{ t('import.subscription.authorize') }}
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
  grid-template-columns: minmax(0, 1fr) auto auto auto;
  align-items: center;
  gap: var(--space-3);
  min-height: 60px;
  background: var(--color-surface);
  padding: 9px 12px;
}
.subscription-stager__account + .subscription-stager__account {
  border-top: 1px solid var(--color-border-subtle);
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
.subscription-stager__reopen {
  display: inline-flex;
  min-height: var(--control-compact);
  align-items: center;
  gap: 5px;
  color: var(--color-action);
  font-size: var(--text-sm);
}
.subscription-stager__actions {
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
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .subscription-stager__reopen,
  .subscription-stager__account :deep(.app-button) {
    min-height: var(--touch-target);
  }
  .subscription-stager__actions :deep(.app-button),
  .subscription-stager__file {
    width: 100%;
    min-height: var(--touch-target);
  }
}
</style>
