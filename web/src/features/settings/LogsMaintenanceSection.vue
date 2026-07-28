<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { FileClock, Save } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { getSettings, projectSettingsConflict, updateSettings } from '@/api/control/settings'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
import {
  buildSettingsPatch,
  createSettingsDraft,
  isValidRetention,
  replaceDraftFieldFromSettings,
  setSettingsOverride,
  validateSettingsSection,
  type SettingsDraft,
} from './settings-patch'
import {
  chooseSettingsMutationResult,
  mergeSettingsConflict,
  reconcileSettingsMutation,
  type SettingsMergeConflict,
} from './settings-response'

const props = defineProps<{ resource: SettingsResource }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const base = ref(props.resource)
const draft = ref<SettingsDraft>(createSettingsDraft(props.resource.settings))
const pending = ref(false)
const failed = ref(false)
const succeeded = ref(false)
const concurrent = ref(false)
const indeterminate = ref(false)
const reconciling = ref(false)
const conflicts = ref<SettingsMergeConflict[]>([])
let controller: AbortController | undefined
let unknownOperation: { base: SettingsResource; draft: SettingsDraft } | undefined

const patch = computed(() =>
  buildSettingsPatch(base.value.settings, draft.value, 'logs-maintenance'),
)
const dirty = computed(() => Object.keys(patch.value).length > 0)
const operationLocked = computed(() => pending.value || indeterminate.value || reconciling.value)
const valid = computed(
  () => conflicts.value.length === 0 && validateSettingsSection(draft.value, 'logs-maintenance'),
)
const owned = computed(() => draft.value.overrides.has('request_log_retention_days'))
const error = computed(() =>
  owned.value && !isValidRetention(draft.value.values.request_log_retention_days)
    ? t('settings.logs.retentionError')
    : undefined,
)

function rebase(resource: SettingsResource): void {
  base.value = resource
  draft.value = createSettingsDraft(resource.settings)
  failed.value = false
  concurrent.value = false
  conflicts.value = []
  indeterminate.value = false
  reconciling.value = false
  unknownOperation = undefined
}

function acceptExternalSettings(resource: SettingsResource): void {
  if (resource.settings_etag === base.value.settings_etag) return
  if (unknownOperation) {
    void applyUnknownLatest(resource)
    return
  }
  if (dirty.value) {
    const merged = mergeSettingsConflict(base.value, draft.value, resource, 'logs-maintenance')
    base.value = merged.resource
    draft.value = merged.draft
    conflicts.value = merged.conflicts
    concurrent.value = true
    return
  }
  rebase(resource)
}
watch(() => props.resource, acceptExternalSettings)

function setOwned(enabled: boolean): void {
  draft.value = setSettingsOverride(
    base.value.settings,
    draft.value,
    'request_log_retention_days',
    enabled,
  )
  conflicts.value = []
  concurrent.value = false
  succeeded.value = false
}

function setValue(value: number): void {
  draft.value = {
    values: { ...draft.value.values, request_log_retention_days: value },
    overrides: new Set(draft.value.overrides),
  }
  conflicts.value = []
  concurrent.value = false
  succeeded.value = false
}

function chooseMine(): void {
  conflicts.value = []
  concurrent.value = false
}

function chooseLatest(): void {
  draft.value = replaceDraftFieldFromSettings(
    draft.value,
    base.value.settings,
    'request_log_retention_days',
  )
  conflicts.value = []
  concurrent.value = false
}

async function applyUnknownLatest(latest: SettingsResource): Promise<void> {
  const operation = unknownOperation
  if (!operation) return
  const result = reconcileSettingsMutation(
    operation.base,
    operation.draft,
    latest,
    'logs-maintenance',
  )
  base.value = result.resource
  draft.value = result.draft
  conflicts.value = result.conflicts
  queryClient.setQueryData(controlQueryKeys.settings(), latest)

  if (result.kind === 'confirmed') {
    unknownOperation = undefined
    indeterminate.value = false
    concurrent.value = false
    succeeded.value = true
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.details() })
    return
  }
  if (result.kind === 'conflict') {
    unknownOperation = undefined
    indeterminate.value = false
    concurrent.value = true
  }
}

async function reconcileUnknownOperation(activeController: AbortController): Promise<void> {
  if (!unknownOperation || controller !== activeController) return
  reconciling.value = true
  try {
    const latest = await getSettings(client, activeController.signal)
    if (controller !== activeController) return
    await applyUnknownLatest(latest)
  } catch (error: unknown) {
    if (controller !== activeController || error instanceof RequestCancelledError) return
    indeterminate.value = true
  } finally {
    if (controller === activeController) reconciling.value = false
  }
}

async function checkUnknownResult(): Promise<void> {
  if (!unknownOperation || reconciling.value) return
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  await reconcileUnknownOperation(activeController)
  if (controller === activeController) controller = undefined
}

