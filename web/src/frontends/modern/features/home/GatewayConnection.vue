<script setup lang="ts">
import { ArrowUpRight, Copy } from '@lucide/vue'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import type { HomeKey } from '@modern/api/home'
import { getModels } from '@modern/api/models'
import { revealAccessKey } from '@modern/api/access-keys'
import { useURLState, positivePage } from '@modern/app/url-state'
import { useMessages } from '@modern/app/messages'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { useApiClient } from '@shared/http/client-context'
import { RequestCancelledError } from '@shared/http/errors'
import {
  AppButton,
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
  gatewayNeedsModel,
  gatewayTargets,
  type GatewayClientID,
  type GatewayConfig,
} from './gateway-config'
import ConnectClientList from './ConnectClientList.vue'
import ConnectFields from './ConnectFields.vue'
import HomeSectionLink from './HomeSectionLink.vue'
import ConnectTerminal from './ConnectTerminal.vue'
import { readGatewayPreferences, rememberGatewayPreferences } from './gateway-preferences'

const props = defineProps<{ keys: HomeKey[]; admin: boolean }>()
const { t } = useI18n()
const client = useApiClient()
const session = useAuthSession()
const messages = useMessages()
const route = useRoute()
const remembered = readGatewayPreferences(props.admin)
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
// 链接中明确指定的选择优先；普通返回首页时恢复浏览器记忆。
state.value = {
  client: route.query.client === undefined ? remembered.client : state.value.client,
  id: route.query.access_key_id === undefined ? remembered.accessKeyID : state.value.id,
}
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
const target = ref<GatewayConfig['target']>(remembered.target)
const model = ref(remembered.model)
const selectedTarget = computed(() => gatewayTargets.find((item) => item.id === target.value)!)
const config = computed<GatewayConfig>(() => ({
  client: selectedClient.value.id,
  target: target.value,
  origin: window.location.origin,
  model: model.value,
  name: 'GPT-Load' + (key.value ? ' · ' + key.value.name : ''),
}))
const signature = computed(() => JSON.stringify([config.value, key.value?.id]))
const mask = computed(() => key.value?.mask ?? 'YOUR_API_KEY')
const isTerminal = computed(() => selectedClient.value.surface === 'cli')
const fields = computed(() => (isTerminal.value ? [] : gatewayFields(config.value, mask.value)))
const configBlocks = computed(() => gatewayConfiguration(config.value, mask.value))
const keyOptions = computed(() =>
  props.keys.map((key) => ({ value: String(key.id), label: key.name, description: key.mask })),
)
const targetOptions = gatewayTargets.map((target) => ({ value: target.id, label: target.name }))
const quickImport = computed(() => ['cc-switch', 'cherry-studio'].includes(selectedClient.value.id))
const needsModel = computed(() => gatewayNeedsModel(selectedClient.value.id, target.value))
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
watch(
  () => [state.value.client, key.value?.id, target.value, model.value] as const,
  ([client, id, target, model]) =>
    rememberGatewayPreferences(props.admin, {
      client,
      accessKeyID: props.admin ? (id ?? state.value.id) : 0,
      target,
      model,
    }),
  { immediate: true, flush: 'sync' },
)
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
  if (!key.value) throw new RequestCancelledError()
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
  configBlocks.value.map((block, index) => ({
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
  if (importing.value || !key.value || importModelMissing.value) return
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
  <AppPanel :title="t('home.connection')" compact flush>
    <template #actions>
      <HomeSectionLink
        v-if="admin"
        :to="{ name: 'modern-access-keys' }"
        :label="t('home.manageKeys')"
      />
    </template>
    <div class="modern-connect">
      <div class="modern-connect-split">
        <ConnectClientList
          class="modern-connect-aside"
          :selected="selectedClient.id"
          @select="selectClient"
        />
        <div class="modern-connect-main">
          <div
            v-if="admin || selectedClient.id === 'cc-switch' || needsModel || quickImport"
            class="modern-connect-controls"
          >
            <AppSearchSelect
              v-if="admin"
              v-model="selectedKey"
              :label="t('home.accessKey')"
              :options="keyOptions"
              size="xs"
              class="modern-connect-field"
            />
            <AppSelect
              v-if="selectedClient.id === 'cc-switch'"
              v-model="target"
              :label="t('home.target')"
              :options="targetOptions"
              size="xs"
              class="modern-connect-field"
            />
            <AppSearchSelect
              v-if="needsModel"
              v-model="model"
              :label="t('home.model')"
              :placeholder="t('home.modelPlaceholder')"
              :load-options="loadModels"
              size="xs"
              allow-custom
              class="modern-connect-field"
            />
            <AppButton
              v-if="quickImport"
              class="modern-connect-import"
              variant="primary"
              size="xs"
              :icon="ArrowUpRight"
              :disabled="!key || importModelMissing"
              @click="importOpen = true"
              >{{ t('home.importAction') }}</AppButton
            >
          </div>
          <div class="modern-connect-content">
            <AppNotice v-if="!key" tone="info">
              {{ t('home.noKeys') }}
              <AppButton v-if="admin" as-child variant="text" size="xs">
                <RouterLink :to="{ name: 'modern-access-keys', query: { panel: 'create' } }">{{
                  t('home.createKey')
                }}</RouterLink>
              </AppButton>
            </AppNotice>
            <p class="modern-connect-instruction">{{ t('home.steps.' + selectedClient.id) }}</p>
            <ConnectTerminal
              v-if="isTerminal"
              :key="signature"
              :blocks="blocks"
              :copyable="Boolean(key) && !missingModel"
            />
            <template v-else>
              <ConnectFields
                :key="signature"
                :fields="fields"
                :copyable="Boolean(key)"
                :resolve-key="copyKey"
              />
              <div
                v-for="block in blocks"
                :key="signature + block.label"
                class="modern-connect-code"
              >
                <header>
                  <span>{{ t('home.fullConfig') }}</span>
                  <AppCopyValue :value="block.content" :resolve-value="block.resolve">
                    <template #trigger="{ copy, pending }">
                      <AppButton
                        size="xs"
                        variant="ghost"
                        :icon="Copy"
                        :loading="pending"
                        :disabled="!key || missingModel"
                        @click="copy()"
                      >
                        {{ t('home.copyConfig') }}
                      </AppButton>
                    </template>
                  </AppCopyValue>
                </header>
                <pre tabindex="0">{{ block.content }}</pre>
              </div>
            </template>
          </div>
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
.modern-connect {
  container: modern-connect / inline-size;
  height: clamp(360px, 52dvh, 480px);
  min-width: 0;
}
.modern-connect-split {
  display: grid;
  grid-template-columns: 184px minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
  height: 100%;
  min-width: 0;
  min-height: 0;
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-connect-aside {
  overflow: auto;
  min-width: 0;
  min-height: 0;
  border-inline-end: var(--modern-line-width) solid var(--modern-border);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
}
.modern-connect-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  gap: var(--modern-space-3);
  padding: var(--modern-space-4);
}
.modern-connect-controls {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--modern-space-3);
}
.modern-connect-field {
  flex: 1;
  min-width: min(100%, 128px);
}
.modern-connect-content {
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: auto;
  min-width: 0;
  min-height: 0;
  gap: var(--modern-space-3);
  padding: var(--modern-space-1);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
}
.modern-connect-content > * {
  flex: none;
}
.modern-connect-instruction {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-connect-import {
  flex: none;
  white-space: nowrap;
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
  gap: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-1) var(--modern-space-2) var(--modern-space-1) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-connect-code header > span {
  min-width: 0;
}
.modern-connect-code pre {
  overflow-x: auto;
  margin: 0;
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
@container modern-connect (max-width: 620px) {
  .modern-connect-split {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto minmax(0, 1fr);
  }
  .modern-connect-aside {
    max-height: 64px;
    border-inline-end: 0;
    border-block-end: var(--modern-line-width) solid var(--modern-border);
  }
}
</style>
