<script setup lang="ts">
import { Layers3, Zap } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type { HomeBaseDto } from '@/app/resources/home'
import { accessKeysLocation } from '@/app/route-locations'
import { revealAccessKey } from '@/app/resources/access-keys'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CodeBlock from '@/components/ui/CodeBlock.vue'
import CopyAction from '@/components/ui/CopyAction.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'

import {
  ccSwitchTargets,
  clientConfiguration,
  clientQuickImportURL,
  clientRequiredProtocol,
  gatewayClients,
  type CCSwitchTargetID,
  type GatewayClientID,
} from './gateway-clients'

const props = defineProps<{
  accessKeys: HomeBaseDto['access_keys']
  selectedAccessKeyId: number | null
  clientId: GatewayClientID
  credential?: string
  selfScoped?: boolean
}>()
const emit = defineEmits<{
  'update:selectedAccessKeyId': [id: number]
  'update:clientId': [id: GatewayClientID]
}>()

type ActionTarget = 'key' | 'configuration' | 'quick-import'
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
const selectedKeyID = computed(() => props.selectedAccessKeyId)
const activeClient = computed(() => props.clientId)
const feedback = ref<ActionFeedback | null>(null)
const actionBusy = ref(false)
const quickImportConfirmationOpen = ref(false)
const ccSwitchTargetID = ref<CCSwitchTargetID>('claude')
const ccSwitchModel = ref('')
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
const currentCCSwitchTarget = computed(
  () =>
    ccSwitchTargets.find((candidate) => candidate.id === ccSwitchTargetID.value) ??
    ccSwitchTargets[0]!,
)
const currentRequiredProtocol = computed(() =>
  clientRequiredProtocol(currentClient.value, currentCCSwitchTarget.value),
)
const selectedKeySupportsClient = computed(() => {
  const requiredProtocol = currentRequiredProtocol.value
  return Boolean(!requiredProtocol || selectedKey.value?.protocols.includes(requiredProtocol))
})
const ccSwitchTargetOptions = computed<SegmentedControlOption[]>(() =>
  ccSwitchTargets.map((target) => ({
    value: target.id,
    label: t(`home.ledger.connection.ccSwitchTargets.${target.id}`),
    disabled: actionBusy.value || !selectedKey.value?.protocols.includes(target.requiredProtocol),
  })),
)
const quickImportAvailable = computed(() => Boolean(currentClient.value.quickImport))
const quickImportRequiresModel = computed(
  () => activeClient.value === 'cc-switch' && currentCCSwitchTarget.value.requiresModel,
)
const quickImportReady = computed(
  () =>
    quickImportAvailable.value &&
    selectedKeySupportsClient.value &&
    (!quickImportRequiresModel.value || Boolean(ccSwitchModel.value.trim())),
)
const maskedSnippet = computed(() => {
  const key = selectedKey.value
  if (!key) return ''
  return clientConfiguration(
    activeClient.value,
    origin,
    key.masked_key,
    ccSwitchTargetID.value,
    ccSwitchModel.value,
    `GPT-Load · ${key.name}`,
  )
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
  if (current.kind === 'success') {
    return t('home.ledger.connection.quickImportRequested', {
      client: quickImportClientLabel.value,
    })
  }
  if (current.kind === 'popup-blocked') {
    return t('home.ledger.connection.popupBlocked', { client: quickImportClientLabel.value })
  }
  return t('home.ledger.connection.quickImportFailed', { client: quickImportClientLabel.value })
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

function selectedClientKind(clientID: GatewayClientID): string {
  const kind = gatewayClients.find((candidate) => candidate.id === clientID)?.kind
  return kind ? t(`home.ledger.connection.clientKinds.${kind}`) : ''
}

const quickImportClientLabel = computed(() => {
  const clientLabel = selectedClientLabel(activeClient.value)
  if (activeClient.value !== 'cc-switch') return clientLabel
  return `${clientLabel} · ${t(`home.ledger.connection.ccSwitchTargets.${ccSwitchTargetID.value}`)}`
})

const configurationLanguage = computed(() => {
  if (activeClient.value === 'new-api') return t('home.ledger.connection.connectionInfo')
  if (activeClient.value === 'cc-switch') return t('home.ledger.connection.importParameters')
  return t('home.ledger.connection.configuration')
})

const copyConfigurationLabel = computed(() => {
  if (activeClient.value === 'new-api') return t('home.ledger.connection.copyConnectionInfo')
  if (activeClient.value === 'cc-switch') return t('home.ledger.connection.copyImportParameters')
  return t('home.ledger.connection.copyConfiguration')
})

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
    quickImportConfirmationOpen.value = false
  },
  { immediate: true },
)

watch(selectedKeyID, (value, previous) => {
  if (value === previous) return
  invalidateSensitiveAction()
  quickImportConfirmationOpen.value = false
  ccSwitchModel.value = ''
  selectFirstSupportedCCSwitchTarget()
})

watch(activeClient, (value, previous) => {
  if (value === previous) return
  invalidateSensitiveAction()
  quickImportConfirmationOpen.value = false
})

