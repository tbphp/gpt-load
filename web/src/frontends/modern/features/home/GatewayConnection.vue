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
import { protocolLabel } from '@modern/i18n/protocols'
import {
  AppButton,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppNotice,
  AppPanel,
  AppSearchSelect,
  AppSelect,
} from '@modern/components/ui'
import {
  gatewayClients,
  gatewayConfiguration,
  gatewayFields,
  gatewayImportURL,
  gatewayMirror,
  gatewayNeedsModel,
  gatewayTargets,
  gatewayTerminal,
  type GatewayClientID,
  type GatewayConfig,
} from './gateway-config'
import ConnectClientList, { type ClientEntry } from './ConnectClientList.vue'
import ConnectMirror from './ConnectMirror.vue'
import ConnectTerminal from './ConnectTerminal.vue'

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
/* 目录里逐个客户端判断能不能用：选之前就看得出来，不用选完再弹一条警告。
   cc-switch 的协议由目标应用决定，这里无法预判，留给选中后的 supported。 */
const clientEntries = computed<ClientEntry[]>(() =>
  gatewayClients.map((entry) => ({
    id: entry.id,
    name: entry.name,
    group: entry.group,
    protocol: entry.protocol,
    supported: Boolean(
      key.value?.protocols.length &&
      (!entry.protocol || key.value.protocols.includes(entry.protocol)),
    ),
  })),
)
const config = computed<GatewayConfig>(() => ({
  client: selectedClient.value.id,
  target: target.value,
  origin: window.location.origin,
  model: model.value,
  name: 'GPT-Load' + (key.value ? ' · ' + key.value.name : ''),
}))
const signature = computed(() => JSON.stringify([config.value, key.value?.id, supported.value]))
const mask = computed(() => key.value?.mask ?? '')
const isTerminal = computed(() => selectedClient.value.surface === 'cli')
const terminalLines = computed(() =>
  isTerminal.value ? gatewayTerminal(config.value, mask.value) : [],
)
const terminalContent = computed(() => terminalLines.value.map((line) => line.text).join('\n'))
const fields = computed(() => (isTerminal.value ? [] : gatewayFields(config.value, mask.value)))
const slotLabel = (slot: string) => t('home.slots.' + slot)
const mirrorRows = computed(() =>
  isTerminal.value ? [] : gatewayMirror(selectedClient.value.id, fields.value, slotLabel),
)
const mirrorValues = computed(() =>
  fields.value.map((field) => ({
    slot: field.slot,
    label: slotLabel(field.slot),
    value: field.value,
    secret: field.slot === 'apiKey',
  })),
)
/* 图形客户端里还要额外粘贴的整段配置（cc-switch 与 new-api 的 JSON）。 */
const extraBlocks = computed(() =>
  isTerminal.value ? [] : gatewayConfiguration(config.value, mask.value),
)
const keyOptions = computed(() =>
  props.keys.map((key) => ({ value: String(key.id), label: key.name, description: key.mask })),
)
const targetOptions = gatewayTargets.map((target) => ({ value: target.id, label: target.name }))
const quickImport = computed(() => ['cc-switch', 'cherry-studio'].includes(selectedClient.value.id))
const needsModel = computed(() => gatewayNeedsModel(selectedClient.value.id))
const missingModel = computed(() => needsModel.value && !model.value.trim())
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
async function resolveTerminal(): Promise<string> {
  const input = { ...config.value }
  const secret = await resolveKey()
  return gatewayTerminal(input, secret)
    .map((line) => line.text)
    .join('\n')
}
const blocks = computed(() =>
  extraBlocks.value.map((block, index) => ({
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
function selectClient(value: GatewayClientID): void {
  state.value = { ...state.value, client: value }
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
  <AppPanel :title="t('home.connection')" :description="t('home.connectionHelp')" compact>
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
    <div v-else class="modern-connect">
      <div class="modern-connect-split">
        <ConnectClientList
          class="modern-connect-aside"
          :clients="clientEntries"
          :selected="selectedClient.id"
          @select="selectClient"
        />
        <div class="modern-connect-main">
          <div class="modern-connect-controls">
            <AppSearchSelect
              v-if="admin"
              v-model="selectedKey"
              :label="t('home.accessKey')"
              :options="keyOptions"
            />
            <AppSelect
              v-if="selectedClient.id === 'cc-switch'"
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

          <AppNotice v-if="!supported" tone="warning">{{
            key.protocols.length
              ? t('home.unsupported', { protocol: protocolLabel(requiredProtocol, t) })
              : t('home.noProtocols')
          }}</AppNotice>
          <template v-else>
            <div v-if="quickImport" class="modern-connect-import">
              <div>
                <strong>{{ t('home.importTitle', { client: selectedClient.name }) }}</strong>
                <span>{{ t('home.importHelp') }}</span>
              </div>
              <AppButton
                variant="primary"
                size="sm"
                :icon="ArrowUpRight"
                :disabled="importModelMissing"
                @click="importOpen = true"
                >{{ t('home.quickImport') }}</AppButton
              >
            </div>
            <AppNotice v-if="missingModel" tone="info">{{ t('home.selectModel') }}</AppNotice>

            <ConnectTerminal
              v-if="isTerminal"
              :key="signature"
              :lines="terminalLines"
              :content="terminalContent"
              :resolve="resolveTerminal"
              :copyable="supported"
              :note="t('home.steps.' + selectedClient.id)"
            />
            <template v-else>
              <ConnectMirror
                :key="signature"
                :rows="mirrorRows"
                :values="mirrorValues"
                :caption="t('home.mirrorCaption', { client: selectedClient.name })"
                :copyable="supported"
                :resolve-key="copyKey"
              />
              <p class="modern-connect-hint">{{ t('home.steps.' + selectedClient.id) }}</p>
              <div
                v-for="block in blocks"
                :key="signature + block.label"
                class="modern-connect-code"
              >
                <header>
                  <span>{{ block.label }}</span>
                  <AppCopyValue :value="block.content" :resolve-value="block.resolve">
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
            </template>
            <p class="modern-connect-hint">{{ t('home.copyHint') }}</p>
          </template>
        </div>
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
/* 容器查询而不是媒体查询：决定能否分栏的是这块面板的宽度，不是视口。
   容器自身不能被自己的容器查询改布局，所以分栏交给内层的 split。 */
.modern-connect {
  container: modern-connect / inline-size;
}
/* 目录列常驻，切客户端比较配置时不用滚回顶部。 */
.modern-connect-split {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr);
}
.modern-connect-aside {
  border-inline-end: var(--modern-line-width) solid var(--modern-border);
}
.modern-connect-main {
  display: grid;
  align-content: start;
  min-width: 0;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3) var(--modern-space-4) var(--modern-space-4);
}
.modern-connect-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--modern-space-3);
}
.modern-connect-controls > * {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-connect-import {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-segmented-active-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-accent-soft);
  padding: var(--modern-space-2) var(--modern-space-2) var(--modern-space-2) var(--modern-space-3);
}
.modern-connect-import > div {
  display: grid;
  gap: var(--modern-space-0-5);
  min-width: 0;
}
.modern-connect-import strong {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-connect-import span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-connect-hint {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-connect-code {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-connect-code header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-1-5) var(--modern-space-2) var(--modern-space-1-5)
    var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-connect-code pre {
  overflow: auto;
  margin: 0;
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
}
/* 面板窄于 620px 时（200px 目录 + 约 420px 才够放下代码块），目录改成顶部横排。 */
@container modern-connect (max-width: 620px) {
  .modern-connect-split {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-connect-aside {
    border-inline-end: 0;
    border-block-end: var(--modern-line-width) solid var(--modern-border);
  }
}
</style>
