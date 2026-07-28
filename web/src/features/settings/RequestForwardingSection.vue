<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { ChevronDown, Save, SlidersHorizontal } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  getSettings,
  projectSettingsConflict,
  updateSettings,
  type RuntimeSettingKey,
  type TimeoutSettingKey,
} from '@/api/control/settings'
import type { HeaderRulesDto } from '@/api/control/groups'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
import {
  buildSettingsPatch,
  createSettingsDraft,
  hasDuplicateHeaderNames,
  isValidTimeout,
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
const headerSaveError = ref(false)
const disclosureRequested = ref(props.resource.settings.overrides.includes('header_rules'))
const headerValid = ref(!hasDuplicateHeaderNames(props.resource.settings.values.header_rules))
let controller: AbortController | undefined
let unknownOperation: { base: SettingsResource; draft: SettingsDraft } | undefined

const timeoutKeys: TimeoutSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const patch = computed(() =>
  buildSettingsPatch(base.value.settings, draft.value, 'request-forwarding'),
)
const dirty = computed(() => Object.keys(patch.value).length > 0)
const operationLocked = computed(() => pending.value || indeterminate.value || reconciling.value)
const valid = computed(
  () =>
    conflicts.value.length === 0 &&
    (!draft.value.overrides.has('header_rules') || headerValid.value) &&
    validateSettingsSection(draft.value, 'request-forwarding'),
)
const headerDirty = computed(() =>
  Object.prototype.hasOwnProperty.call(patch.value, 'header_rules'),
)
const headerOpen = computed(
  () =>
    disclosureRequested.value ||
    draft.value.overrides.has('header_rules') ||
    headerDirty.value ||
    (!headerValid.value && draft.value.overrides.has('header_rules')) ||
    headerSaveError.value,
)

function rebase(resource: SettingsResource): void {
  base.value = resource
  draft.value = createSettingsDraft(resource.settings)
  headerValid.value = !hasDuplicateHeaderNames(resource.settings.values.header_rules)
  disclosureRequested.value = resource.settings.overrides.includes('header_rules')
  failed.value = false
  concurrent.value = false
  conflicts.value = []
  indeterminate.value = false
  reconciling.value = false
  unknownOperation = undefined
  headerSaveError.value = false
}

function acceptExternalSettings(resource: SettingsResource): void {
  if (resource.settings_etag === base.value.settings_etag) return
  if (unknownOperation) {
    void applyUnknownLatest(resource)
    return
  }
  if (dirty.value) {
    const merged = mergeSettingsConflict(base.value, draft.value, resource, 'request-forwarding')
    base.value = merged.resource
    draft.value = merged.draft
    conflicts.value = merged.conflicts
    concurrent.value = true
    headerSaveError.value = merged.conflicts.some((conflict) => conflict.key === 'header_rules')
    headerValid.value = !hasDuplicateHeaderNames(draft.value.values.header_rules)
    return
  }
  rebase(resource)
}

watch(() => props.resource, acceptExternalSettings)

function clearConflict(key: RuntimeSettingKey): void {
  conflicts.value = conflicts.value.filter((conflict) => conflict.key !== key)
  if (conflicts.value.length === 0) concurrent.value = false
}

function hasOverride(key: TimeoutSettingKey | 'header_rules'): boolean {
  return draft.value.overrides.has(key)
}

function setOverride(key: TimeoutSettingKey, enabled: boolean): void {
  draft.value = setSettingsOverride(base.value.settings, draft.value, key, enabled)
  clearConflict(key)
  succeeded.value = false
}

function setTimeoutValue(key: TimeoutSettingKey, value: number): void {
  draft.value = {
    values: { ...draft.value.values, [key]: value },
    overrides: new Set(draft.value.overrides),
  }
  clearConflict(key)
  succeeded.value = false
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(draft.value.values[key])
    ? t('settings.request.timeoutError')
    : undefined
}

function setHeaderOverride(enabled: boolean): void {
  draft.value = setSettingsOverride(base.value.settings, draft.value, 'header_rules', enabled)
  clearConflict('header_rules')
  if (enabled) disclosureRequested.value = true
  succeeded.value = false
}

function setHeaderRules(value: HeaderRulesDto): void {
  draft.value = {
    values: { ...draft.value.values, header_rules: value },
    overrides: new Set(draft.value.overrides),
  }
  clearConflict('header_rules')
  succeeded.value = false
}

function toggleDisclosure(): void {
  disclosureRequested.value = !headerOpen.value
}

function chooseMine(key: RuntimeSettingKey): void {
  clearConflict(key)
}

function chooseLatest(key: RuntimeSettingKey): void {
  draft.value = replaceDraftFieldFromSettings(draft.value, base.value.settings, key)
  clearConflict(key)
}

function conflictValue(conflict: SettingsMergeConflict, side: 'mine' | 'latest'): string {
  const identity = conflict[side]
  if (!identity.is_override) return t('settings.default')
  if (conflict.key === 'header_rules') {
    const rules = identity.normalized_value as { set: Record<string, string>; remove: string[] }
    return t('settings.conflict.headerRulesSummary', {
      set: Object.keys(rules.set).length,
      remove: rules.remove.length,
    })
  }
  return String(identity.normalized_value)
}

function conflictLabel(key: RuntimeSettingKey): string {
  return key === 'header_rules' ? t('settings.request.headerRules') : t(`settings.request.${key}`)
}

async function applyUnknownLatest(latest: SettingsResource): Promise<void> {
  const operation = unknownOperation
  if (!operation) return
  const result = reconcileSettingsMutation(
    operation.base,
    operation.draft,
    latest,
    'request-forwarding',
  )
  base.value = result.resource
  draft.value = result.draft
  conflicts.value = result.conflicts
  headerValid.value = !hasDuplicateHeaderNames(result.draft.values.header_rules)
  headerSaveError.value = result.conflicts.some((conflict) => conflict.key === 'header_rules')
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
  const normalizedPatch = buildSettingsPatch(base.value.settings, draft.value, 'request-forwarding')
  if (Object.keys(normalizedPatch).length === 0) return
  const operationBase = base.value
  const operationDraft = draft.value

  pending.value = true
  failed.value = false
  succeeded.value = false
  concurrent.value = false
  indeterminate.value = false
  headerSaveError.value = false
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
      'request-forwarding',
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
    headerValid.value = !hasDuplicateHeaderNames(decision.draft.values.header_rules)
    disclosureRequested.value = decision.resource.settings.overrides.includes('header_rules')
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
        'request-forwarding',
      )
      base.value = merged.resource
      draft.value = merged.draft
      conflicts.value = merged.conflicts
      concurrent.value = true
      headerSaveError.value = merged.conflicts.some((conflict) => conflict.key === 'header_rules')
      headerValid.value = !hasDuplicateHeaderNames(draft.value.values.header_rules)
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
      headerSaveError.value = Object.prototype.hasOwnProperty.call(normalizedPatch, 'header_rules')
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
  <SurfaceCard class="settings-card request-forwarding">
    <header class="settings-card__heading settings-card__heading--actions">
      <div class="settings-card__title">
        <span class="settings-card__icon"><SlidersHorizontal :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.request.title') }}</h2>
          <p>{{ t('settings.request.description') }}</p>
        </div>
      </div>
      <AppButton
        data-test="request-forwarding-save"
        :busy="pending"
        :disabled="!dirty || !valid || operationLocked"
        @click="save"
      >
        <Save :size="16" aria-hidden="true" />{{ t('settings.save') }}
      </AppButton>
    </header>

    <InlineFeedback v-if="failed" tone="danger">{{
      t('settings.request.saveFailed')
    }}</InlineFeedback>
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
    <ul v-if="conflicts.length > 0" class="settings-conflicts" data-test="settings-conflicts">
      <li v-for="conflict in conflicts" :key="conflict.key">
        <strong>{{ conflictLabel(conflict.key) }}</strong>
        <span>{{ t('settings.conflict.mine') }}: {{ conflictValue(conflict, 'mine') }}</span>
        <span>{{ t('settings.conflict.latest') }}: {{ conflictValue(conflict, 'latest') }}</span>
        <div>
          <AppButton variant="secondary" @click="chooseMine(conflict.key)">{{
            t('settings.conflict.useMine')
          }}</AppButton>
          <AppButton variant="ghost" @click="chooseLatest(conflict.key)">{{
            t('settings.conflict.useLatest')
          }}</AppButton>
        </div>
      </li>
    </ul>

    <div class="request-forwarding__fields">
      <SettingOverrideField
        v-for="key in timeoutKeys"
        :key="key"
        :setting-key="key"
        :label="t(`settings.request.${key}`)"
        :description="t('settings.request.seconds')"
        :effective-value="base.settings.values[key]"
        :owned="hasOverride(key)"
        :model-value="draft.values[key]"
        :error="timeoutError(key)"
        :min="1"
        :disabled="operationLocked"
        @update:owned="setOverride(key, $event)"
        @update:model-value="setTimeoutValue(key, $event)"
      />
    </div>

    <section class="request-forwarding__advanced">
      <button
        data-test="settings-header-disclosure"
        class="request-forwarding__disclosure"
        type="button"
        :aria-expanded="headerOpen"
        aria-controls="settings-header-rules"
        @click="toggleDisclosure"
      >
        <span>
          <strong>{{ t('settings.request.headerRules') }}</strong>
          <small>{{
            t('settings.request.headerSummary', {
              set: Object.keys(base.settings.values.header_rules.set).length,
              remove: base.settings.values.header_rules.remove.length,
            })
          }}</small>
        </span>
        <StatusBadge :tone="hasOverride('header_rules') ? 'warning' : 'neutral'">
          {{ hasOverride('header_rules') ? t('settings.override') : t('settings.default') }}
        </StatusBadge>
        <ChevronDown :size="18" aria-hidden="true" :class="{ rotated: headerOpen }" />
      </button>

      <div v-if="headerOpen" id="settings-header-rules" class="request-forwarding__advanced-body">
        <label class="request-forwarding__header-toggle">
          <input
            data-test="override-header_rules"
            type="checkbox"
            :checked="hasOverride('header_rules')"
            :disabled="operationLocked"
            @change="setHeaderOverride(($event.target as HTMLInputElement).checked)"
          />
          {{ t('settings.useOverride') }}
        </label>
        <InlineFeedback tone="warning">{{ t('settings.request.headerWarning') }}</InlineFeedback>
        <div v-if="hasOverride('header_rules')" data-test="header-rules-editor">
          <HeaderRulesEditor
            :model-value="draft.values.header_rules"
            :disabled="operationLocked"
            @update:model-value="setHeaderRules"
            @update:valid="headerValid = $event"
          />
        </div>
      </div>
    </section>
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading,
.request-forwarding__fields,
.request-forwarding__advanced,
.request-forwarding__advanced-body {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  gap: var(--space-4);
}
.settings-card__heading--actions {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
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
.request-forwarding__fields,
.request-forwarding__advanced-body {
  gap: var(--space-4);
}
.request-forwarding__advanced {
  gap: var(--space-3);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}
.settings-conflicts {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.settings-conflicts li {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.settings-conflicts li > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.request-forwarding__disclosure {
  display: grid;
  min-height: 44px;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  border: 0;
  background: transparent;
  color: var(--color-text);
  padding: 0;
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.request-forwarding__disclosure span:first-child {
  display: grid;
  gap: var(--space-1);
}
.request-forwarding__disclosure small {
  color: var(--color-text-muted);
}
.request-forwarding__disclosure svg {
  transition: transform var(--duration-fast) ease;
}
.request-forwarding__disclosure svg.rotated {
  transform: rotate(180deg);
}
.request-forwarding__header-toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
.request-forwarding__header-toggle input {
  width: 18px;
  height: 18px;
}
@media (max-width: 640px) {
  .settings-card__heading--actions {
    grid-template-columns: 1fr;
  }
  .settings-card__heading :deep(.app-button) {
    width: 100%;
  }
  .request-forwarding__disclosure {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .request-forwarding__disclosure :deep(.status-badge) {
    grid-column: 1;
    width: fit-content;
  }
  .request-forwarding__disclosure > svg {
    grid-column: 2;
    grid-row: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .request-forwarding__disclosure svg {
    transition: none;
  }
}
</style>
