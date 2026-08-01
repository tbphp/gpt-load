<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { LockKeyhole } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { ApiError, InvalidResponseError } from '@/api/errors'
import {
  groupOptionsQueryOptions,
  groupSummaryQueryOptions,
  importGroupKeys,
} from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { groupDetailLocation, importLocation } from '@/app/route-locations'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ImportOperationNotice from './ImportOperationNotice.vue'
import { useImportOperationOwner } from './import-operation-owner'
import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import KeyTextarea from './KeyTextarea.vue'
import type { ExistingGroupImportDraft } from './model-draft'

const props = defineProps<{ initialDraft?: ExistingGroupImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const completed = ref(false)
const errorKey = ref('')
const submissionError = ref<HTMLElement>()
const importOperationOwner = useImportOperationOwner()
const operation = importOperationOwner.importKeys
const stableInitialDraft = operation.operation.value?.payload.draft
const keys = ref(
  stableInitialDraft?.mode === 'existing'
    ? stableInitialDraft.keys
    : (props.initialDraft?.keys ?? ''),
)
if (operation.outcome.value?.kind === 'confirmed') operation.reset()
const pending = operation.pending
const payloadLocked = computed(() => operation.operation.value !== null)
const operationNoticeKey = computed(() => {
  const outcome = operation.outcome.value
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
  const outcome = operation.outcome.value
  return outcome?.kind === 'failed' && outcome.reason === 'expired-known'
    ? outcome.resource_identity
    : ''
})
const selectorPlaceholder = '__select_group__'
let componentActive = true

function parsePositiveID(value: unknown): number | undefined {
  if (typeof value !== 'string' || !/^[1-9]\d*$/u.test(value)) return undefined
  const id = Number(value)
  return Number.isSafeInteger(id) ? id : undefined
}

const hasFixedTargetIntent = computed(() =>
  Object.prototype.hasOwnProperty.call(route.query, 'group_id'),
)
const routeGroupID = computed(() =>
  hasFixedTargetIntent.value ? parsePositiveID(route.query.group_id) : undefined,
)
const operationGroupID = computed(() => operation.operation.value?.payload.groupID)
const hasLockedTarget = computed(
  () => hasFixedTargetIntent.value || operationGroupID.value !== undefined,
)
const targetGroupID = computed(() => operationGroupID.value ?? routeGroupID.value)
const groupsQuery = useQuery(groupOptionsQueryOptions(api))
const summaryQuery = useQuery(groupSummaryQueryOptions(api, targetGroupID))
const fixedGroupMissing = computed(
  () => summaryQuery.error.value instanceof ApiError && summaryQuery.error.value.status === 404,
)
const fixedGroup = computed(() => {
  if (fixedGroupMissing.value) return null
  const group = summaryQuery.data.value
  return group && group.id === targetGroupID.value ? group : null
})
const canReturnToSelector = computed(
  () =>
    operationGroupID.value === undefined &&
    hasFixedTargetIntent.value &&
    (routeGroupID.value === undefined ||
      (!summaryQuery.isPending.value && fixedGroup.value === null)),
)
const selectorOptions = computed(() => [
  { value: selectorPlaceholder, label: t('import.existing.groupPlaceholder') },
  ...(groupsQuery.data.value ?? []).map((group) => ({
    value: String(group.id),
    label: t('import.existing.groupOption', { id: group.id, name: group.name }),
  })),
])
const keyAnalysis = computed(() => analyzeKeys(keys.value))
const canSubmit = computed(
  () =>
    !payloadLocked.value &&
    !pending.value &&
    fixedGroup.value !== null &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys,
)
const dirty = computed(() => !completed.value && keys.value !== '')
const actionSummary = computed(() =>
  fixedGroup.value
    ? t('import.existing.actionSummary', { name: fixedGroup.value.name })
    : t('import.existing.actionSelectTarget'),
)

const unsavedChanges = useUnsavedChanges(dirty, { blocked: pending })
const unregisterRecovery = recovery.register(() =>
  completed.value
    ? null
    : operation.operation.value?.payload.draft.mode === 'existing'
      ? operation.operation.value.payload.draft
      : {
          mode: 'existing',
          group_id: targetGroupID.value ?? null,
          keys: keys.value,
        },
)

async function selectGroup(value: string): Promise<void> {
  if (payloadLocked.value) return
  if (value === selectorPlaceholder) return
  const id = parsePositiveID(value)
  if (id === undefined) return
  errorKey.value = ''
  await unsavedChanges.runWithoutPrompt(() =>
    router.push(importLocation({ mode: 'existing', group_id: String(id) })),
  )
}

async function returnToSelector(): Promise<void> {
  if (payloadLocked.value) return
  errorKey.value = ''
  await unsavedChanges.runWithoutPrompt(() => router.push(importLocation({ mode: 'existing' })))
}

async function submit(): Promise<void> {
  const groupID = targetGroupID.value
  if (groupID === undefined || !fixedGroup.value || !canSubmit.value) return
  if (
    !importOperationOwner.beginImportKeys({ groupID, keys: keys.value }, 'existing', {
      mode: 'existing',
      group_id: groupID,
      keys: keys.value,
    })
  ) {
    return
  }
  await executeImportOperation()
}

async function executeImportOperation(): Promise<void> {
  const current = operation.operation.value
  if (!current) return
  errorKey.value = ''
  const outcome = await operation.execute(async (stableOperation, signal) => {
    const imported = await importGroupKeys(
      api,
      stableOperation.payload.groupID,
      { keys: stableOperation.payload.keys },
      stableOperation.idempotencyKey,
      signal,
    )
    if (imported.group_id !== stableOperation.payload.groupID) throw new InvalidResponseError()
    return imported
  })
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = current.payload.groupID
    completed.value = true
    keys.value = ''
    recovery.clear()
    operation.reset()
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.group.importKeys(targetID))
    if (!componentActive) return
    await router.push(groupDetailLocation(targetID))
    return
  }
  if (!componentActive) return
  if (outcome.kind === 'failed' && outcome.reason === 'rejected') {
    operation.reset()
    errorKey.value = 'import.existing.importFailed'
    await nextTick()
    submissionError.value?.focus()
  }
}

