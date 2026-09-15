<script setup lang="ts">
import { ArrowUpRight, Copy } from '@lucide/vue'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeKey } from '@modern/api/home'
import { getModels } from '@modern/api/models'
import { revealAccessKey } from '@modern/api/access-keys'
import { useURLState, positivePage } from '@modern/app/url-state'
import { useMessages } from '@modern/app/messages'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { useApiClient } from '@shared/http/client-context'
import { RequestCancelledError } from '@shared/http/errors'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppPanel,
  AppProtocolTag,
  AppSearchSelect,
  AppSelect,
} from '@modern/components/ui'
import {
  gatewayClients,
  gatewayTargets,
  gatewayConfiguration,
  gatewayEndpoint,
  gatewayImportURL,
  type GatewayConfig,
} from './gateway-config'

const props = defineProps<{ keys: HomeKey[]; admin: boolean }>()
const { t } = useI18n()
const client = useApiClient()
const session = useAuthSession()
const messages = useMessages()
const state = useURLState(
  ['client', 'access_key_id'],
  (query) => ({
    client: gatewayClients.find((client) => client.id === query.client)?.id ?? 'codex',
    id: props.admin ? positivePage(query.access_key_id, 0) : 0,
  }),
  (value) => ({
    client: value.client === 'codex' ? undefined : value.client,
    access_key_id: props.admin && value.id ? String(value.id) : undefined,
  }),
)
const key = computed(() => props.keys.find((key) => key.id === state.value.id) ?? props.keys[0])
const selectedKey = computed({
  get: () => String(key.value?.id ?? ''),
  set: (value: string) => {
    state.value = { ...state.value, id: Number(value) }
  },
})
const selectedClient = computed(() =>
  gatewayClients.find((client) => client.id === state.value.client)!,
)
const target = ref<GatewayConfig['target']>('claude')
const model = ref('')
const selectedTarget = computed(() => gatewayTargets.find((item) => item.id === target.value)!)
const requiredProtocol = computed(() =>
  selectedClient.value.id === 'cc-switch'
    ? selectedTarget.value.protocol
    : selectedClient.value.protocol,
)
const supported = computed(() =>
  Boolean(
    key.value?.protocols.length &&
    (!requiredProtocol.value || key.value.protocols.includes(requiredProtocol.value)),
  ),
)
const config = computed<GatewayConfig>(() => ({
  client: selectedClient.value.id,
  target: target.value,
  origin: window.location.origin,
  model: model.value,
  name: 'GPT-Load' + (key.value ? ' · ' + key.value.name : ''),
}))
const signature = computed(() => JSON.stringify([config.value, key.value?.id, supported.value]))
const preview = computed(() =>
  selectedClient.value.kind === 'snippet'
    ? gatewayConfiguration(config.value, key.value?.mask ?? '')
    : [],
)
const keyOptions = computed(() =>
  props.keys.map((key) => ({ value: String(key.id), label: key.name, description: key.mask })),
)
const clientOptions = gatewayClients.map((client) => ({ value: client.id, label: client.name }))
const targetOptions = gatewayTargets.map((target) => ({ value: target.id, label: target.name }))
const quickImport = computed(() => ['cc-switch', 'cherry-studio'].includes(selectedClient.value.id))
const needsModel = computed(
  () =>
    !['gemini-cli', 'new-api', 'nextchat', 'open-webui', 'cherry-studio'].includes(
      selectedClient.value.id,
    ),
)
const importModelMissing = computed(
  () =>
    selectedClient.value.id === 'cc-switch' &&
    selectedTarget.value.requiresModel &&
    !model.value.trim(),
)
const importOpen = ref(false)
const importing = ref(false)
const importError = ref('')
let controller: AbortController | undefined
function cancel(): void {
  controller?.abort()
  controller = undefined
  importOpen.value = false
  importing.value = false
  importError.value = ''
}
watch(signature, cancel, { flush: 'sync' })
onScopeDispose(cancel)
async function resolveKey(): Promise<string> {
  if (!key.value || !supported.value) throw new RequestCancelledError()
  controller?.abort()
  const request = new AbortController()
  controller = request
  const current = signature.value
  const revision = session.getRevision()
  const secret = props.admin
    ? await revealAccessKey(client, key.value.id, request.signal)
    : session.getAuthKey()
  if (request.signal.aborted || current !== signature.value || revision !== session.getRevision())
    throw new RequestCancelledError()
  return secret
}
const copyKey = () => resolveKey()
const blocks = computed(() =>
  preview.value.map((block, index) => ({
    ...block,
    resolve: async () => {
      const input = { ...config.value }
      const secret = await resolveKey()
      return gatewayConfiguration(input, secret)[index]!.content
    },
  })),
)
async function loadModels(q: string, signal: AbortSignal) {
  const result = await getModels(
    client,
    { q, groups: 'enabled', pricing: 'all', page: 1, pageSize: 100 },
    signal,
  )
  return result.items.map((row) => ({ value: row.name, label: row.name }))
}
async function importClient(): Promise<void> {
  if (importing.value || !supported.value || importModelMissing.value) return
  importing.value = true
  importError.value = ''
  const current = signature.value
  try {
    const input = { ...config.value }
    const secret = await resolveKey()
    if (signature.value !== current) return
    window.location.assign(gatewayImportURL(input, secret))
    importOpen.value = false
    messages.show({ tone: 'success', text: t('home.importRequested') })
  } catch (error) {
    if (!(error instanceof RequestCancelledError) && current === signature.value)
      importError.value = t('home.importFailed')
  } finally {
    if (current === signature.value) importing.value = false
  }
}
</script>

