<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { Check } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import {
  createGroup,
  discoverModels,
  importGroupKeys,
  isUpstreamUrlConflictData,
  type GroupCreateRequest,
  type GroupRuntimeConfigDto,
  type UpstreamUrlConflictData,
} from '@/app/resources/groups'
import type { GroupProtocol } from '@/api/control/types'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'

import { channelPresets, type ChannelPreset } from './channel-presets'
import ImportConnectionStep from './ImportConnectionStep.vue'
import ImportModelsStep from './ImportModelsStep.vue'
import ImportOperationNotice from './ImportOperationNotice.vue'
import ImportReviewStep from './ImportReviewStep.vue'
import { useImportOperationOwner } from './import-operation-owner'
import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import type { ImportDraft } from './model-draft'
import { createModelDraft, toGroupModels } from './model-draft'
import { useUnsavedChanges } from '@/app/unsaved-changes'

const props = defineProps<{ initialDraft?: ImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const router = useRouter()
const { t } = useI18n()

function freshDraft(): ImportDraft {
  return {
    mode: 'new',
    step: 1,
    preset_id: 'openai',
    name: '',
    upstream_url: channelPresets[0]!.upstream_url,
    protocols: [...channelPresets[0]!.protocols],
    keys: '',
    header_rules: { set: {}, remove: [] },
    models: [],
  }
}

const source = props.initialDraft ?? freshDraft()
const draft = reactive<ImportDraft>({
  ...source,
  protocols: [...source.protocols],
  header_rules: { set: { ...source.header_rules.set }, remove: [...source.header_rules.remove] },
  models: source.models.map((model) => ({ ...model })),
})
const discoveryPending = ref(false)
const discoveryReady = ref(draft.step > 1 && draft.models.length > 0)
const discoveryFailed = ref(false)
const manualMode = ref(
  draft.models.length > 0 ||
    (props.initialDraft?.step === 2 && props.initialDraft.models.length === 0),
)
const errorKey = ref('')
const conflict = ref<UpstreamUrlConflictData | null>(null)
const completed = ref(false)
const connectionStep = ref<InstanceType<typeof ImportConnectionStep>>()
const modelsStep = ref<InstanceType<typeof ImportModelsStep>>()
const reviewStep = ref<InstanceType<typeof ImportReviewStep>>()
const importOperationOwner = useImportOperationOwner()
const createOperation = importOperationOwner.createGroup
const appendOperation = importOperationOwner.importKeys
if (createOperation.outcome.value?.kind === 'confirmed') createOperation.reset()
if (appendOperation.outcome.value?.kind === 'confirmed') appendOperation.reset()
const pending = computed(
  () => discoveryPending.value || createOperation.pending.value || appendOperation.pending.value,
)
const activeMutation = computed(() =>
  appendOperation.operation.value
    ? appendOperation
    : createOperation.operation.value
      ? createOperation
      : null,
)
const mutationOutcome = computed(() => activeMutation.value?.outcome.value ?? null)
const operationNoticeKey = computed(() => {
  const outcome = mutationOutcome.value
  if (!outcome) return ''
  if (outcome.kind === 'reconciling') return 'import.operation.reconciling'
  if (outcome.kind === 'indeterminate') return 'import.operation.indeterminate'
  if (outcome.kind === 'failed' && outcome.reason === 'retryable-precondition')
    return 'import.operation.waiting'
  if (outcome.kind === 'failed' && outcome.reason === 'expired-known')
    return 'import.operation.expired'
  return ''
})
const operationResourceIdentity = computed(() => {
  const outcome = mutationOutcome.value
  return outcome?.kind === 'failed' && outcome.reason === 'expired-known'
    ? outcome.resource_identity
    : ''
})
const canRetryOperation = computed(() => activeMutation.value?.canRetry.value ?? false)
let discoveryController: AbortController | undefined
let componentActive = true

const keyAnalysis = computed(() => analyzeKeys(draft.keys))
const dirty = computed(
  () =>
    !completed.value &&
    (draft.name !== '' ||
      draft.keys !== '' ||
      draft.step !== 1 ||
      draft.upstream_url !== channelPresets[0]!.upstream_url ||
      draft.protocols.join(',') !== 'openai' ||
      Object.keys(draft.header_rules.set).length > 0 ||
      draft.header_rules.remove.length > 0),
)
const headerRulesValid = ref(true)
const canDiscover = computed(
  () =>
    !pending.value &&
    draft.upstream_url.trim() !== '' &&
    draft.protocols.length > 0 &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys &&
    headerRulesValid.value,
)
const canReview = computed(() => !pending.value && toGroupModels(draft.models).length > 0)
useUnsavedChanges(dirty)
const unregisterRecovery = recovery.register(() => (completed.value ? null : snapshotDraft()))

function snapshotDraft(): ImportDraft {
  return {
    ...draft,
    protocols: [...draft.protocols],
    header_rules: { set: { ...draft.header_rules.set }, remove: [...draft.header_rules.remove] },
    models: draft.models.map((model) => ({ ...model })),
  }
}

function buildDraftConfig(): GroupRuntimeConfigDto {
  const headerRules = snapshotDraft().header_rules
  return Object.keys(headerRules.set).length > 0 || headerRules.remove.length > 0
    ? { header_rules: headerRules }
    : {}
}

function startAction(): AbortController {
  discoveryController?.abort()
  discoveryController = new AbortController()
  discoveryPending.value = true
  errorKey.value = ''
  return discoveryController
}

function finishAction(controller: AbortController): void {
  if (discoveryController === controller) {
    discoveryController = undefined
    discoveryPending.value = false
  }
}

function invalidateDiscovery(): void {
  if (discoveryController) {
    discoveryController.abort()
    discoveryController = undefined
    discoveryPending.value = false
  }
  if (!discoveryReady.value && !discoveryFailed.value) return
  discoveryReady.value = false
  discoveryFailed.value = false
  manualMode.value = false
  draft.models = []
  if (draft.step > 1) draft.step = 1
}

async function moveToStep(step: ImportDraft['step']): Promise<void> {
  draft.step = step
  await nextTick()
  const surface =
    step === 1 ? connectionStep.value : step === 2 ? modelsStep.value : reviewStep.value
  surface?.focusHeading()
}

watch(
  [
    () => draft.upstream_url,
    () => draft.keys,
    () => draft.protocols.join('\u0000'),
    () => JSON.stringify(draft.header_rules),
  ],
  invalidateDiscovery,
)

function applyPreset(id: ChannelPreset['id']): void {
  const preset = channelPresets.find((item) => item.id === id)!
  draft.preset_id = id
  draft.upstream_url = preset.upstream_url
  draft.protocols = [...preset.protocols]
}

function toggleProtocol(protocol: GroupProtocol, checked: boolean): void {
  draft.protocols = checked
    ? [...new Set([...draft.protocols, protocol])]
    : draft.protocols.filter((item) => item !== protocol)
}

async function runDiscovery(): Promise<void> {
  if (!canDiscover.value) return
  const controller = startAction()
  conflict.value = null
  try {
    const result = await discoverModels(
      api,
      {
        upstream_url: draft.upstream_url,
        protocols: [...draft.protocols],
        keys: draft.keys,
        config: buildDraftConfig(),
      },
      controller.signal,
    )
    if (discoveryController !== controller) return
    draft.models = createModelDraft(result.models)
    discoveryReady.value = true
    discoveryFailed.value = false
    manualMode.value = true
    await moveToStep(2)
  } catch (error: unknown) {
    if (discoveryController !== controller || error instanceof RequestCancelledError) return
    discoveryFailed.value = true
    manualMode.value = false
    await moveToStep(2)
    errorKey.value = 'import.discoveryFailed'
  } finally {
    finishAction(controller)
  }
}

function showManualPath(): void {
  manualMode.value = true
  errorKey.value = ''
}

function buildCreateBody(confirmSameURL: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    ...(name ? { name } : {}),
    upstream_url: draft.upstream_url,
    protocols: [...draft.protocols],
    models: toGroupModels(draft.models),
    config: buildDraftConfig(),
    keys: draft.keys,
    confirm_same_upstream_url: confirmSameURL,
  }
}

