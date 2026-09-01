<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelCandidate } from '@/app/resources/providers'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'

import type { ModelDraftItem, ModelNameConflict, ModelSyncMode } from './model-diff'

const props = defineProps<{
  open: boolean
  mode: ModelSyncMode
  additions: readonly ModelCandidate[]
  removals: readonly ModelDraftItem[]
  conflicts: readonly ModelNameConflict[]
  pending: boolean
  error: string
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:mode': [mode: ModelSyncMode]
  confirm: []
}>()
const { t } = useI18n()
const previewLimit = 40

const modeOptions = computed<SegmentedControlOption[]>(() =>
  (['full', 'cleanup', 'add'] as const).map((value) => ({
    value,
    label: t(`group.modelEditor.sync.mode.${value}`),
    disabled: props.pending,
  })),
)
const showAdditions = computed(() => props.mode === 'add' || props.mode === 'full')
const showRemovals = computed(() => props.mode === 'cleanup' || props.mode === 'full')
const activeAdditions = computed(() => (showAdditions.value ? props.additions : []))
const activeRemovals = computed(() => (showRemovals.value ? props.removals : []))
const additionPreview = computed(() => activeAdditions.value.slice(0, previewLimit))
const removalPreview = computed(() => activeRemovals.value.slice(0, previewLimit))
const additionRemainder = computed(
  () => activeAdditions.value.length - additionPreview.value.length,
)
const removalRemainder = computed(() => activeRemovals.value.length - removalPreview.value.length)
const changeCount = computed(() => activeAdditions.value.length + activeRemovals.value.length)
const conflictNames = computed(() =>
  props.conflicts.map(({ client_model }) => client_model).join(', '),
)

function setMode(value: string): void {
  if (value === 'cleanup' || value === 'add' || value === 'full') emit('update:mode', value)
}
</script>

<template>
  <AppConfirmDialog
    appearance="ledger"
    :open="open"
    :title="t('group.modelEditor.sync.title')"
    :description="t('group.modelEditor.sync.description')"
    :close-label="t('group.modelEditor.sync.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('group.modelEditor.sync.confirm')"
    description-tone="warning"
    :tone="activeRemovals.length ? 'danger' : 'default'"
    :pending="pending"
    :confirm-disabled="changeCount === 0 || conflicts.length > 0"
    @update:open="emit('update:open', $event)"
    @confirm="emit('confirm')"
  >
    <div class="group-model-sync">
      <SegmentedControl
        :class="['group-model-sync__modes', `group-model-sync__modes--${mode}`]"
        :model-value="mode"
        :label="t('group.modelEditor.sync.modeLabel')"
        :options="modeOptions"
        controls-id="group-model-sync-changes"
        id-prefix="group-model-sync-mode"
        appearance="drawer"
        size="sm"
        @update:model-value="setMode"
      />

      <div
        id="group-model-sync-changes"
        class="group-model-sync__changes"
        role="tabpanel"
        :aria-labelledby="`group-model-sync-mode-${mode}`"
      >
        <section
          v-if="showAdditions"
          class="group-model-sync__section group-model-sync__section--addition"
        >
          <strong>{{
            t('group.modelEditor.sync.additions', { count: activeAdditions.length })
          }}</strong>
          <ul v-if="additionPreview.length">
            <li v-for="candidate in additionPreview" :key="candidate.id">
              <code>{{ candidate.id }}</code>
            </li>
          </ul>
          <span v-else class="group-model-sync__empty">{{
            t('group.modelEditor.sync.noAdditions')
          }}</span>
          <span v-if="additionRemainder" class="group-model-sync__remainder">{{
            t('group.modelEditor.sync.more', { count: additionRemainder })
          }}</span>
        </section>

        <section
          v-if="showRemovals"
          class="group-model-sync__section group-model-sync__section--removal"
        >
          <strong>{{
            t('group.modelEditor.sync.removals', { count: activeRemovals.length })
          }}</strong>
          <ul v-if="removalPreview.length">
            <li v-for="model in removalPreview" :key="model.key">
              <code>{{ model.id }}</code>
              <template v-if="model.alias_enabled">
                <span aria-hidden="true">→</span>
                <code>{{ model.alias }}</code>
              </template>
            </li>
          </ul>
          <span v-else class="group-model-sync__empty">{{
            t('group.modelEditor.sync.noRemovals')
          }}</span>
          <span v-if="removalRemainder" class="group-model-sync__remainder">{{
            t('group.modelEditor.sync.more', { count: removalRemainder })
          }}</span>
        </section>
      </div>

      <InlineFeedback v-if="conflicts.length" tone="danger">
        {{ t('group.modelEditor.sync.conflict', { names: conflictNames }) }}
      </InlineFeedback>
      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.group-model-sync,
.group-model-sync__changes {
  display: grid;
  gap: var(--space-3);
}

.group-model-sync__modes {
  width: 100%;
}

.group-model-sync__modes :deep(.segmented-control__list) {
  width: 100%;
}

.group-model-sync__modes :deep(.segmented-control__trigger) {
  min-width: 0;
  flex: 1;
}

.group-model-sync__modes--cleanup :deep(.segmented-control__trigger[data-state='active']) {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.group-model-sync__modes--add :deep(.segmented-control__trigger[data-state='active']) {
  background: var(--color-success-bg);
  color: var(--color-success);
}

.group-model-sync__section {
  display: grid;
  gap: var(--space-2);
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: 10px 11px;
}

.group-model-sync__section--addition {
  border-color: color-mix(in srgb, var(--color-success) 32%, var(--color-border-subtle));
  border-left-width: 3px;
  background: color-mix(in srgb, var(--color-success-bg) 72%, var(--color-surface));
}

.group-model-sync__section--removal {
  border-color: color-mix(in srgb, var(--color-danger) 32%, var(--color-border-subtle));
  border-left-width: 3px;
  background: color-mix(in srgb, var(--color-danger-bg) 72%, var(--color-surface));
}

.group-model-sync__section > strong {
  font-size: var(--text-sm);
}

.group-model-sync__section--addition > strong {
  color: var(--color-success);
}

.group-model-sync__section--removal > strong {
  color: var(--color-danger);
}

.group-model-sync__section ul {
  display: grid;
  max-height: 150px;
  gap: 5px;
  margin: 0;
  overflow-y: auto;
  padding: 0;
  list-style: none;
}

.group-model-sync__section li {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
  color: var(--color-text);
  font-size: var(--text-sm);
}

.group-model-sync__section code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-model-sync__empty,
.group-model-sync__remainder {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
</style>
