<script setup lang="ts">
import { ExternalLink, KeyRound } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type { HomeBaseDto } from '@/app/resources/home'
import { accessKeysLocation } from '@/app/route-locations'
import { revealAccessKey } from '@/app/resources/access-keys'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import CodeBlock from '@/components/ui/CodeBlock.vue'
import CopyAction from '@/components/ui/CopyAction.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'

import { clientConfiguration, gatewayClients, type GatewayClientID } from './gateway-clients'

const props = defineProps<{
  accessKeys: HomeBaseDto['access_keys']
}>()

type ActionTarget = 'key' | 'configuration' | 'nextchat'
type FeedbackKind = 'success' | 'failure' | 'popup-blocked'

interface OperationIdentity {
  operationID: number
  accessKeyID: number
  clientID: GatewayClientID
}

interface ActionFeedback extends OperationIdentity {
  target: ActionTarget
  kind: FeedbackKind
}

const gatewayClientPanelID = 'gateway-client-panel'
const gatewayClientTabPrefix = 'gateway-client-tab'
const client = useApiClient()
const { t } = useI18n()
const selectedKeyID = ref<number | null>(props.accessKeys[0]?.id ?? null)
const activeClient = ref<GatewayClientID>('nextchat')
const feedback = ref<ActionFeedback | null>(null)
const actionBusy = ref(false)
const nextChatConfirmationOpen = ref(false)
let resetTimer: number | undefined
let actionController: AbortController | undefined
let operationSequence = 0
let activeOperationID = 0
let unmounted = false

const origin = window.location.origin
const selectedKey = computed(
  () => props.accessKeys.find((accessKey) => accessKey.id === selectedKeyID.value) ?? null,
)
const selectOptions = computed(() =>
  props.accessKeys.map((accessKey) => ({
    value: String(accessKey.id),
    label: `${accessKey.name} · ${accessKey.masked_key}`,
  })),
)
const clientOptions = computed<SegmentedControlOption[]>(() =>
  gatewayClients.map((gatewayClient) => ({
    value: gatewayClient.id,
    label: selectedClientLabel(gatewayClient.id),
    disabled: actionBusy.value,
  })),
)
const currentClient = computed(
  () =>
    gatewayClients.find((candidate) => candidate.id === activeClient.value) ?? gatewayClients[0]!,
)
const selectedKeySupportsClient = computed(() => {
  const requiredProtocol = currentClient.value.requiredProtocol
  return Boolean(!requiredProtocol || selectedKey.value?.protocols.includes(requiredProtocol))
})
const maskedSnippet = computed(() => {
  const key = selectedKey.value
  if (!key || activeClient.value === 'more') return ''
  return clientConfiguration(activeClient.value, origin, key.masked_key)
})
const visibleFeedback = computed(() => {
  const current = feedback.value
  if (
    !current ||
    current.accessKeyID !== selectedKeyID.value ||
    current.clientID !== activeClient.value
  ) {
    return null
  }
  return current
})
const feedbackTone = computed(() =>
  visibleFeedback.value?.kind === 'success' ? 'success' : 'danger',
)
const feedbackMessage = computed(() => {
  const current = visibleFeedback.value
  if (!current) return ''
  if (current.target === 'key') {
    return t(
      current.kind === 'success'
        ? 'home.ledger.connection.keyCopied'
        : 'home.ledger.connection.keyCopyFailed',
    )
  }
  if (current.target === 'configuration') {
    return t(
      current.kind === 'success'
        ? 'home.ledger.connection.configurationCopied'
        : 'home.ledger.connection.configurationCopyFailed',
    )
  }
  if (current.kind === 'success') return t('home.ledger.connection.nextChatOpened')
  if (current.kind === 'popup-blocked') return t('home.ledger.connection.popupBlocked')
  return t('home.ledger.connection.nextChatFailed')
})
const configurationCopyState = computed(() =>
  visibleFeedback.value?.target === 'configuration' && visibleFeedback.value.kind === 'success'
    ? 'success'
    : 'idle',
)
const keyCopyState = computed(() =>
  visibleFeedback.value?.target === 'key' && visibleFeedback.value.kind === 'success'
    ? 'success'
    : 'idle',
)