async function finishSuccess(groupID: number): Promise<void> {
  if (!componentActive) return
  await applyInvalidationPlan(queryClient, mutationInvalidationPlans.group.create)
  if (!componentActive) return
  completed.value = true
  draft.keys = ''
  recovery.clear()
  await router.push({ name: 'group-detail', params: { id: groupID } })
}

async function submitCreate(confirmSameURL = false): Promise<void> {
  if (pending.value || toGroupModels(draft.models).length === 0) return
  if (confirmSameURL) createOperation.reset()
  if (!importOperationOwner.beginCreate(buildCreateBody(confirmSameURL))) return
  if (!confirmSameURL) conflict.value = null
  errorKey.value = ''
  const outcome = await createOperation.execute((operation, signal) =>
    createGroup(api, operation.payload, operation.idempotencyKey, signal),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    createOperation.reset()
    if (!componentActive) return
    await finishSuccess(targetID)
    return
  }
  if (!componentActive) return
  if (outcome.kind === 'failed' && outcome.reason === 'rejected') {
    const error = createOperation.lastError.value
    createOperation.reset()
    if (
      error instanceof ApiError &&
      error.code === 'UPSTREAM_URL_CONFLICT' &&
      isUpstreamUrlConflictData(error.data)
    ) {
      conflict.value = error.data
      return
    }
    errorKey.value = 'import.createFailed'
  }
}

