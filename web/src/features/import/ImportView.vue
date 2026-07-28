<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import PageHeader from '@/components/ui/PageHeader.vue'

import { useImportOperationOwner } from './import-operation-owner'
import { useImportRecovery } from './import-recovery'
import type { ExistingGroupImportDraft, ImportDraft } from './model-draft'

const ExistingGroupImport = lazySurface(() => import('./ExistingGroupImport.vue'))
const NewGroupImport = lazySurface(() => import('./NewGroupImport.vue'))

const route = useRoute()
const router = useRouter()
const recovery = useImportRecovery()
const operationOwner = useImportOperationOwner()
const { t } = useI18n()
const recoveredDraft = ref(recovery.consume())
const rawMode = computed(() => route.query.mode)
const operationMode = operationOwner.operationMode
const activeMode = computed<'new' | 'existing'>(() => {
  if (operationMode.value) return operationMode.value
  if (recoveredDraft.value) return recoveredDraft.value.mode
  return rawMode.value === 'existing' ? 'existing' : 'new'
})
const recoveredNewDraft = computed<ImportDraft | null>(() =>
  recoveredDraft.value?.mode === 'new' ? recoveredDraft.value : null,
)
const recoveredExistingDraft = computed<ExistingGroupImportDraft | null>(() =>
  recoveredDraft.value?.mode === 'existing' ? recoveredDraft.value : null,
)

if (!operationMode.value) {
  if (recoveredDraft.value?.mode === 'existing') {
    const query: Record<string, string> = { mode: 'existing' }
    if (recoveredDraft.value.group_id !== null) {
      query.group_id = String(recoveredDraft.value.group_id)
    }
    void router.replace({ name: 'import', query })
  } else if (recoveredDraft.value?.mode === 'new') {
    void router.replace({ name: 'import', query: { mode: 'new' } })
  } else if (
    rawMode.value !== undefined &&
    rawMode.value !== 'new' &&
    rawMode.value !== 'existing'
  ) {
    void router.replace({ name: 'import', query: { mode: 'new' } })
  }
}

watch(
  operationMode,
  (mode) => {
    if (mode && rawMode.value !== mode) {
      void router.replace({ name: 'import', query: { mode } })
    }
  },
  { immediate: true },
)

function selectMode(mode: 'new' | 'existing'): void {
  if (operationMode.value) return
  if (activeMode.value === mode) return
  recoveredDraft.value = null
  void router.push({ name: 'import', query: { mode } })
}
</script>

<template>
  <div class="import-page">
    <PageHeader :title="t('import.title')" :description="t('import.description')" />
    <div class="mode-selector" :aria-label="t('import.mode.label')" role="group">
      <button
        data-test="mode-new"
        type="button"
        :aria-pressed="activeMode === 'new'"
        :disabled="operationMode !== null"
        @click="selectMode('new')"
      >
        {{ t('import.mode.new') }}
      </button>
      <button
        data-test="mode-existing"
        type="button"
        :aria-pressed="activeMode === 'existing'"
        :disabled="operationMode !== null"
        @click="selectMode('existing')"
      >
        {{ t('import.mode.existing') }}
      </button>
    </div>
    <NewGroupImport v-if="activeMode === 'new'" :initial-draft="recoveredNewDraft" />
    <ExistingGroupImport v-else :initial-draft="recoveredExistingDraft" />
  </div>
</template>

<style scoped>
.import-page {
  display: grid;
  gap: var(--space-6);
}
.mode-selector {
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  gap: var(--space-1);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  padding: var(--space-1);
}
.mode-selector button {
  min-height: 44px;
  border: 0;
  border-radius: calc(var(--radius-control) - 2px);
  background: transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) var(--space-4);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
  transition:
    background-color 180ms ease,
    color 180ms ease;
}
.mode-selector button[aria-pressed='true'] {
  background: var(--color-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-card);
}
@media (max-width: 480px) {
  .mode-selector {
    width: 100%;
  }
  .mode-selector button {
    flex: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .mode-selector button {
    transition: none;
  }
}
</style>