async function abandonOperation(): Promise<void> {
  if (pending.value || !payloadLocked.value) return
  if (!(await unsavedChanges.confirmDiscard()) || pending.value) return
  operation.reset()
  errorKey.value = ''
}

onBeforeUnmount(() => {
  componentActive = false
  unregisterRecovery()
})
</script>

<template>
  <div class="existing-import">
    <ImportOperationNotice
      :message-key="operationNoticeKey"
      :resource-identity="operationResourceIdentity"
      :can-retry="operation.canRetry.value"
      :can-abandon="payloadLocked && !pending"
      :pending="pending"
      @retry="executeImportOperation"
      @abandon="abandonOperation"
    />

    <InlineFeedback tone="info" appearance="hint">
      {{ t('import.existing.description') }}
    </InlineFeedback>

    <section class="existing-import__target" aria-labelledby="existing-target-heading">
      <header class="existing-import__section-header">
        <div>
          <h2 id="existing-target-heading">{{ t('import.existing.title') }}</h2>
          <p>
            {{
              t(
                hasLockedTarget
                  ? 'import.existing.fixedDescription'
                  : 'import.existing.selectorDescription',
              )
            }}
          </p>
        </div>
        <AppButton
          v-if="canReturnToSelector"
          variant="secondary"
          size="compact"
          :disabled="payloadLocked"
          @click="returnToSelector"
        >
          {{ t('import.existing.backToSelector') }}
        </AppButton>
      </header>

      <template v-if="!hasLockedTarget">
        <InlineFeedback v-if="groupsQuery.isPending.value" tone="info">
          {{ t('import.existing.groupsLoading') }}
        </InlineFeedback>
        <div
          v-else-if="groupsQuery.isError.value && !groupsQuery.data.value"
          class="existing-import__query-error"
        >
          <InlineFeedback tone="danger">{{ t('import.existing.groupsFailed') }}</InlineFeedback>
          <AppButton variant="secondary" size="compact" @click="groupsQuery.refetch()">
            {{ t('common.retry') }}
          </AppButton>
        </div>
        <template v-else>
          <InlineFeedback v-if="groupsQuery.isError.value" tone="warning">
            {{ t('import.existing.groupsStale') }}
          </InlineFeedback>
          <InlineFeedback v-if="groupsQuery.data.value?.length === 0" tone="info">
            {{ t('import.existing.groupsEmpty') }}
          </InlineFeedback>
          <FormField
            v-else
            id="existing-group-select"
            :label="t('import.existing.groupLabel')"
            :description="t('import.existing.selectorHelp')"
            required
            :required-text="t('import.required')"
          >
            <template #default="field">
              <AppSelect
                id="existing-group-select"
                :model-value="selectorPlaceholder"
                :label="t('import.existing.groupLabel')"
                :options="selectorOptions"
                :disabled="payloadLocked"
                :aria-describedby="field.describedBy"
                @update:model-value="selectGroup"
              />
            </template>
          </FormField>
        </template>
      </template>

      <InlineFeedback v-else-if="targetGroupID === undefined" tone="danger">
        {{ t('import.existing.invalidGroupID') }}
      </InlineFeedback>
      <InlineFeedback v-else-if="summaryQuery.isPending.value" tone="info">
        {{ t('import.existing.targetLoading') }}
      </InlineFeedback>
      <div v-else-if="!fixedGroup" class="existing-import__query-error">
        <InlineFeedback tone="danger">
          {{
            t(
              fixedGroupMissing
                ? 'import.existing.groupNotFound'
                : 'import.existing.groupLoadFailed',
              { id: targetGroupID },
            )
          }}
        </InlineFeedback>
        <AppButton variant="secondary" size="compact" @click="summaryQuery.refetch()">
          {{ t('common.retry') }}
        </AppButton>
      </div>
      <template v-else>
        <InlineFeedback v-if="summaryQuery.isError.value" tone="warning">
          {{ t('import.existing.targetStale') }}
        </InlineFeedback>
        <div class="existing-import__locked-heading">
          <LockKeyhole :size="18" aria-hidden="true" />
          <strong>#{{ fixedGroup.id }} · {{ fixedGroup.name }}</strong>
          <span>{{ t('import.existing.targetLocked') }}</span>
        </div>
        <dl class="existing-import__summary">
          <div>
            <dt>{{ t('import.existing.status') }}</dt>
            <dd>
              <StatusBadge :status="fixedGroup.service_status">
                {{ t(`groups.collection.status.${fixedGroup.service_status}`) }}
              </StatusBadge>
            </dd>
          </div>
          <div>
            <dt>{{ t('import.existing.upstream') }}</dt>
            <dd>
              <code>{{ fixedGroup.upstream_url }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('import.existing.protocols') }}</dt>
            <dd class="existing-import__protocols">
              <code v-for="protocol in fixedGroup.protocols" :key="protocol">{{ protocol }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('import.existing.modelsAndKeys') }}</dt>
            <dd>
              {{
                t('import.existing.modelsAndKeysSummary', {
                  models: fixedGroup.model_count,
                  keys: fixedGroup.key_count,
                })
              }}
            </dd>
          </div>
        </dl>
      </template>
    </section>

    <template v-if="!hasLockedTarget || fixedGroup">
      <KeyTextarea v-model="keys" :disabled="payloadLocked" />

      <div v-if="errorKey" ref="submissionError" class="existing-import__error" tabindex="-1">
        <InlineFeedback tone="danger">{{ t(errorKey) }}</InlineFeedback>
      </div>

      <footer class="existing-import__actions">
        <div aria-live="polite">
          <strong>{{ actionSummary }}</strong>
          <span>{{ t('import.existing.actionHelp') }}</span>
        </div>
        <AppButton :busy="pending" :disabled="!canSubmit" @click="submit">
          {{ t('import.existing.submit') }}
        </AppButton>
      </footer>
    </template>
  </div>