function selectedClientLabel(clientID: GatewayClientID): string {
  return t(`home.ledger.connection.clients.${clientID}`)
}

function identityMatches(identity: OperationIdentity): boolean {
  return (
    !unmounted &&
    activeOperationID === identity.operationID &&
    selectedKeyID.value === identity.accessKeyID &&
    activeClient.value === identity.clientID
  )
}

function operationIsCurrent(identity: OperationIdentity, controller: AbortController): boolean {
  return identityMatches(identity) && actionController === controller && !controller.signal.aborted
}

function setFeedback(identity: OperationIdentity, target: ActionTarget, kind: FeedbackKind): void {
  if (!identityMatches(identity)) return
  feedback.value = { ...identity, target, kind }
  window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => {
    if (feedback.value?.operationID === identity.operationID) feedback.value = null
  }, 2_000)
}

function setImmediateFeedback(target: ActionTarget, kind: FeedbackKind): void {
  const accessKey = selectedKey.value
  if (!accessKey || unmounted) return
  const identity = {
    operationID: ++operationSequence,
    accessKeyID: accessKey.id,
    clientID: activeClient.value,
  }
  activeOperationID = identity.operationID
  setFeedback(identity, target, kind)
}

function invalidateSensitiveAction(): void {
  activeOperationID = ++operationSequence
  actionController?.abort()
  actionController = undefined
  actionBusy.value = false
  feedback.value = null
  window.clearTimeout(resetTimer)
}

watch(
  () => props.accessKeys.map((accessKey) => accessKey.id),
  (ids) => {
    if (selectedKeyID.value !== null && ids.includes(selectedKeyID.value)) return
    invalidateSensitiveAction()
    nextChatConfirmationOpen.value = false
    selectedKeyID.value = ids[0] ?? null
  },
  { immediate: true },
)

watch(selectedKeyID, (value, previous) => {
  if (value === previous) return
  invalidateSensitiveAction()
  nextChatConfirmationOpen.value = false
})

watch(activeClient, (value, previous) => {
  if (value === previous) return
  invalidateSensitiveAction()
  nextChatConfirmationOpen.value = false
})

function selectKey(value: string): void {
  if (actionBusy.value) return
  const id = Number(value)
  if (Number.isSafeInteger(id) && props.accessKeys.some((accessKey) => accessKey.id === id)) {
    selectedKeyID.value = id
  }
}

function selectClient(value: string): void {
  if (actionBusy.value) return
  const gatewayClient = gatewayClients.find((candidate) => candidate.id === value)
  if (gatewayClient) activeClient.value = gatewayClient.id
}

async function withRevealedKey(
  target: ActionTarget,
  clientID: GatewayClientID,
  operation: (key: string, isCurrent: () => boolean) => Promise<void> | void,
): Promise<boolean> {
  const accessKey = selectedKey.value
  if (!accessKey || actionBusy.value || unmounted || activeClient.value !== clientID) {
    return false
  }

  const identity = {
    operationID: ++operationSequence,
    accessKeyID: accessKey.id,
    clientID,
  }
  const controller = new AbortController()
  activeOperationID = identity.operationID
  actionController = controller
  actionBusy.value = true
  feedback.value = null
  window.clearTimeout(resetTimer)

  let secret: string | undefined
  const isCurrent = () => operationIsCurrent(identity, controller)
  try {
    secret = (await revealAccessKey(client, identity.accessKeyID, controller.signal)).key
    if (!isCurrent()) return false
    await operation(secret, isCurrent)
    if (!isCurrent()) return false
    setFeedback(identity, target, 'success')
    return true
  } catch {
    if (isCurrent()) setFeedback(identity, target, 'failure')
    return false
  } finally {
    secret = undefined
    if (actionController === controller) {
      actionController = undefined
      actionBusy.value = false
    }
  }
}

