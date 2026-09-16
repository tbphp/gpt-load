<script setup lang="ts">
import { channelSearchOption } from '@modern/components/channel-options'
import { Eye, EyeOff, ChevronDown, KeyRound, UserRound } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useMessageSource } from '@modern/app/messages'
import { useI18n } from 'vue-i18n'
import {
  discoverGroupDraftModels,
  getGroupChannels,
  type GroupConnectionDraft,
  type GroupCreateRequest,
  type GroupCreateResult,
  type ProxyOverride,
} from '@modern/api/group-create'
import { readCredentialStage, type CredentialStage } from '@modern/api/credential-stages'
import type { ModelCandidate } from '@modern/api/model-discovery'
import { integer, list, record, text } from '@modern/api/response'
import {
  AppButton,
  AppBadge,
  AppChannelIcon,
  AppCollectionState,
  AppConfirmDialog,
  AppIcon,
  AppIconButton,
  AppNotice,
  AppProtocolTag,
  AppSearchSelect,
  AppSegmentedField,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useLoadingFeedback } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupModelPicker from './GroupModelPicker.vue'
import GroupEditorSurface from './GroupEditorSurface.vue'
import SubscriptionCredentialStager from './SubscriptionCredentialStager.vue'
import { useGroupCreateOperation } from './group-create-operation'
import { validProxyURL } from '@modern/app/proxy'
import {
  credentialCount,
  modelErrors,
  validBaseURL,
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
  queryKey: ['modern', 'group-channels'],
  queryFn: ({ signal }) => getGroupChannels(client, signal),
})
const channels = computed(() => query.data.value ?? [])
const options = computed(() => channels.value.map(channelSearchOption))
const channelID = ref('')
const channel = computed(() => channels.value.find((item) => item.id === channelID.value))
const subscription = computed(() => channel.value?.connectionType === 'subscription')
const params = ref<Record<string, string>>({})
const name = ref('')
const price = ref('1')
const credentials = ref('')
const stages = ref<CredentialStage[]>([])
const authorizationPending = computed(() =>
  stages.value.some((stage) =>
    ['pending_authorization', 'exchanging', 'outcome_unknown'].includes(stage.status),
  ),
)
const stagingBusy = ref(false)
const stagingDirty = ref(false)
const stager = ref<InstanceType<typeof SubscriptionCredentialStager>>()
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
const inputLocked = computed(() => locked.value || stagingBusy.value)
const busy = computed(() => operation.pending.value || stagingBusy.value)
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
let approvedLeave = false
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
    stages: stages.value.map((stage) => stage.id),
    models: models.value,
    proxyMode: proxyMode.value,
    proxyURL: proxyURL.value,
  })
}
const dirty = computed(
  () => !completed.value && (snapshot() !== baseline.value || stagingDirty.value),
)
baseline.value = snapshot()
function currentReadyIDs(): string[] {
  return stages.value
    .filter((stage) => stage.status === 'ready' && stage.expiresAt > Date.now())
    .map((stage) => stage.id)
}
function expireReadyStages(): void {
  if (stages.value.some((stage) => stage.status === 'ready' && stage.expiresAt <= Date.now()))
    stages.value = stages.value.map((stage) =>
      stage.status === 'ready' && stage.expiresAt <= Date.now()
        ? { ...stage, status: 'expired' }
        : stage,
    )
}
const count = computed(() =>
  subscription.value ? currentReadyIDs().length : credentialCount(credentials.value, channel.value),
)
const credentialError = computed(() =>
  !count.value
    ? t(subscription.value ? 'subscriptions.readyRequired' : 'groupCreate.credentialsRequired')
    : count.value > (subscription.value ? 1000 : 5000)
      ? t(subscription.value ? 'subscriptions.stageLimit' : 'groupCreate.credentialsLimit')
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
const proxyOverride = computed<ProxyOverride | undefined>(() =>
  !channel.value?.proxy || proxyMode.value === 'inherit'
    ? undefined
    : proxyMode.value === 'direct'
      ? { mode: 'direct' }
      : { mode: 'custom', url: proxyURL.value.trim() },
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
  if (inputLocked.value || !channels.value.some((item) => item.id === value)) return
  cancelDiscovery()
  channelID.value = value
  params.value = Object.fromEntries(
    (channel.value?.fields ?? []).map((field) => [
      field.key,
      field.defaultValue || (field.key === 'base_url' ? (channel.value?.defaultBaseURL ?? '') : ''),
    ]),
  )
  credentials.value = ''
  stages.value = []
  stagingDirty.value = false
  models.value = []
  candidates.value = []
  proxyMode.value = 'inherit'
  proxyURL.value = ''
  secretsVisible.value.clear()
  attempted.value = false
  errorText.value = ''
}
function requestChannel(value: string): void {
  if (inputLocked.value || value === channelID.value) return
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
function invalidateDiscovery(): void {
  connectionRevision.value++
  cancelDiscovery()
  candidates.value = []
  discoveryError.value = ''
}
watch([channelID, params, credentials, proxyMode, proxyURL], invalidateDiscovery, { deep: true })
// 只跟踪实际用于发现模型的账号，其他账号就绪不打断当前模型选择。
watch(() => currentReadyIDs()[0], invalidateDiscovery)
function toggleSecret(key: string): void {
  if (secretsVisible.value.has(key)) secretsVisible.value.delete(key)
  else secretsVisible.value.add(key)
}
function connection(): GroupConnectionDraft {
  const proxy = proxyOverride.value
  return {
    channel_id: channelID.value,
    params: Object.fromEntries(
      Object.entries(params.value).map(([key, value]) => [key, value.trim()]),
    ),
    ...(proxy ? { proxy } : {}),
  }
}
function request(): GroupCreateRequest {
  return {
    ...connection(),
    ...(subscription.value
      ? { connection_type: 'subscription' as const, staged_credential_ids: currentReadyIDs() }
      : { connection_type: 'api_key' as const, credentials: credentials.value }),
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
    !stagingBusy.value &&
    (!subscription.value || currentReadyIDs().length > 0) &&
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
  else if (credentialError.value) {
    if (subscription.value) stager.value?.focus()
    else credentialInput.value?.focus()
  } else if (proxyError.value) proxyInput.value?.focus()
}
async function discover(): Promise<void> {
  if (inputLocked.value || discovering.value || !channel.value?.discovery) return
  expireReadyStages()
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
  const stagedID = subscription.value ? currentReadyIDs()[0] : undefined
  try {
    const body = subscription.value
      ? {
          ...connection(),
          connection_type: 'subscription' as const,
          staged_credential_id: stagedID!,
        }
      : { ...connection(), connection_type: 'api_key' as const, credentials: credentials.value }
    const result = await discoverGroupDraftModels(client, body, controller.signal)
    if (!controller.signal.aborted) {
      candidates.value = result
    }
  } catch (error) {
    if (!controller.signal.aborted)
      discoveryError.value =
        error instanceof ApiError ? error.message : t('groupCreate.discoveryFailed')
  } finally {
    if (stagedID && !controller.signal.aborted) {
      try {
        const updated = await readCredentialStage(client, stagedID, controller.signal)
        if (!controller.signal.aborted)
          stages.value = stages.value.map((stage) => (stage.id === updated.id ? updated : stage))
      } catch {
        if (!controller.signal.aborted && !discoveryError.value)
          discoveryError.value = t('subscriptions.refreshFailed')
      }
    }
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
  if (inputLocked.value || query.isPending.value) return
  if (authorizationPending.value) {
    stager.value?.focus()
    return
  }
  expireReadyStages()
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
  operation.reset()
  conflicts.value = []
  if (current.request.connection_type === 'subscription')
    operation.begin({
      kind: 'connect',
      group: { ...group },
      stageIDs: current.request.staged_credential_ids,
    })
  else
    operation.begin({
      kind: 'append',
      group: { ...group },
      credentials: current.request.credentials,
    })
  await execute()
}
function editDraft(): void {
  if (operation.pending.value) return
  operation.reset()
  conflicts.value = []
}
function close(): void {
  if (busy.value) return
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
    if (!resolveLeave) approvedLeave = true
    const controlledLeave = Boolean(resolveLeave)
    resolveLeave?.(true)
    resolveLeave = undefined
    if (!controlledLeave) emit('close')
  }
}
function guardLeave(): boolean | Promise<boolean> {
  if (approvedLeave) {
    approvedLeave = false
    return true
  }
  if (busy.value) return false
  if (!dirty.value && !operation.operation.value) return true
  resolveLeave?.(false)
  confirmAction.value = 'close'
  return new Promise((resolve) => {
    resolveLeave = resolve
  })
}
onBeforeRouteLeave(guardLeave)
onBeforeRouteUpdate(
  (to, from) => (to.path === from.path && to.query.panel === from.query.panel) || guardLeave(),
)
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
  stages.value = []
  params.value = {}
})
useMessageSource(() => (errorText.value ? { text: errorText.value, tone: 'danger' } : undefined))
</script>

<template>
  <GroupEditorSurface
    :pending="busy"
    :title="t('groupCreate.title')"
    :description="t('groupCreate.title')"
    prevent-auto-focus
    hide-description
    @close="close"
  >
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
          :disabled="inputLocked"
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
            <AppBadge
              size="xs"
              :icon="
                channels.find((item) => item.id === option.value)?.connectionType === 'subscription'
                  ? UserRound
                  : KeyRound
              "
              :tone="
                channels.find((item) => item.id === option.value)?.connectionType === 'subscription'
                  ? 'brand'
                  : 'neutral'
              "
              >{{
                t(
                  channels.find((item) => item.id === option.value)?.connectionType ===
                    'subscription'
                    ? 'subscriptions.connectionType'
                    : 'subscriptions.apiKey',
                )
              }}</AppBadge
            >
          </template>
        </AppSearchSelect>
        <div v-if="channel?.nativeProtocols.length" class="modern-group-create-protocols">
          <span>{{ t('groupCreate.supportedProtocols') }}</span>
          <div>
            <AppProtocolTag
              v-for="protocol in channel.nativeProtocols"
              :key="protocol"
              :protocol="protocol"
            />
          </div>
        </div>
        <AppTextField
          ref="nameInput"
          v-model="name"
          :label="t('groups.edit.name')"
          :placeholder="t('groupCreate.autoName')"
          autocomplete="off"
          :disabled="inputLocked"
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
            :disabled="inputLocked"
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
            v-if="!subscription"
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
            :disabled="inputLocked"
            :error="attempted ? credentialError : undefined"
          />
          <p v-if="!subscription" class="modern-group-create-count">
            {{ t('groupCreate.credentialCount', { count: n(count) }) }}
          </p>
          <SubscriptionCredentialStager
            v-else
            :key="channel.id"
            ref="stager"
            v-model="stages"
            :channel="channel"
            :proxy="proxyOverride"
            :disabled="locked || discovering"
            :entry-disabled="Boolean(proxyError) || Object.keys(paramErrors).length > 0"
            :error="attempted ? credentialError : undefined"
            @busy="stagingBusy = $event"
            @dirty="stagingDirty = $event"
          />
          <GroupModelPicker
            ref="modelPicker"
            v-model="models"
            :connection-revision="connectionRevision"
            :candidates="candidates"
            :loading="discoveryFeedback"
            :discovery-supported="channel.discovery"
            :can-discover="validConnection()"
            :discovery-error="discoveryError"
            :disabled="inputLocked"
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
                :disabled="inputLocked"
                :error="attempted ? priceError : undefined"
              />
              <AppSegmentedField
                v-if="channel.proxy"
                v-model="proxyMode"
                :label="t('groupCreate.proxy')"
                :options="proxyOptions"
                :disabled="inputLocked || stages.length > 0"
                :description="stages.length ? t('subscriptions.proxyLocked') : undefined"
              />
              <AppTextField
                v-if="channel.proxy && proxyMode === 'custom'"
                ref="proxyInput"
                v-model="proxyURL"
                :label="t('groupCreate.proxyURL')"
                :disabled="inputLocked || stages.length > 0"
                :error="attempted ? proxyError : undefined"
                placeholder="http://127.0.0.1:7890"
                autocomplete="off"
                spellcheck="false"
              />
            </div>
          </details>
        </template>
        <section
          v-if="conflicts.length"
          ref="errorBox"
          class="modern-group-create-conflict"
          tabindex="-1"
        >
          <AppNotice tone="warning">{{ t('groupCreate.targetConflict') }}</AppNotice>
          <p>{{ t('groupCreate.appendHelp') }}</p>
          <div v-for="group in conflicts" :key="group.id" class="modern-group-create-conflict-row">
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
          authorizationPending
            ? t('subscriptions.finishAuthorization')
            : t(subscription ? 'subscriptions.createSummary' : 'groupCreate.summary', {
                credentials: n(count),
                models: n(models.length),
              })
        }}</span>
        <div class="modern-group-create-actions">
          <AppButton :disabled="busy" @click="close">{{ t('ui.cancel') }}</AppButton>
          <AppButton
            type="submit"
            variant="primary"
            :loading="operation.pending.value"
            :disabled="inputLocked || !channels.length || authorizationPending"
            >{{ t('groups.create') }}</AppButton
          >
        </div>
      </footer>
    </form>
  </GroupEditorSurface>
  <AppConfirmDialog
    :open="Boolean(confirmAction)"
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
    :cancel-label="
      t(confirmAction === 'channel' ? 'groupCreate.cancelChannelChange' : 'groups.edit.keepEditing')
    "
    :confirm-label="
      t(confirmAction === 'channel' ? 'groupCreate.confirmChannelChange' : 'groups.edit.discard')
    "
    tone="danger"
    @cancel="cancelConfirm"
    @confirm="confirmDiscard"
  />
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
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
}
.modern-group-create-protocols {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-create-protocols > div {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1);
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
</style>