<template>
  <AppPanel :title="t('home.connection')" compact>
    <template #actions
      ><AppButton v-if="admin" as-child size="sm" variant="text"
        ><RouterLink :to="{ name: 'modern-access-keys' }">{{
          t('home.keys')
        }}</RouterLink></AppButton
      ></template
    >
    <AppCollectionState v-if="!key" :title="t('home.noKeys')">
      <p>{{ t('home.noKeyHelp') }}</p>
      <AppButton v-if="admin" as-child variant="primary"
        ><RouterLink :to="{ name: 'modern-access-keys', query: { panel: 'create' } }">{{
          t('home.createKey')
        }}</RouterLink></AppButton
      >
    </AppCollectionState>
    <div v-else class="modern-home-connection">
      <div class="modern-home-connection-controls">
        <AppSearchSelect
          v-if="admin"
          v-model="selectedKey"
          :label="t('home.accessKey')"
          :options="keyOptions"
        />
        <AppSelect v-model="state.client" :label="t('home.client')" :options="clientOptions" />
        <AppSelect
          v-if="state.client === 'cc-switch'"
          v-model="target"
          :label="t('home.target')"
          :options="targetOptions"
        />
        <AppSearchSelect
          v-if="needsModel"
          v-model="model"
          :label="t('home.model')"
          :placeholder="t('home.modelPlaceholder')"
          :load-options="loadModels"
          allow-custom
        />
      </div>
      <div class="modern-home-connection-status">
        <AppProtocolTag v-if="requiredProtocol" :protocol="requiredProtocol" />
        <AppBadge v-if="!supported" tone="warning" size="xs">{{
          t(key.protocols.length ? 'home.unsupported' : 'home.noProtocols')
        }}</AppBadge>
      </div>
      <p class="modern-home-connection-help">{{ t('home.steps.' + state.client) }}</p>
      <dl class="modern-home-connection-values">
        <div>
          <dt>{{ t('home.endpoint') }}</dt>
          <dd><AppCopyValue :value="gatewayEndpoint(config)" /></dd>
        </div>
        <div>
          <dt>{{ t('home.apiKey') }}</dt>
          <dd>
            <AppCopyValue
              v-if="supported"
              :key="signature"
              :value="key.mask"
              :resolve-value="copyKey"
            /><span v-else>{{ key.mask }}</span>
          </dd>
        </div>
      </dl>
      <div v-for="block in blocks" :key="signature + block.label" class="modern-home-code">
        <header>
          <span>{{ block.label === 'shell' ? t('home.shell') : block.label }}</span>
          <AppCopyValue v-if="supported" :value="block.content" :resolve-value="block.resolve">
            <template #trigger="{ copy, pending }"
              ><AppButton
                size="xs"
                variant="ghost"
                :icon="Copy"
                :loading="pending"
                @click="copy()"
                >{{ t('home.copyConfig') }}</AppButton
              ></template
            >
          </AppCopyValue>
        </header>
        <pre tabindex="0">{{ block.content }}</pre>
      </div>
      <div class="modern-home-connection-footer">
        <span v-if="blocks.length && supported">{{ t('home.copyHint') }}</span>
        <AppButton
          v-if="quickImport"
          :icon="ArrowUpRight"
          size="sm"
          :disabled="!supported || importModelMissing"
          @click="importOpen = true"
          >{{ t('home.quickImport') }}</AppButton
        >
        <span v-if="importModelMissing">{{ t('home.selectModel') }}</span>
      </div>
    </div>
  </AppPanel>
  <AppConfirmDialog
    :open="importOpen"
    :title="t('home.importTitle', { client: selectedClient.name })"
    :description="t('home.importHelp')"
    :subject="key?.name"
    :confirm-label="t('home.importConfirm')"
    :pending="importing"
    :error="importError || undefined"
    @confirm="importClient"
    @cancel="importOpen = false"
  />
</template>

<style scoped>
.modern-home-connection {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-home-connection-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--modern-space-3);
}
.modern-home-connection-controls > * {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-home-connection-status,
.modern-home-connection-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-home-connection-status:empty,
.modern-home-connection-footer:empty {
  display: none;
}
.modern-home-connection-help,
.modern-home-connection-footer {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-home-connection-values {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
  font-size: var(--modern-font-size-secondary);
}
.modern-home-connection-values > div {
  display: grid;
  grid-template-columns: 110px minmax(0, 1fr);
  gap: var(--modern-space-3);
  align-items: center;
}
.modern-home-connection-values dt {
  color: var(--modern-muted);
}
.modern-home-connection-values dd {
  min-width: 0;
  margin: 0;
}
.modern-home-code {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-home-code header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-home-code pre {
  overflow: auto;
  margin: 0;
  padding: var(--modern-space-3);
  font-size: var(--modern-font-size-small);
  font-family: var(--modern-font-mono);
  line-height: var(--modern-leading-body);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
}
.modern-home-connection-footer > button {
  margin-inline-start: auto;
}
</style>