async function copyAccessKey(): Promise<void> {
  const clientID = activeClient.value
  await withRevealedKey('key', clientID, async (key, isCurrent) => {
    if (!isCurrent()) return
    await navigator.clipboard.writeText(key)
  })
}

async function copyClientConfiguration(): Promise<void> {
  const clientID = activeClient.value
  if (clientID === 'more' || !selectedKeySupportsClient.value) return

  await withRevealedKey('configuration', clientID, async (key, isCurrent) => {
    if (!isCurrent()) return
    let configuration: string | undefined
    try {
      configuration = clientConfiguration(clientID, origin, key)
      if (!isCurrent()) return
      await navigator.clipboard.writeText(configuration)
    } finally {
      configuration = undefined
    }
  })
}

async function openNextChat(): Promise<void> {
  const clientID = activeClient.value
  if (clientID !== 'nextchat' || !selectedKeySupportsClient.value || actionBusy.value) return

  const popup = window.open('about:blank', '_blank')
  if (!popup) {
    setImmediateFeedback('nextchat', 'popup-blocked')
    return
  }
  try {
    popup.opener = null
  } catch {
    popup.close()
    setImmediateFeedback('nextchat', 'failure')
    return
  }
  nextChatConfirmationOpen.value = false

  let target: string | undefined
  const opened = await withRevealedKey('nextchat', clientID, (key, isCurrent) => {
    if (!isCurrent()) return
    target = `https://app.nextchat.club/#/?settings=${encodeURIComponent(
      JSON.stringify({ key, url: origin }),
    )}`
    if (!isCurrent()) return
    popup.location.replace(target)
  })
  target = undefined
  if (!opened) popup.close()
}

onBeforeUnmount(() => {
  unmounted = true
  invalidateSensitiveAction()
})
</script>

<template>
  <section class="gateway-connection" aria-labelledby="gateway-connection-title">
    <div class="gateway-connection__heading">
      <KeyRound :size="16" aria-hidden="true" />
      <h2 id="gateway-connection-title">{{ t('home.ledger.connection.title') }}</h2>
    </div>

    <div v-if="!selectedKey" class="gateway-connection__empty">
      <p>{{ t('home.ledger.connection.noAccessKey') }}</p>
      <RouterLink class="button-link" :to="accessKeysLocation()">
        {{ t('home.ledger.connection.createAccessKey') }}
      </RouterLink>
    </div>

    <template v-else>
      <div class="gateway-connection__toolbar">
        <div class="gateway-connection__key">
          <label class="gateway-connection__label" for="gateway-access-key">
            {{ t('home.ledger.connection.accessKey') }}
          </label>
          <div class="gateway-connection__key-control">
            <AppSelect
              id="gateway-access-key"
              variant="embedded"
              :model-value="String(selectedKey.id)"
              :label="t('home.ledger.connection.accessKey')"
              :options="selectOptions"
              :disabled="actionBusy"
              @update:model-value="selectKey"
            />
            <CopyAction
              variant="embedded"
              :label="t('home.ledger.connection.copyAccessKey')"
              :disabled="actionBusy"
              :busy="actionBusy"
              :state="keyCopyState"
              @copy="copyAccessKey"
            />
          </div>
        </div>

        <div class="gateway-connection__clients">
          <SegmentedControl
            :model-value="activeClient"
            :label="t('home.ledger.connection.clients.label')"
            :options="clientOptions"
            :controls-id="gatewayClientPanelID"
            :id-prefix="gatewayClientTabPrefix"
            scrollable
            @update:model-value="selectClient"
          />
        </div>
      </div>

      <div
        :id="gatewayClientPanelID"
        class="gateway-connection__panel"
        role="tabpanel"
        :aria-labelledby="`${gatewayClientTabPrefix}-${activeClient}`"
      >
        <header class="gateway-connection__panel-header">
          <strong>{{ selectedClientLabel(activeClient) }}</strong>
          <AppButton
            v-if="activeClient === 'nextchat'"
            size="sm"
            :disabled="!selectedKeySupportsClient || actionBusy"
            :busy="actionBusy"
            @click="nextChatConfirmationOpen = true"
          >
            <ExternalLink :size="16" aria-hidden="true" />
            {{ t('home.ledger.connection.openNextChat') }}
          </AppButton>
        </header>

        <div class="gateway-connection__panel-body">
          <template v-if="activeClient === 'more'">
            <p class="gateway-connection__more">
              {{ t('home.ledger.connection.moreDescription') }}
            </p>
          </template>
          <template v-else>
            <InlineFeedback v-if="!selectedKeySupportsClient" tone="warning">
              {{
                t('home.ledger.connection.protocolUnavailable', {
                  client: selectedClientLabel(activeClient),
                  protocol: t(
                    `home.ledger.connection.requiredProtocols.${currentClient.requiredProtocol}`,
                  ),
                })
              }}
            </InlineFeedback>

            <CodeBlock :code="maskedSnippet" :language="selectedClientLabel(activeClient)">
              <template #action>
                <CopyAction
                  :label="t('home.ledger.connection.copyConfiguration')"
                  :disabled="!selectedKeySupportsClient || actionBusy"
                  :busy="actionBusy"
                  :state="configurationCopyState"
                  @copy="copyClientConfiguration"
                />
              </template>
            </CodeBlock>

            <InlineFeedback v-if="activeClient === 'nextchat'" tone="info">
              {{ t('home.ledger.connection.disableFastLink') }}
            </InlineFeedback>
          </template>
        </div>
      </div>
    </template>

    <InlineFeedback
      v-if="visibleFeedback"
      class="gateway-connection__feedback"
      :tone="feedbackTone"
    >
      {{ feedbackMessage }}
    </InlineFeedback>

    <AppDialog
      v-model:open="nextChatConfirmationOpen"
      :title="t('home.ledger.connection.nextChatConfirmTitle')"
      :description="t('home.ledger.connection.nextChatConfirmDescription')"
      :close-label="t('common.close')"
      :dismissible="!actionBusy"
    >
      <div class="gateway-connection__dialog-actions">
        <AppButton
          variant="secondary"
          :disabled="actionBusy"
          @click="nextChatConfirmationOpen = false"
        >
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton :busy="actionBusy" @click="openNextChat">
          {{ t('home.ledger.connection.openNextChat') }}
        </AppButton>
      </div>
    </AppDialog>
  </section>
