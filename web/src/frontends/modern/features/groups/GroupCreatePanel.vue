<script setup lang="ts">
import { Eye, EyeOff, ChevronDown } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  discoverGroupDraftModels,
  getAPIKeyChannels,
  type GroupConnectionDraft,
  type GroupCreateRequest,
  type GroupCreateResult,
} from '@modern/api/group-create'
import type { ModelCandidate } from '@modern/api/model-discovery'
import { integer, list, record, text } from '@modern/api/response'
import {
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppIcon,
  AppIconButton,
  AppNotice,
  AppSearchSelect,
  AppSelect,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useLoadingFeedback } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupModelPicker from './GroupModelPicker.vue'
import { useGroupCreateOperation } from './group-create-operation'
import {
  credentialCount,
  modelErrors,
  validBaseURL,
  validProxyURL,
  type GroupDraftModel,
} from './group-create-rules'

const props = defineProps<{ initialChannel?: string }>()
const emit = defineEmits<{
  close: []
  created: [result: GroupCreateResult, appended: boolean]
  located: [id: number]
}>()
const { t, n } = useI18n()
const client = useApiClient()
const query = useQuery({
  queryKey: ['modern', 'api-key-channels'],
  queryFn: ({ signal }) => getAPIKeyChannels(client, signal),
})
const channels = computed(() => query.data.value ?? [])
const options = computed(() =>
  channels.value.map((channel) => ({
    value: channel.id,
    label: channel.name,
    keywords: channel.keywords,
  })),
)
const channelID = ref('')
const channel = computed(() => channels.value.find((item) => item.id === channelID.value))
const params = ref<Record<string, string>>({})
const name = ref('')
const price = ref('1')
const credentials = ref('')
const models = ref<GroupDraftModel[]>([])
const proxyMode = ref('inherit')
const proxyURL = ref('')
const advanced = ref(false)
const secretsVisible = ref(new Set<string>())
const candidates = ref<ModelCandidate[]>([])
const discovering = ref(false)
const discoveryFeedback = useLoadingFeedback(discovering)
const discoveryError = ref('')
const connectionRevision = ref(0)
const attempted = ref(false)
const errorText = ref('')
const conflicts = ref<{ id: number; name: string }[]>([])
const operation = useGroupCreateOperation(client)
const locked = computed(() => Boolean(operation.operation.value) || operation.pending.value)
const outcome = operation.outcome
const unresolved = computed(
  () => outcome.value && outcome.value.kind !== 'success' && outcome.value.kind !== 'rejected',
)
const baseline = ref('')
const completed = ref(false)
const channelInput = ref<InstanceType<typeof AppSearchSelect>>()
const nameInput = ref<InstanceType<typeof AppTextField>>()
const priceInput = ref<InstanceType<typeof AppTextField>>()
const proxyInput = ref<InstanceType<typeof AppTextField>>()
const credentialInput = ref<InstanceType<typeof AppTextArea>>()
const paramInputs = new Map<string, { focus(): void }>()
const modelPicker = ref<InstanceType<typeof GroupModelPicker>>()
const errorBox = ref<HTMLElement>()
const confirmAction = ref<'close' | 'channel'>()
let requestedChannel = ''
let resolveLeave: ((allow: boolean) => void) | undefined
let discoveryController: AbortController | undefined
let initialized = false
let disposed = false
function snapshot(): string {
  return JSON.stringify({
    channelID: channelID.value,
    params: params.value,
    name: name.value,
    price: price.value,
    credentials: credentials.value,
    models: models.value,
    proxyMode: proxyMode.value,
    proxyURL: proxyURL.value,
  })
}
const dirty = computed(() => !completed.value && snapshot() !== baseline.value)
baseline.value = snapshot()
const count = computed(() => credentialCount(credentials.value, channel.value))
const credentialError = computed(() =>
  !count.value
    ? t('groupCreate.credentialsRequired')
    : count.value > 5000
      ? t('groupCreate.credentialsLimit')
      : '',
)
const nameError = computed(() => {
  const value = name.value.trim()
  return new TextEncoder().encode(value).length > 255 || /\p{Cc}/u.test(value)
    ? t('groups.edit.nameError')
    : ''
})
const priceError = computed(() =>
  !/^\d+(?:\.\d{1,6})?$/u.test(price.value.trim()) || Number(price.value) > 1000
    ? t('groups.edit.priceError')
    : '',
)
const proxyError = computed(() =>
  channel.value?.proxy && proxyMode.value === 'custom' && !validProxyURL(proxyURL.value.trim())
    ? t('groupCreate.proxyError')
    : '',
)
const paramErrors = computed(() =>
  Object.fromEntries(
    (channel.value?.fields ?? []).flatMap((field) => {
      const value = (params.value[field.key] ?? '').trim()
      const error =
        field.required && !value
          ? t('groupCreate.required')
          : value && field.inputKind === 'url' && !validBaseURL(value)
            ? t('groupCreate.urlError')
            : ''
      return error ? [[field.key, error]] : []
    }),
  ),
)
const structured = computed(
  () =>
    channel.value &&
    (channel.value.credentialFields.length !== 1 ||
      channel.value.credentialFields[0]?.key !== 'api_key'),
)
const credentialPlaceholder = computed(() =>
  structured.value
    ? JSON.stringify(
        Object.fromEntries(channel.value!.credentialFields.map((field) => [field.key, ''])),
        null,
        2,
      )
    : t('groupCreate.credentialsPlaceholder'),
)
const proxyOptions = computed(() => [
  { value: 'inherit', label: t('groupCreate.proxyInherit') },
  { value: 'direct', label: t('groupCreate.proxyDirect') },
  { value: 'custom', label: t('groupCreate.proxyCustom') },
])
function cancelDiscovery(): void {
  discoveryController?.abort()
  discovering.value = false
}
function selectChannel(value: string): void {
  if (locked.value || !channels.value.some((item) => item.id === value)) return
  cancelDiscovery()
  channelID.value = value
  params.value = Object.fromEntries(
    (channel.value?.fields ?? []).map((field) => [
      field.key,
      field.defaultValue || (field.key === 'base_url' ? (channel.value?.defaultBaseURL ?? '') : ''),
    ]),
  )
  credentials.value = ''
  models.value = []
  candidates.value = []
  proxyMode.value = 'inherit'
  proxyURL.value = ''
  secretsVisible.value.clear()
  attempted.value = false
  errorText.value = ''
}
function requestChannel(value: string): void {
  if (locked.value || value === channelID.value) return
  if (channelID.value && (credentials.value.trim() || models.value.length || dirty.value)) {
    requestedChannel = value
    confirmAction.value = 'channel'
  } else selectChannel(value)
}
watch(
  query.data,
  (value) => {
    if (!value || initialized) return
    initialized = true
    if (props.initialChannel && value.some((item) => item.id === props.initialChannel))
      selectChannel(props.initialChannel)
    baseline.value = snapshot()
    void nextTick(() => (channelID.value ? nameInput.value?.focus() : channelInput.value?.focus()))
  },
  { immediate: true },
)
watch(
  [channelID, params, credentials, proxyMode, proxyURL],
  () => {
    connectionRevision.value++
    cancelDiscovery()
    candidates.value = []
    discoveryError.value = ''
  },
  { deep: true },
)
function toggleSecret(key: string): void {
  if (secretsVisible.value.has(key)) secretsVisible.value.delete(key)
  else secretsVisible.value.add(key)
}
function connection(): GroupConnectionDraft {
  const proxy =
    channel.value?.proxy && proxyMode.value !== 'inherit'
      ? proxyMode.value === 'direct'
        ? { mode: 'direct' as const }
        : { mode: 'custom' as const, url: proxyURL.value.trim() }
      : undefined
  return {
    channel_id: channelID.value,
    connection_type: 'api_key',
    params: Object.fromEntries(
      Object.entries(params.value).map(([key, value]) => [key, value.trim()]),
    ),
    credentials: credentials.value,
    ...(proxy ? { proxy } : {}),
  }
}
function request(): GroupCreateRequest {
  return {
    ...connection(),
    ...(name.value.trim() ? { name: name.value.trim() } : {}),
    price_multiplier: price.value.trim(),
    models: models.value.map((model) => ({
      id: model.id.trim(),
      alias: model.alias.trim(),
      alias_enabled: Boolean(model.alias.trim()),
    })),
    confirm_same_target: false,
  }
}
function validConnection(): boolean {
  return (
    Boolean(channel.value) &&
    !Object.keys(paramErrors.value).length &&
    !credentialError.value &&
    !proxyError.value
  )
}
function paramRef(key: string, element: unknown): void {
  if (element && typeof element === 'object' && 'focus' in element)
    paramInputs.set(key, element as { focus(): void })
  else paramInputs.delete(key)
}
function focusConnectionError(): void {
  const field = Object.keys(paramErrors.value)[0]
  if (!channel.value) channelInput.value?.focus()
  else if (field) paramInputs.get(field)?.focus()
  else if (credentialError.value) credentialInput.value?.focus()
  else if (proxyError.value) proxyInput.value?.focus()
}
async function discover(): Promise<void> {
  if (locked.value || discovering.value || !channel.value?.discovery) return
  attempted.value = true
  if (!validConnection()) {
    if (proxyError.value) advanced.value = true
    await nextTick()
    focusConnectionError()
    return
  }
  cancelDiscovery()
  const controller = new AbortController()
  discoveryController = controller
  discovering.value = true
  discoveryError.value = ''
  try {
    const result = await discoverGroupDraftModels(client, connection(), controller.signal)
    if (!controller.signal.aborted) {
      candidates.value = result
    }
  } catch (error) {
    if (!controller.signal.aborted)
      discoveryError.value =
        error instanceof ApiError ? error.message : t('groupCreate.discoveryFailed')
  } finally {
    if (!controller.signal.aborted) discovering.value = false
  }
}
async function execute(): Promise<void> {
  const result = await operation.execute()
  if (!result || disposed) return
  if (result.kind === 'success') {
    completed.value = true
    credentials.value = ''
    operation.reset()
    emit('created', result.result, result.appended)
  } else if (result.kind === 'rejected') {
    if (
      result.error.code === 'CHANNEL_TARGET_CONFLICT' &&
      operation.operation.value?.payload.kind === 'create'
    ) {
      try {
        const groups = list(record(result.error.data).groups).map((raw) => {
          const item = record(raw)
          return { id: integer(item.id, 1), name: text(item.name) }
        })
        if (groups.length) {
          conflicts.value = groups
          await nextTick()
          errorBox.value?.focus()
          return
        }
      } catch {
        /* 无效冲突响应不展示未经验证的分组操作。 */
      }
    }
    operation.reset()
    errorText.value = result.error.message || t('groupCreate.failed')
    const data = result.error.data
    if (
      result.error.code === 'VALIDATION_FAILED' &&
      data &&
      typeof data === 'object' &&
      'entry' in data &&
      Number.isSafeInteger(data.entry) &&
      'field' in data &&
      typeof data.field === 'string' &&
      /^[a-z][a-z0-9_]*$/u.test(data.field)
    ) {
      errorText.value = t('groupCreate.credentialFieldError', {
        entry: data.entry,
        field: data.field,
      })
    }
    await nextTick()
    errorBox.value?.focus()
  } else {
    await nextTick()
    errorBox.value?.focus()
  }
}
async function submit(): Promise<void> {
  if (locked.value || query.isPending.value) return
  attempted.value = true
  errorText.value = ''
  if (!validConnection() || nameError.value || priceError.value || modelErrors(models.value).size) {
    if (priceError.value || proxyError.value) advanced.value = true
    await nextTick()
    if (nameError.value) nameInput.value?.focus()
    else if (!validConnection()) focusConnectionError()
    else if (priceError.value) priceInput.value?.focus()
    else modelPicker.value?.focusFirstInvalid()
    return
  }
  cancelDiscovery()
  try {
    operation.begin({ kind: 'create', request: request() })
  } catch {
    errorText.value = t('groupCreate.failed')
    return
  }
  await execute()
}
async function confirmSeparate(): Promise<void> {
  const current = operation.operation.value?.payload
  if (current?.kind !== 'create' || operation.pending.value) return
  const body = { ...current.request, confirm_same_target: true }
  operation.reset()
  conflicts.value = []
  operation.begin({ kind: 'create', request: body })
  await execute()
}
async function appendTo(group: { id: number; name: string }): Promise<void> {
  const current = operation.operation.value?.payload
  if (current?.kind !== 'create' || operation.pending.value) return
  const raw = current.request.credentials
  operation.reset()
  conflicts.value = []
  operation.begin({ kind: 'append', group: { id: group.id, name: group.name }, credentials: raw })
  await execute()
}
function editDraft(): void {
  if (operation.pending.value) return
  operation.reset()
  conflicts.value = []
}
function close(): void {
  if (operation.pending.value) return
  if (dirty.value || operation.operation.value) confirmAction.value = 'close'
  else emit('close')
}
function cancelConfirm(): void {
  confirmAction.value = undefined
  resolveLeave?.(false)
  resolveLeave = undefined
}
function confirmDiscard(): void {
  const action = confirmAction.value
  confirmAction.value = undefined
  if (action === 'channel') selectChannel(requestedChannel)
  else {
    resolveLeave?.(true)
    resolveLeave = undefined
    emit('close')
  }
}
function guardLeave(): boolean | Promise<boolean> {
  if (operation.pending.value) return false
  if (!dirty.value && !operation.operation.value) return true
  resolveLeave?.(false)
  confirmAction.value = 'close'
  return new Promise((resolve) => {
    resolveLeave = resolve
  })
}
onBeforeRouteLeave(guardLeave)
onBeforeRouteUpdate(guardLeave)
function beforeUnload(event: BeforeUnloadEvent): void {
  if (!dirty.value && !operation.operation.value) return
  event.preventDefault()
  event.returnValue = ''
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onScopeDispose(() => {
  disposed = true
  cancelDiscovery()
  resolveLeave?.(false)
  window.removeEventListener('beforeunload', beforeUnload)
  credentials.value = ''
  params.value = {}
})
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (value) => {
        if (!value) close()
      }
    "
  >
    <AppDialogContent
      placement="editor"
      :title="t('groupCreate.title')"
      :description="t('groupCreate.title')"
      @open-auto-focus.prevent
    >
      <AppDialogHeader
        :title="t('groupCreate.title')"
        :close-label="t('ui.close')"
        :close-disabled="operation.pending.value"
        @close="close"
      />
      <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
      <AppCollectionState
        v-else-if="!query.data.value"
        :title="t('groupCreate.channelsFailed')"
        error
      >
        <AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
      </AppCollectionState>
      <form v-else class="modern-group-create-form" novalidate @submit.prevent="submit">
        <div class="modern-group-create-body">
          <AppSearchSelect
            ref="channelInput"
            :model-value="channelID"
            :label="t('groupCreate.channel')"
            :options="options"
            :disabled="locked"
            :error="attempted && !channel ? t('groupCreate.required') : undefined"
            @update:model-value="requestChannel"
          >
            <template #option="{ option }">
              <AppChannelIcon
                :icon="channels.find((item) => item.id === option.value)?.icon"
                :mark="channels.find((item) => item.id === option.value)?.mark"
                :name="option.label"
              />
              <span>{{ option.label }}</span>
            </template>
          </AppSearchSelect>
          <AppTextField
            ref="nameInput"
            v-model="name"
            :label="t('groups.edit.name')"
            :placeholder="t('groupCreate.autoName')"
            autocomplete="off"
            :disabled="locked"
            :error="attempted ? nameError : undefined"
          />
          <template v-if="channel">
            <AppTextField
              v-for="field in channel.fields"
              :key="field.key"
              :ref="(element) => paramRef(field.key, element)"
              :model-value="params[field.key] ?? ''"
              :label="field.key === 'base_url' ? t('groupCreate.baseURL') : field.label"
              :type="
                (field.sensitive || field.inputKind === 'secret') && !secretsVisible.has(field.key)
                  ? 'password'
                  : 'text'
              "
              :placeholder="
                field.defaultValue || (field.inputKind === 'url' ? 'https://' : undefined)
              "
              :disabled="locked"
              :error="attempted ? paramErrors[field.key] : undefined"
              autocomplete="off"
              spellcheck="false"
              @update:model-value="params[field.key] = $event"
            >
              <template v-if="field.sensitive || field.inputKind === 'secret'" #suffix>
                <AppIconButton
                  :icon="secretsVisible.has(field.key) ? EyeOff : Eye"
                  :label="
                    t(
                      secretsVisible.has(field.key)
                        ? 'groupCreate.hideSecret'
                        : 'groupCreate.showSecret',
                    )
                  "
                  size="xs"
                  @click="toggleSecret(field.key)"
                />
              </template>
            </AppTextField>
            <AppTextArea
              ref="credentialInput"
              v-model="credentials"
              :label="t('groupCreate.credentials')"
              :description="
                t(structured ? 'groupCreate.structuredHelp' : 'groupCreate.credentialsHelp')
              "
              :placeholder="credentialPlaceholder"
              :rows="6"
              mono
              autocomplete="off"
              spellcheck="false"
              :disabled="locked"
              :error="attempted ? credentialError : undefined"
            />
            <p class="modern-group-create-count">
              {{ t('groupCreate.credentialCount', { count: n(count) }) }}
            </p>
            <GroupModelPicker
              ref="modelPicker"
              v-model="models"
              :connection-revision="connectionRevision"
              :candidates="candidates"
              :loading="discoveryFeedback"
              :discovery-supported="channel.discovery"
              :can-discover="validConnection()"
              :discovery-error="discoveryError"
              :disabled="locked"
              :attempted="attempted"
              @discover="discover"
              @cancel-discovery="cancelDiscovery"
            />
            <details
              class="modern-group-create-advanced"
              :open="advanced"
              @toggle="advanced = ($event.target as HTMLDetailsElement).open"
            >
              <summary>
                <AppIcon :icon="ChevronDown" size="sm" />{{ t('groupCreate.moreSettings') }}
              </summary>
              <div class="modern-group-create-options">
                <AppTextField
                  ref="priceInput"
                  v-model="price"
                  :label="t('groups.edit.price')"
                  inputmode="decimal"
                  :disabled="locked"
                  :error="attempted ? priceError : undefined"
                />
                <AppSelect
                  v-if="channel.proxy"
                  v-model="proxyMode"
                  :label="t('groupCreate.proxy')"
                  :options="proxyOptions"
                  :disabled="locked"
                />
                <AppTextField
                  v-if="channel.proxy && proxyMode === 'custom'"
                  ref="proxyInput"
                  v-model="proxyURL"
                  :label="t('groupCreate.proxyURL')"
                  :disabled="locked"
                  :error="attempted ? proxyError : undefined"
                  placeholder="http://127.0.0.1:7890"
                  autocomplete="off"
                  spellcheck="false"
                />
              </div>
            </details>
          </template>
          <div v-if="errorText" ref="errorBox" tabindex="-1">
            <AppNotice tone="danger">{{ errorText }}</AppNotice>
          </div>
          <section
            v-if="conflicts.length"
            ref="errorBox"
            class="modern-group-create-conflict"
            tabindex="-1"
          >
            <AppNotice tone="warning">{{ t('groupCreate.targetConflict') }}</AppNotice>
            <p>{{ t('groupCreate.appendHelp') }}</p>
            <div
              v-for="group in conflicts"
              :key="group.id"
              class="modern-group-create-conflict-row"
            >
              <span>{{ group.name }}</span>
              <AppButton size="sm" :disabled="operation.pending.value" @click="appendTo(group)">{{
                t('groupCreate.append')
              }}</AppButton>
            </div>
            <div class="modern-group-create-actions">
              <AppButton size="sm" @click="editDraft">{{ t('groupCreate.editDraft') }}</AppButton>
              <AppButton size="sm" variant="primary" @click="confirmSeparate">{{
                t('groupCreate.createSeparate')
              }}</AppButton>
            </div>
          </section>
          <div v-else-if="unresolved" ref="errorBox" tabindex="-1">
            <AppNotice tone="warning">
              {{ t('groupCreate.outcome.' + outcome!.kind) }}
              <template #actions>
                <AppButton
                  v-if="outcome?.kind === 'expired' && outcome.groupID"
                  size="sm"
                  @click="emit('located', outcome.groupID!)"
                  >{{ t('groupCreate.viewGroup') }}</AppButton
                >
                <AppButton
                  v-else-if="outcome?.kind !== 'expired'"
                  size="sm"
                  :disabled="!operation.canRetry.value"
                  @click="execute"
                  >{{ t('groupCreate.checkResult') }}</AppButton
                >
              </template>
            </AppNotice>
          </div>
        </div>
        <footer class="modern-group-create-footer">
          <span v-if="channel">{{
            t('groupCreate.summary', { credentials: n(count), models: n(models.length) })
          }}</span>
          <div class="modern-group-create-actions">
            <AppButton :disabled="operation.pending.value" @click="close">{{
              t('ui.cancel')
            }}</AppButton>
            <AppButton
              type="submit"
              variant="primary"
              :loading="operation.pending.value"
              :disabled="locked || !channels.length"
              >{{ t('groups.create') }}</AppButton
            >
          </div>
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
  <DialogRoot
    :open="Boolean(confirmAction)"
    @update:open="
      (value) => {
        if (!value) cancelConfirm()
      }
    "
  >
    <AppDialogContent
      :title="t(confirmAction === 'channel' ? 'groupCreate.changeChannel' : 'groups.edit.unsaved')"
      :description="
        t(
          confirmAction === 'channel'
            ? 'groupCreate.changeChannelHelp'
            : unresolved
              ? 'groupCreate.abandonUnknown'
              : 'groups.edit.unsavedHelp',
        )
      "
    >
      <AppDialogHeader
        :title="
          t(confirmAction === 'channel' ? 'groupCreate.changeChannel' : 'groups.edit.unsaved')
        "
        :description="
          t(
            confirmAction === 'channel'
              ? 'groupCreate.changeChannelHelp'
              : unresolved
                ? 'groupCreate.abandonUnknown'
                : 'groups.edit.unsavedHelp',
          )
        "
        :close-label="t('ui.close')"
        @close="cancelConfirm"
      />
      <div class="modern-group-create-confirm">
        <AppButton @click="cancelConfirm">{{ t('groups.edit.keepEditing') }}</AppButton>
        <AppButton variant="danger" @click="confirmDiscard">{{
          t('groups.edit.discard')
        }}</AppButton>
      </div>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-group-create-form {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-group-create-body {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
}
.modern-group-create-count {
  margin-top: calc(-1 * var(--modern-space-2));
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-create-advanced {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-4);
}
.modern-group-create-advanced summary {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  cursor: pointer;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  list-style: none;
}
.modern-group-create-advanced summary::-webkit-details-marker {
  display: none;
}
.modern-group-create-advanced[open] summary {
  margin-bottom: var(--modern-space-4);
}
.modern-group-create-options {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-group-create-footer {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-4) var(--modern-space-5);
}
.modern-group-create-footer > span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-create-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  margin-left: auto;
}
.modern-group-create-conflict {
  display: grid;
  gap: var(--modern-space-3);
}
.modern-group-create-conflict > p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-group-create-conflict-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-group-create-conflict-row > span {
  min-width: 0;
  overflow-wrap: anywhere;
}
.modern-group-create-confirm {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
  padding: var(--modern-space-5);
}
</style>
