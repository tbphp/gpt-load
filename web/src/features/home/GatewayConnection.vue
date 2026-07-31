<script setup lang="ts">
import { Check, Copy, ExternalLink, KeyRound } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type { HomeBaseDto } from '@/app/resources/home'
import { accessKeysLocation } from '@/app/route-locations'
import { revealAccessKey } from '@/app/resources/access-keys'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import AppSelect from '@/components/ui/AppSelect.vue'

import { clientConfiguration, gatewayClients, type GatewayClientID } from './gateway-clients'

const props = defineProps<{
  accessKeys: HomeBaseDto['access_keys']
}>()

const client = useApiClient()
const { t } = useI18n()
const selectedKeyID = ref<number | null>(props.accessKeys[0]?.id ?? null)
const activeClient = ref<GatewayClientID>('nextchat')
type ActionTarget = 'key' | 'configuration' | 'nextchat'
type FeedbackKind = 'success' | 'failure' | 'popup-blocked'
const feedback = ref<{ target: ActionTarget; kind: FeedbackKind } | null>(null)
const actionBusy = ref(false)
const nextChatConfirmationOpen = ref(false)
const clientTabs = ref<HTMLElement | null>(null)
let resetTimer: number | undefined
let actionController: AbortController | undefined
let unmounted = false