</template>

<style scoped>
.gateway-connection {
  padding-top: var(--space-6);
}

.gateway-connection__heading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.gateway-connection__heading h2 {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.gateway-connection__heading svg {
  color: var(--color-text-faint);
}

.gateway-connection__toolbar {
  display: grid;
  grid-template-columns: minmax(220px, 300px) minmax(0, 1fr);
  align-items: end;
  gap: var(--space-5);
  margin-top: var(--space-5);
}

.gateway-connection__key {
  display: grid;
  min-width: 0;
  gap: 6px;
}

.gateway-connection__label {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.gateway-connection__key-control {
  display: flex;
  min-width: 0;
  min-height: var(--control-md);
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}

.gateway-connection__key-control :deep(.app-select__trigger) {
  flex: 1;
}

.gateway-connection__clients {
  display: flex;
  min-width: 0;
  justify-content: flex-end;
}

.gateway-connection__panel {
  margin-top: 14px;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-sheet);
  background: var(--color-surface);
}

.gateway-connection__panel-header {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 10px 14px;
}

.gateway-connection__panel-header strong {
  font-size: var(--text-body);
  font-weight: 600;
}

.gateway-connection__panel-body {
  display: grid;
  gap: var(--space-3);
  padding: 14px;
}

.gateway-connection__more {
  margin: 0;
  color: var(--color-text-muted);
}

.gateway-connection__feedback {
  margin-top: var(--space-3);
}

.gateway-connection__empty {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-4);
}

.gateway-connection__empty p {
  margin: 0;
  color: var(--color-text-muted);
}

.gateway-connection__dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

@media (max-width: 860px) {
  .gateway-connection__toolbar {
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .gateway-connection__clients {
    justify-content: flex-start;
  }
}
</style>
