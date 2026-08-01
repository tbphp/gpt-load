<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import { importLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'

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
const hasFixedTargetIntent = computed(() =>
  Object.prototype.hasOwnProperty.call(route.query, 'group_id'),
)
const operationMode = operationOwner.operationMode
const modeOptions = computed(() => [
  {
    value: 'new',
    label: t('import.mode.new'),
    disabled:
      hasFixedTargetIntent.value || (operationMode.value !== null && operationMode.value !== 'new'),
  },
  {
    value: 'existing',
    label: t('import.mode.existing'),
    disabled: operationMode.value !== null && operationMode.value !== 'existing',
  },
])
const activeMode = computed<'new' | 'existing'>(() => {
  if (operationMode.value) return operationMode.value
  if (hasFixedTargetIntent.value) return 'existing'
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
  if (hasFixedTargetIntent.value) {
    if (rawMode.value !== 'existing') {
      void router.replace(importLocation({ mode: 'existing', group_id: route.query.group_id }))
    }
  } else if (recoveredDraft.value?.mode === 'existing') {
    const query: Record<string, string> = { mode: 'existing' }
    if (recoveredDraft.value.group_id !== null) {
      query.group_id = String(recoveredDraft.value.group_id)
    }
    void router.replace(importLocation(query))
  } else if (recoveredDraft.value?.mode === 'new') {
    void router.replace(importLocation({ mode: 'new' }))
  } else if (rawMode.value !== 'new' && rawMode.value !== 'existing') {
    void router.replace(importLocation({ mode: 'new' }))
  }
}

watch(
  operationMode,
  (mode) => {
    if (mode && rawMode.value !== mode) {
      void router.replace(
        importLocation({
          mode,
          ...(mode === 'existing' && hasFixedTargetIntent.value
            ? { group_id: route.query.group_id }
            : {}),
        }),
      )
    }
  },
  { immediate: true },
)

function selectMode(mode: 'new' | 'existing'): void {
  if (operationMode.value) return
  if (activeMode.value === mode) return
  recoveredDraft.value = null
  void router.push(importLocation({ mode }))
}

function updateMode(mode: string): void {
  if (mode === 'new' || mode === 'existing') selectMode(mode)
}
</script>

<template>
  <PageFrame aria-labelledby="import-page-title">
    <LedgerSheet class="import-page">
      <PageHeader id="import-page-title" :title="t('import.title')">
        <template #actions>
          <SegmentedControl
            :model-value="activeMode"
            :label="t('import.mode.label')"
            :options="modeOptions"
            size="touch"
            @update:model-value="updateMode"
          />
        </template>
      </PageHeader>
      <NewGroupImport v-if="activeMode === 'new'" :initial-draft="recoveredNewDraft" />
      <ExistingGroupImport v-else :initial-draft="recoveredExistingDraft" />
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.import-page {
  min-height: calc(100vh - var(--topbar-height) - var(--stage-padding-top));
}

@media (max-width: 560px) {
  .import-page :deep(.page-header) {
    align-items: stretch;
    flex-direction: column;
  }

  .import-page :deep(.page-header__actions),
  .import-page :deep(.segmented-control),
  .import-page :deep(.segmented-control__list) {
    width: 100%;
  }

  .import-page :deep(.segmented-control__trigger) {
    flex: 1;
  }
}
</style>