watch(
  () => selectedKey.value?.protocols,
  () => selectFirstSupportedCCSwitchTarget(),
  { immediate: true },
)

function selectKey(value: string): void {
  if (actionBusy.value) return
  const id = Number(value)
  if (Number.isSafeInteger(id) && props.accessKeys.some((accessKey) => accessKey.id === id)) {
    emit('update:selectedAccessKeyId', id)
  }
}

function selectClient(value: string): void {
  if (actionBusy.value) return
  const gatewayClient = gatewayClients.find((candidate) => candidate.id === value)
  if (gatewayClient) emit('update:clientId', gatewayClient.id)
}

function selectCCSwitchTarget(value: string): void {
  if (actionBusy.value) return
  const target = ccSwitchTargets.find((candidate) => candidate.id === value)
  if (!target || !selectedKey.value?.protocols.includes(target.requiredProtocol)) return
  invalidateSensitiveAction()
  quickImportConfirmationOpen.value = false
  ccSwitchTargetID.value = target.id
  ccSwitchModel.value = ''
}

function selectFirstSupportedCCSwitchTarget(): void {
  const protocols = selectedKey.value?.protocols ?? []
  if (protocols.includes(currentCCSwitchTarget.value.requiredProtocol)) return
  const supported = ccSwitchTargets.find((target) => protocols.includes(target.requiredProtocol))
  if (supported) ccSwitchTargetID.value = supported.id
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
    secret = props.selfScoped
      ? props.credential
      : (await revealAccessKey(client, identity.accessKeyID, controller.signal)).key
    if (!secret) throw new Error('ACCESS_KEY_UNAVAILABLE')
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
  if (!selectedKeySupportsClient.value) return

  if (clientID === 'codex') {
    try {
      await navigator.clipboard.writeText(maskedSnippet.value)
      setImmediateFeedback('configuration', 'success')
    } catch {
      setImmediateFeedback('configuration', 'failure')
    }
    return
  }

  await withRevealedKey('configuration', clientID, async (key, isCurrent) => {
    if (!isCurrent()) return
    let configuration: string | undefined
    try {
      configuration = clientConfiguration(
        clientID,
        origin,
        key,
        ccSwitchTargetID.value,
        ccSwitchModel.value,
        `GPT-Load · ${selectedKey.value?.name ?? ''}`,
      )
      if (!isCurrent()) return
      await navigator.clipboard.writeText(configuration)
    } finally {
      configuration = undefined
    }
  })
}