async function save(): Promise<void> {
  if (operationLocked.value || !valid.value) return
  const normalizedPatch = buildSettingsPatch(base.value.settings, draft.value, 'logs-maintenance')
  if (Object.keys(normalizedPatch).length === 0) return
  const operationBase = base.value
  const operationDraft = draft.value

  pending.value = true
  failed.value = false
  succeeded.value = false
  concurrent.value = false
  indeterminate.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const resource = await updateSettings(
      client,
      normalizedPatch,
      operationBase.settings_etag,
      activeController.signal,
    )
    if (controller !== activeController) return
    await queryClient.cancelQueries({ queryKey: controlQueryKeys.settings(), exact: true })
    if (controller !== activeController) return

    const cached = queryClient.getQueryData<SettingsResource>(controlQueryKeys.settings())
    const decision = chooseSettingsMutationResult(
      resource,
      cached,
      operationBase,
      operationDraft,
      'logs-maintenance',
    )
    if (decision.kind === 'refetch') {
      await queryClient.refetchQueries({
        queryKey: controlQueryKeys.settings(),
        exact: true,
      })
      if (controller !== activeController) return
      return
    }

    base.value = decision.resource
    draft.value = decision.draft
    queryClient.setQueryData(controlQueryKeys.settings(), decision.resource)
    unknownOperation = undefined
    succeeded.value = true
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.details() })
    if (controller !== activeController) return
  } catch (error: unknown) {
    if (controller !== activeController || error instanceof RequestCancelledError) return
    const latest = projectSettingsConflict(error)
    if (latest) {
      const merged = mergeSettingsConflict(
        operationBase,
        operationDraft,
        latest,
        'logs-maintenance',
      )
      base.value = merged.resource
      draft.value = merged.draft
      conflicts.value = merged.conflicts
      concurrent.value = true
      queryClient.setQueryData(controlQueryKeys.settings(), latest)
      return
    }
    const outcome = classifyMutationOutcome<SettingsResource>({
      kind: 'error',
      error,
      requestSent: true,
    })
    if (outcome.kind === 'failed') {
      failed.value = true
      return
    }
    unknownOperation = { base: operationBase, draft: operationDraft }
    indeterminate.value = true
    await reconcileUnknownOperation(activeController)
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
}

onBeforeUnmount(() => {
  controller?.abort()
  controller = undefined
  unknownOperation = undefined
})
</script>

<template>
  <SurfaceCard class="settings-card logs-maintenance">
    <header class="settings-card__heading">
      <div class="settings-card__title">
        <span class="settings-card__icon"><FileClock :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.logs.title') }}</h2>
          <p>{{ t('settings.logs.description') }}</p>
        </div>
      </div>
      <AppButton
        data-test="logs-maintenance-save"
        :busy="pending"
        :disabled="!dirty || !valid || operationLocked"
        @click="save"
      >
        <Save :size="16" aria-hidden="true" />{{ t('settings.save') }}
      </AppButton>
    </header>
    <InlineFeedback v-if="failed" tone="danger">{{ t('settings.logs.saveFailed') }}</InlineFeedback>
    <InlineFeedback v-if="succeeded" tone="info">{{ t('settings.saved') }}</InlineFeedback>
    <InlineFeedback v-if="reconciling" data-test="settings-reconciling" tone="info">{{
      t('settings.outcome.reconciling')
    }}</InlineFeedback>
    <div v-else-if="indeterminate" data-test="settings-indeterminate">
      <InlineFeedback tone="warning">{{ t('settings.outcome.indeterminate') }}</InlineFeedback>
      <AppButton data-test="settings-check-result" variant="secondary" @click="checkUnknownResult">
        {{ t('settings.outcome.checkResult') }}
      </AppButton>
    </div>
    <InlineFeedback v-if="concurrent" tone="warning">{{
      conflicts.length > 0 ? t('settings.conflict.blocked') : t('settings.conflict.rebased')
    }}</InlineFeedback>
    <section v-if="conflicts.length > 0" class="settings-conflict" data-test="settings-conflicts">
      <strong>{{ t('settings.logs.retention') }}</strong>
      <span>
        {{ t('settings.conflict.mine') }}:
        {{ conflicts[0]?.mine.normalized_value }}
      </span>
      <span>
        {{ t('settings.conflict.latest') }}:
        {{ conflicts[0]?.latest.normalized_value }}
      </span>
      <div>
        <AppButton variant="secondary" @click="chooseMine">{{
          t('settings.conflict.useMine')
        }}</AppButton>
        <AppButton variant="ghost" @click="chooseLatest">{{
          t('settings.conflict.useLatest')
        }}</AppButton>
      </div>
    </section>
    <SettingOverrideField
      setting-key="request_log_retention_days"
      :label="t('settings.logs.retention')"
      :description="t('settings.logs.retentionDescription')"
      :effective-value="base.settings.values.request_log_retention_days"
      :owned="owned"
      :model-value="draft.values.request_log_retention_days"
      :error="error"
      :min="1"
      :max="365"
      :disabled="operationLocked"
      @update:owned="setOwned"
      @update:model-value="setValue"
    />
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-4);
}
.settings-card__title {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.settings-card__heading :deep(.app-button) {
  gap: var(--space-2);
}
.settings-conflict {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.settings-conflict > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
@media (max-width: 640px) {
  .settings-card__heading {
    grid-template-columns: 1fr;
  }
  .settings-card__heading :deep(.app-button) {
    width: 100%;
  }
}
</style>
