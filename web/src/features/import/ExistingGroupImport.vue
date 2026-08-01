<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { InvalidResponseError } from '@/api/errors'
import { groupOptionsQueryOptions, importGroupKeys } from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { groupDetailLocation, importLocation } from '@/app/route-locations'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

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

const routeGroupID = computed(() =>
  Object.prototype.hasOwnProperty.call(route.query, 'group_id')
    ? parsePositiveID(route.query.group_id)
    : undefined,
)
const operationGroupID = computed(() => operation.operation.value?.payload.groupID)
const targetGroupID = computed(() => operationGroupID.value ?? routeGroupID.value)
const groupsQuery = useQuery(groupOptionsQueryOptions(api))
const selectedGroup = computed(() => {
  const id = targetGroupID.value
  return id === undefined
    ? null
    : (groupsQuery.data.value?.find((group) => group.id === id) ?? null)
})
const selectedGroupMissing = computed(
  () =>
    targetGroupID.value !== undefined &&
    groupsQuery.data.value !== undefined &&
    selectedGroup.value === null,
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
    selectedGroup.value !== null &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys,
)
const dirty = computed(() => !completed.value && keys.value !== '')
const actionSummary = computed(() =>
  selectedGroup.value
    ? t('import.existing.actionSummary', { name: selectedGroup.value.name })
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
  errorKey.value = ''
  if (value === selectorPlaceholder) {
    await unsavedChanges.runWithoutPrompt(() => router.push(importLocation({ mode: 'existing' })))
    return
  }
  const id = parsePositiveID(value)
  if (id === undefined) return
  await unsavedChanges.runWithoutPrompt(() =>
    router.push(importLocation({ mode: 'existing', group_id: String(id) })),
  )
}

async function submit(): Promise<void> {
  const groupID = targetGroupID.value
  if (groupID === undefined || !selectedGroup.value || !canSubmit.value) return
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

    <InlineFeedback tone="neutral" appearance="ledger-hint" glyph="i">
      {{ t('import.existing.description') }}
    </InlineFeedback>

    <section class="existing-import__target" aria-labelledby="existing-target-heading">
      <header class="existing-import__section-header">
        <h2 id="existing-target-heading">{{ t('import.existing.title') }}</h2>
        <div class="existing-import__section-actions">
          <span v-if="groupsQuery.data.value" class="existing-import__group-count">
            {{ t('import.existing.groupCount', { count: groupsQuery.data.value.length }) }}
          </span>
        </div>
      </header>

      <InlineFeedback v-if="groupsQuery.isPending.value && !groupsQuery.data.value" tone="info">
        {{ t('import.existing.groupsLoading') }}
      </InlineFeedback>
      <div
        v-else-if="groupsQuery.isError.value && !groupsQuery.data.value"
        class="existing-import__query-error"
      >
        <InlineFeedback tone="danger">
          {{ t('import.existing.groupsFailed') }}
        </InlineFeedback>
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
        <div class="existing-import__target-body">
          <FormField
            id="existing-group-select"
            :label="t('import.existing.groupLabel')"
            required
            :required-text="t('import.required')"
            size="compact"
          >
            <template #default="field">
              <AppSelect
                id="existing-group-select"
                :model-value="selectedGroup ? String(selectedGroup.id) : selectorPlaceholder"
                :label="t('import.existing.groupLabel')"
                :options="selectorOptions"
                size="sm"
                :disabled="payloadLocked || groupsQuery.data.value?.length === 0"
                :aria-describedby="field.describedBy"
                @update:model-value="selectGroup"
              />
            </template>
          </FormField>
          <div v-if="selectedGroup" class="existing-import__group-meta">
            <strong>
              {{
                t('import.existing.groupMeta', {
                  id: selectedGroup.id,
                  models: selectedGroup.models.length,
                })
              }}
            </strong>
            <span>{{ t('import.existing.groupUnchanged') }}</span>
          </div>
        </div>
        <InlineFeedback v-if="selectedGroupMissing" tone="danger">
          {{ t('import.existing.groupNotFound', { id: targetGroupID }) }}
        </InlineFeedback>
      </template>
    </section>

    <KeyTextarea
      v-model="keys"
      :disabled="payloadLocked"
      :show-header-description="false"
      :storage-description="t('import.existing.keyStorageNotice')"
      :duplicate-label="t('import.existing.batchDuplicates')"
      :show-upstream-notice="false"
      :rows="8"
    />

    <div v-if="errorKey" ref="submissionError" class="existing-import__error" tabindex="-1">
      <InlineFeedback tone="danger">{{ t(errorKey) }}</InlineFeedback>
    </div>

    <footer class="existing-import__actions">
      <div aria-live="polite">
        <strong>{{ actionSummary }}</strong>
        <span>{{ t('import.existing.actionHelp') }}</span>
      </div>
      <AppButton size="sm" :busy="pending" :disabled="!canSubmit" @click="submit">
        {{ t('import.existing.submit') }}
      </AppButton>
    </footer>
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
  margin-top: 22px;
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-muted);
  padding-bottom: 18px;
  font-size: 11px;
  line-height: 1.55;
}

.existing-import__target {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.existing-import__section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.existing-import__section-header h2 {
  margin: 0;
}

.existing-import__section-header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.existing-import__section-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.existing-import__group-count {
  display: inline-flex;
  min-height: 23px;
  align-items: center;
  gap: 6px;
  border-radius: 999px;
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
  padding: 2px var(--space-2);
  font-size: var(--text-label-xs);
  font-weight: 590;
  white-space: nowrap;
}

.existing-import__group-count::before {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: currentColor;
  content: '';
}

.existing-import__target-body {
  display: grid;
  grid-template-columns: minmax(280px, 0.66fr) minmax(300px, 1fr);
  align-items: end;
  gap: 18px;
}

.existing-import__target-body :deep(.app-select__trigger) {
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

.existing-import__group-meta {
  display: flex;
  min-height: var(--control-xs);
  min-width: 0;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-4);
  border-left: 1px solid var(--color-border-subtle);
  color: var(--color-text-faint);
  padding-left: 18px;
  font-size: 10.8px;
}

.existing-import__error {
  margin-top: var(--space-5);
}

.existing-import__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  min-height: 64px;
  padding: var(--space-4) 0 0;
}

.existing-import__actions > div {
  min-width: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.existing-import__actions strong,
.existing-import__actions span {
  display: block;
}

.existing-import__actions strong {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.existing-import__actions span {
  margin-top: 2px;
}

@media (max-width: 860px) {
  .existing-import__actions {
    align-items: stretch;
    flex-direction: column;
  }

  .existing-import__target-body {
    grid-template-columns: 1fr;
  }

  .existing-import__group-meta {
    border-left: 0;
    padding-left: 0;
  }

  .existing-import__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 640px) {
  .existing-import__section-header,
  .existing-import__section-actions,
  .existing-import__query-error {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