const origin = window.location.origin
const selectedKey = computed(
  () => props.accessKeys.find((accessKey) => accessKey.id === selectedKeyID.value) ?? null,
)
const selectOptions = computed(() =>
  props.accessKeys.map((accessKey) => ({ value: String(accessKey.id), label: accessKey.name })),
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

const feedbackMessage = computed(() => {
  const current = feedback.value
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

function setFeedback(target: ActionTarget, kind: FeedbackKind): void {
  if (unmounted) return
  feedback.value = { target, kind }
  window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => {
    if (!unmounted) feedback.value = null
  }, 2_000)
}

function selectedClientLabel(clientID: GatewayClientID): string {
  return t(`home.ledger.connection.clients.${clientID}`)
}

function abortActiveAction(): void {
  actionController?.abort()
  actionController = undefined
  actionBusy.value = false
}

watch(
  () => props.accessKeys.map((accessKey) => accessKey.id),
  (ids) => {
    if (selectedKeyID.value !== null && ids.includes(selectedKeyID.value)) return
    abortActiveAction()
    selectedKeyID.value = ids[0] ?? null
  },
  { immediate: true },
)

watch(selectedKeyID, abortActiveAction)

async function withRevealedKey(
  target: ActionTarget,
  operation: (key: string) => Promise<void> | void,
): Promise<boolean> {
  const accessKey = selectedKey.value
  if (!accessKey || actionBusy.value || unmounted) return false

  const accessKeyID = accessKey.id
  const controller = new AbortController()
  actionController = controller
  let secret: string | undefined
  actionBusy.value = true
  try {
    secret = (await revealAccessKey(client, accessKeyID, controller.signal)).key
    if (controller.signal.aborted || selectedKey.value?.id !== accessKeyID) return false
    await operation(secret)
    if (controller.signal.aborted || selectedKey.value?.id !== accessKeyID) return false
    setFeedback(target, 'success')
    return true
  } catch {
    if (!controller.signal.aborted && selectedKey.value?.id === accessKeyID) {
      setFeedback(target, 'failure')
    }
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
  await withRevealedKey('key', async (key) => navigator.clipboard.writeText(key))
}

async function copyClientConfiguration(): Promise<void> {
  const clientID = activeClient.value
  if (clientID === 'more' || !selectedKeySupportsClient.value) return
  await withRevealedKey('configuration', async (key) => {
    let configuration: string | undefined
    try {
      configuration = clientConfiguration(clientID, origin, key)
      await navigator.clipboard.writeText(configuration)
    } finally {
      configuration = undefined
    }
  })
}

async function openNextChat(): Promise<void> {
  if (!selectedKeySupportsClient.value || actionBusy.value) return
  const popup = window.open('about:blank', '_blank')
  if (!popup) {
    setFeedback('nextchat', 'popup-blocked')
    return
  }
  try {
    popup.opener = null
  } catch {
    popup.close()
    setFeedback('nextchat', 'failure')
    return
  }
  nextChatConfirmationOpen.value = false

  let target: string | undefined
  const opened = await withRevealedKey('nextchat', (key) => {
    target = `https://app.nextchat.club/#/?settings=${encodeURIComponent(
      JSON.stringify({ key, url: origin }),
    )}`
    popup.location.replace(target)
  })
  target = undefined
  if (!opened) popup.close()
}

function handleClientTabKeydown(event: KeyboardEvent): void {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const tabs = [...(clientTabs.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [])]
  const current = event.currentTarget
  if (!(current instanceof HTMLButtonElement)) return
  const currentIndex = tabs.indexOf(current)
  if (currentIndex < 0) return

  let nextIndex = currentIndex
  if (event.key === 'Home') nextIndex = 0
  if (event.key === 'End') nextIndex = tabs.length - 1
  if (event.key === 'ArrowLeft') nextIndex = (currentIndex - 1 + tabs.length) % tabs.length
  if (event.key === 'ArrowRight') nextIndex = (currentIndex + 1) % tabs.length
  const next = tabs[nextIndex]
  const nextID = next?.dataset.clientId as GatewayClientID | undefined
  if (!next || !nextID || nextID === activeClient.value) return
  event.preventDefault()
  activeClient.value = nextID
  void nextTick(() => next.focus())
}

onBeforeUnmount(() => {
  unmounted = true
  abortActiveAction()
  window.clearTimeout(resetTimer)
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
      <RouterLink class="gateway-connection__create" :to="accessKeysLocation()">
        {{ t('home.ledger.connection.createAccessKey') }}
      </RouterLink>
    </div>

    <template v-else>
      <div class="gateway-connection__toolbar">
        <div class="gateway-connection__key">
          <span class="gateway-connection__label">{{ t('home.ledger.connection.accessKey') }}</span>
          <AppSelect
            :model-value="String(selectedKey.id)"
            :label="t('home.ledger.connection.accessKey')"
            :options="selectOptions"
            @update:model-value="selectedKeyID = Number($event)"
          />
          <div class="gateway-connection__masked">
            <code>{{ selectedKey.masked_key }}</code>
            <button
              type="button"
              :disabled="actionBusy"
              :aria-label="t('home.ledger.connection.copyAccessKey')"
              @click="copyAccessKey"
            >
              <Check
                v-if="feedback?.target === 'key' && feedback.kind === 'success'"
                :size="16"
                aria-hidden="true"
              />
              <Copy v-else :size="16" aria-hidden="true" />
            </button>
          </div>
        </div>

        <div class="gateway-connection__clients">
          <span class="gateway-connection__label">{{
            t('home.ledger.connection.clients.label')
          }}</span>
          <div
            ref="clientTabs"
            class="gateway-connection__tabs"
            role="tablist"
            :aria-label="t('home.ledger.connection.clients.label')"
          >
            <button
              v-for="gatewayClient in gatewayClients"
              :id="`gateway-client-tab-${gatewayClient.id}`"
              :key="gatewayClient.id"
              type="button"
              role="tab"
              :aria-selected="activeClient === gatewayClient.id"
              :aria-controls="`gateway-client-panel-${gatewayClient.id}`"
              :tabindex="activeClient === gatewayClient.id ? 0 : -1"
              :data-client-id="gatewayClient.id"
              :class="{ 'gateway-connection__tab--active': activeClient === gatewayClient.id }"
              @click="activeClient = gatewayClient.id"
              @keydown="handleClientTabKeydown"
            >
              {{ selectedClientLabel(gatewayClient.id) }}
            </button>
          </div>
        </div>
      </div>

      <div
        :id="`gateway-client-panel-${activeClient}`"
        class="gateway-connection__panel"
        role="tabpanel"
        :aria-labelledby="`gateway-client-tab-${activeClient}`"
      >
        <template v-if="activeClient === 'more'">
          <p>{{ t('home.ledger.connection.moreDescription') }}</p>
        </template>
        <template v-else>
          <p v-if="!selectedKeySupportsClient" class="gateway-connection__warning" role="status">
            {{
              t('home.ledger.connection.protocolUnavailable', {
                client: selectedClientLabel(activeClient),
                protocol: t(
                  `home.ledger.connection.requiredProtocols.${currentClient.requiredProtocol}`,
                ),
              })
            }}
          </p>
          <div class="gateway-connection__snippet">
            <pre><code>{{ maskedSnippet }}</code></pre>
            <div class="gateway-connection__actions">
              <AppButton
                variant="secondary"
                :disabled="!selectedKeySupportsClient || actionBusy"
                :busy="actionBusy"
                @click="copyClientConfiguration"
              >
                <Check
                  v-if="feedback?.target === 'configuration' && feedback.kind === 'success'"
                  :size="16"
                  aria-hidden="true"
                />
                <Copy v-else :size="16" aria-hidden="true" />
                {{ t('home.ledger.connection.copyConfiguration') }}
              </AppButton>
              <AppButton
                v-if="activeClient === 'nextchat'"
                :disabled="!selectedKeySupportsClient || actionBusy"
                :busy="actionBusy"
                @click="nextChatConfirmationOpen = true"
              >
                <ExternalLink :size="16" aria-hidden="true" />
                {{ t('home.ledger.connection.openNextChat') }}
              </AppButton>
            </div>
          </div>
          <p v-if="activeClient === 'nextchat'" class="gateway-connection__hint">
            {{ t('home.ledger.connection.disableFastLink') }}
          </p>
        </template>
      </div>
    </template>

    <p
      v-if="feedback"
      class="gateway-connection__feedback"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ feedbackMessage }}
    </p>

    <AppDialog
      v-model:open="nextChatConfirmationOpen"
      :title="t('home.ledger.connection.nextChatConfirmTitle')"
      :description="t('home.ledger.connection.nextChatConfirmDescription')"
      :close-label="t('common.close')"
    >
      <div class="gateway-connection__dialog-actions">
        <AppButton variant="secondary" @click="nextChatConfirmationOpen = false">
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
  font-size: var(--text-lg);
  font-weight: 500;
}
.gateway-connection__heading svg {
  color: var(--color-text-faint);
}
.gateway-connection__toolbar {
  display: grid;
  grid-template-columns: minmax(220px, 300px) minmax(0, 1fr);
  gap: var(--space-5);
  margin-top: var(--space-5);
}
.gateway-connection__key,
.gateway-connection__clients {
  display: grid;
  align-content: start;
  gap: var(--space-2);
}
.gateway-connection__label {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.gateway-connection__masked {
  display: flex;
  min-height: var(--control-md);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding-left: var(--space-3);
}
.gateway-connection__masked code,
.gateway-connection__snippet code {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.gateway-connection__masked button {
  display: inline-flex;
  width: var(--touch-target);
  height: var(--touch-target);
  align-items: center;
  justify-content: center;
  align-self: stretch;
  border: 0;
  border-left: 1px solid var(--color-border-control);
  border-radius: 0 var(--radius-control) var(--radius-control) 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.gateway-connection__masked button:hover:not(:disabled) {
  background: var(--color-surface-sunken);
  color: var(--color-action);
}
.gateway-connection__masked button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.gateway-connection__tabs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.gateway-connection__tabs button {
  min-height: var(--control-sm);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 5px 10px;
  font: inherit;
  font-size: var(--text-sm);
  cursor: pointer;
}
.gateway-connection__tabs button:hover,
.gateway-connection__tabs button:focus-visible {
  border-color: var(--color-text-faint);
  color: var(--color-text);
}
.gateway-connection__tab--active {
  border-color: var(--color-text) !important;
  background: var(--color-text) !important;
  color: var(--color-surface) !important;
}
.gateway-connection__panel {
  margin-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.gateway-connection__panel > p {
  margin: 0;
  color: var(--color-text-muted);
}
.gateway-connection__snippet {
  display: grid;
  gap: var(--space-3);
}
.gateway-connection__snippet pre {
  max-width: 100%;
  margin: 0;
  overflow: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-code-bg);
  color: var(--color-code);
  padding: var(--space-4);
  white-space: pre-wrap;
}
.gateway-connection__actions {
  display: flex;
  gap: var(--space-2);
}
.gateway-connection__warning,
.gateway-connection__hint {
  margin: 0 0 var(--space-3) !important;
  font-size: var(--text-sm);
}
.gateway-connection__warning {
  color: var(--color-warning);
}
.gateway-connection__hint {
  margin-top: var(--space-3) !important;
  color: var(--color-text-faint);
}
.gateway-connection__feedback {
  margin: var(--space-3) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
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
.gateway-connection__create {
  color: var(--color-action);
  font-weight: 600;
}
.gateway-connection__dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
@media (max-width: 760px) {
  .gateway-connection__toolbar {
    grid-template-columns: 1fr;
  }
}
</style>