</template>

<style scoped>
.existing-import {
  min-width: 0;
}

.existing-import :deep(.operation-notice) {
  margin-top: var(--space-5);
}

.existing-import > .inline-feedback {
  margin-top: var(--space-5);
}

.existing-import__target {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-5) 0 var(--space-6);
}

.existing-import__section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.existing-import__section-header h2,
.existing-import__section-header p {
  margin: 0;
}

.existing-import__section-header h2 {
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.existing-import__section-header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.existing-import__target :deep(.form-field) {
  max-width: 620px;
}

.existing-import__target :deep(.app-select__trigger) {
  width: 100%;
}

.existing-import__query-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.existing-import__query-error > .inline-feedback {
  flex: 1;
}

.existing-import__locked-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text);
}

.existing-import__locked-heading strong {
  min-width: 0;
  overflow-wrap: anywhere;
}

.existing-import__locked-heading > span {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

.existing-import__summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  margin: 0;
  border-top: 1px solid var(--color-border-subtle);
  border-left: 1px solid var(--color-border-subtle);
}

.existing-import__summary > div {
  min-width: 0;
  border-right: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-3);
}

.existing-import__summary dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.existing-import__summary dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
}

.existing-import__summary code {
  font-family: var(--font-mono);
}

.existing-import__protocols {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.existing-import__protocols code {
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  padding: var(--space-1) var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.existing-import__error {
  margin-top: var(--space-5);
  outline: none;
}

.existing-import__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) 0 0;
}

.existing-import__actions > div {
  min-width: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.existing-import__actions strong,
.existing-import__actions span {
  display: block;
}

.existing-import__actions strong {
  color: var(--color-text);
  font-weight: 560;
}

.existing-import__actions span {
  margin-top: var(--space-1);
}

@media (max-width: 640px) {
  .existing-import__section-header,
  .existing-import__query-error,
  .existing-import__actions {
    align-items: stretch;
    flex-direction: column;
  }

  .existing-import__summary {
    grid-template-columns: 1fr;
  }

  .existing-import__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