async function openQuickImport(): Promise<void> {
  const clientID = activeClient.value
  if (!quickImportReady.value || actionBusy.value) return

  const popup = window.open('about:blank', '_blank')
  if (!popup) {
    setImmediateFeedback('quick-import', 'popup-blocked')
    return
  }
  try {
    popup.opener = null
  } catch {
    popup.close()
    setImmediateFeedback('quick-import', 'failure')
    return
  }
  quickImportConfirmationOpen.value = false

  let target: string | undefined
  const opened = await withRevealedKey('quick-import', clientID, (key, isCurrent) => {
    if (!isCurrent()) return
    target =
      clientQuickImportURL(
        clientID,
        origin,
        key,
        ccSwitchTargetID.value,
        ccSwitchModel.value,
        `GPT-Load · ${selectedKey.value?.name ?? ''}`,
      ) ?? undefined
    if (!target) throw new Error('QUICK_IMPORT_UNAVAILABLE')
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
      <Layers3 :size="15" aria-hidden="true" />
      <h2 id="gateway-connection-title">{{ t('home.ledger.connection.title') }}</h2>
    </div>

    <div v-if="!selectedKey" class="gateway-connection__empty">
      <p>{{ t('home.ledger.connection.noAccessKey') }}</p>
      <RouterLink v-if="!selfScoped" class="button-link" :to="accessKeysLocation()">
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
              :disabled="actionBusy || selfScoped"
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
            appearance="pills"
            size="compact"
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
          <strong>
            {{ selectedClientLabel(activeClient) }}
            <span v-if="selectedClientKind(activeClient)">
              {{ selectedClientKind(activeClient) }}
            </span>
          </strong>
          <AppButton
            v-if="quickImportAvailable"
            size="compact"
            :disabled="!quickImportReady || actionBusy"
            :busy="actionBusy"
            @click="quickImportConfirmationOpen = true"
          >
            <Zap :size="14" aria-hidden="true" />
            {{
              t(
                activeClient === 'cc-switch'
                  ? 'home.ledger.connection.importAndEnable'
                  : 'home.ledger.connection.quickImport',
              )
            }}
          </AppButton>
        </header>

        <div class="gateway-connection__panel-body">
          <div v-if="activeClient === 'cc-switch'" class="gateway-connection__cc-switch-options">
            <div class="gateway-connection__cc-switch-target">
              <span class="gateway-connection__label">
                {{ t('home.ledger.connection.targetApplication') }}
              </span>
              <SegmentedControl
                :model-value="ccSwitchTargetID"
                :label="t('home.ledger.connection.targetApplication')"
                :options="ccSwitchTargetOptions"
                appearance="joined"
                size="touch"
                scrollable
                @update:model-value="selectCCSwitchTarget"
              />
            </div>

            <div class="gateway-connection__cc-switch-model">
              <span class="gateway-connection__label">
                {{ t('home.ledger.connection.primaryModel') }}
                <span v-if="quickImportRequiresModel">
                  · {{ t('home.ledger.connection.required') }}
                </span>
              </span>
              <AppTextInput
                id="cc-switch-primary-model"
                v-model="ccSwitchModel"
                :label="t('home.ledger.connection.primaryModel')"
                :placeholder="t('home.ledger.connection.modelPlaceholder')"
                :disabled="actionBusy"
                :maxlength="200"
                :spellcheck="false"
                monospace
                size="touch"
              />
            </div>
          </div>

          <InlineFeedback v-if="!selectedKeySupportsClient" tone="warning" appearance="hint">
            {{
              t('home.ledger.connection.protocolUnavailable', {
                client: quickImportClientLabel,
                protocol: currentRequiredProtocol,
              })
            }}
          </InlineFeedback>

          <CodeBlock :code="maskedSnippet" :language="configurationLanguage" appearance="snippet">
            <template #action>
              <CopyAction
                :label="copyConfigurationLabel"
                :disabled="
                  !selectedKeySupportsClient ||
                  actionBusy ||
                  (quickImportRequiresModel && !ccSwitchModel.trim())
                "
                :busy="actionBusy"
                :state="configurationCopyState"
                @copy="copyClientConfiguration"
              />
            </template>
          </CodeBlock>

          <InlineFeedback
            v-if="quickImportRequiresModel && !ccSwitchModel.trim()"
            tone="warning"
            appearance="hint"
          >
            {{ t('home.ledger.connection.ccSwitchModelRequired') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'cc-switch'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.ccSwitchHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'new-api'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.newApiHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'codex'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.codexHint') }}
          </InlineFeedback>
          <InlineFeedback
            v-else-if="activeClient === 'cherry-studio'"
            tone="info"
            appearance="hint"
          >
            {{ t('home.ledger.connection.cherryStudioHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'nextchat'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.disableFastLink') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'claude-code'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.claudeCodeHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'open-webui'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.openWebUIHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'cline'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.clineHint') }}
          </InlineFeedback>
          <InlineFeedback v-else-if="activeClient === 'curl'" tone="info" appearance="hint">
            {{ t('home.ledger.connection.curlHint') }}
          </InlineFeedback>
        </div>
      </div>
    </template>

    <InlineFeedback
      v-if="visibleFeedback"
      class="gateway-connection__feedback"
      :tone="feedbackTone"
      appearance="toast"
    >
      {{ feedbackMessage }}
    </InlineFeedback>

    <AppConfirmDialog
      v-model:open="quickImportConfirmationOpen"
      :title="
        t('home.ledger.connection.quickImportConfirmTitle', { client: quickImportClientLabel })
      "
      :description="
        t('home.ledger.connection.quickImportConfirmDescription', {
          client: quickImportClientLabel,
        })
      "
      :close-label="t('common.close')"
      :cancel-label="t('common.cancel')"
      :confirm-label="
        t(
          activeClient === 'cc-switch'
            ? 'home.ledger.connection.importAndEnable'
            : 'home.ledger.connection.openAndImport',
        )
      "
      :pending="actionBusy"
      @confirm="openQuickImport"
    />
  </section>
</template>

<style scoped>
.gateway-connection {
  padding-top: 22px;
}

.gateway-connection__heading {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
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
  gap: 20px;
  margin-top: 18px;
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
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
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
  min-height: var(--surface-header-min-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 12px 14px;
}

.gateway-connection__panel-header strong {
  display: inline-flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
  font-size: var(--text-body);
  font-weight: 600;
}

.gateway-connection__panel-header strong span {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-weight: 400;
}

.gateway-connection__panel-body {
  display: grid;
  gap: 10px;
  padding: 14px;
}

.gateway-connection__cc-switch-options {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(220px, 0.7fr);
  align-items: end;
  gap: var(--space-3);
}

.gateway-connection__cc-switch-target,
.gateway-connection__cc-switch-model {
  display: grid;
  min-width: 0;
  gap: 6px;
}

.gateway-connection__cc-switch-target :deep(.segmented-control) {
  width: 100%;
}

.gateway-connection__cc-switch-target :deep(.segmented-control__list) {
  width: 100%;
}

.gateway-connection__cc-switch-target :deep(.segmented-control__trigger) {
  flex: 1 0 auto;
}

.gateway-connection__feedback {
  margin: 0;
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

@media (max-width: 860px) {
  .gateway-connection__toolbar {
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .gateway-connection__clients {
    justify-content: flex-start;
  }

  .gateway-connection__cc-switch-options {
    grid-template-columns: 1fr;
  }
}
</style>