async function appendToGroup(groupID: number): Promise<void> {
  if (pending.value) return
  createOperation.reset()
  if (!importOperationOwner.beginImportKeys({ groupID, keys: draft.keys }, 'new')) return
  errorKey.value = ''
  const outcome = await appendOperation.execute((operation, signal) =>
    importGroupKeys(
      api,
      operation.payload.groupID,
      { keys: operation.payload.keys },
      operation.idempotencyKey,
      signal,
    ),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    appendOperation.reset()
    if (!componentActive) return
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.group.importKeys(targetID))
    if (!componentActive) return
    completed.value = true
    draft.keys = ''
    recovery.clear()
    await router.push({ name: 'group-detail', params: { id: targetID } })
    return
  }
  if (!componentActive) return
  if (outcome.kind === 'failed' && outcome.reason === 'rejected') {
    appendOperation.reset()
    errorKey.value = 'import.appendFailed'
  }
}

async function retryOperation(): Promise<void> {
  if (appendOperation.operation.value) {
    const groupID = appendOperation.operation.value.payload.groupID
    await appendToGroup(groupID)
    return
  }
  if (createOperation.operation.value) await submitCreate()
}

async function returnToEdit(): Promise<void> {
  createOperation.reset()
  appendOperation.reset()
  conflict.value = null
  await moveToStep(1)
}

onBeforeUnmount(() => {
  componentActive = false
  discoveryController?.abort()
  discoveryController = undefined
  unregisterRecovery()
})
</script>

<template>
  <div class="import-workflow">
    <ImportOperationNotice
      :message-key="operationNoticeKey"
      :resource-identity="operationResourceIdentity"
      :can-retry="canRetryOperation"
      :pending="pending"
      @retry="retryOperation"
    />

    <ol class="stepper" :aria-label="t('import.progress')">
      <li
        v-for="number in [1, 2, 3]"
        :key="number"
        :class="{ active: draft.step === number, done: draft.step > number }"
      >
        <span
          ><Check v-if="draft.step > number" :size="14" aria-hidden="true" /><template v-else>{{
            number
          }}</template></span
        >
        {{ t(`import.steps.${number}`) }}
      </li>
    </ol>

    <ImportConnectionStep
      v-if="draft.step === 1"
      ref="connectionStep"
      :preset-id="draft.preset_id"
      :name="draft.name"
      :upstream-url="draft.upstream_url"
      :protocols="draft.protocols"
      :keys="draft.keys"
      :header-rules="draft.header_rules"
      :pending="pending"
      :can-discover="canDiscover"
      @apply-preset="applyPreset"
      @update:name="draft.name = $event"
      @update:upstream-url="draft.upstream_url = $event"
      @toggle-protocol="toggleProtocol"
      @update:keys="draft.keys = $event"
      @update:header-rules="draft.header_rules = $event"
      @header-rules-valid="headerRulesValid = $event"
      @discover="runDiscovery"
    />

    <ImportModelsStep
      v-else-if="draft.step === 2"
      ref="modelsStep"
      :discovery-failed="discoveryFailed"
      :manual-mode="manualMode"
      :error-key="errorKey"
      :models="draft.models"
      :can-review="canReview"
      @manual="showManualPath"
      @update:models="draft.models = $event"
      @back="moveToStep(1)"
      @review="moveToStep(3)"
    />

    <ImportReviewStep
      v-else
      ref="reviewStep"
      :name="draft.name"
      :upstream-url="draft.upstream_url"
      :protocols="draft.protocols"
      :key-count="keyAnalysis.nonEmptyCount"
      :models="draft.models"
      :error-key="errorKey"
      :conflict="conflict"
      :pending="pending"
      :operation-notice-active="operationNoticeKey !== ''"
      @append="appendToGroup"
      @separate="submitCreate(true)"
      @edit="returnToEdit"
      @back="moveToStep(2)"
      @create="submitCreate(false)"
    />
  </div>
</template>

<style scoped>
.import-workflow {
  display: grid;
  gap: var(--space-5);
  max-width: 920px;
  margin: 0 auto;
}
.stepper {
  display: flex;
  margin: 0;
  padding: 0;
  list-style: none;
}
.stepper li {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.stepper li:not(:last-child)::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--color-border);
  margin-inline: var(--space-3);
}
.stepper li > span {
  display: grid;
  width: 26px;
  height: 26px;
  flex: none;
  place-items: center;
  border: 1px solid var(--color-border);
  border-radius: 50%;
  font-family: ui-monospace, monospace;
}
.stepper li.active {
  color: var(--color-text);
  font-weight: 650;
}
.stepper li.active > span {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: var(--color-primary-ink);
}
.stepper li.done > span {
  border-color: transparent;
  background: var(--color-success-bg);
  color: var(--color-success);
}
@media (max-width: 640px) {
  .stepper li {
    font-size: 0;
  }
}
</style>
