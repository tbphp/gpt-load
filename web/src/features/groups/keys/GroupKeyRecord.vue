<script setup lang="ts">
import { ChevronDown, RotateCcw, SlidersHorizontal, Trash2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupKeyItemDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

import { presentKeyFailureCategory } from './key-failure-presenter'

const props = defineProps<{
  item: GroupKeyItemDto
  rowIndex: number
  selected: boolean
  busy: boolean
  resolveCopyValue: (id: number) => Promise<string>
}>()
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  weight: [payload: { item: GroupKeyItemDto; value: string }]
  toggle: [item: GroupKeyItemDto]
  restore: [item: GroupKeyItemDto]
  remove: [item: GroupKeyItemDto]
}>()
const { locale, n, t } = useI18n()
const expanded = ref(false)
const weightEditorOpen = ref(false)
const draftWeightMode = ref<'auto' | 'manual'>('auto')
const draftWeight = ref('50')
const detailId = computed(() => `group-key-details-${props.item.id}`)
const weightInputId = computed(() => `group-key-weight-${props.item.id}`)
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)
const weightLabel = computed(() =>
  props.item.weight === null
    ? t('group.keys.none')
    : t(`group.keys.weight.${props.item.weight_mode}`, { weight: n(props.item.weight) }),
)
const recentLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.keys.recentSuccessOnly', { success: n(props.item.recent_success_count) })
    : t('group.keys.recent', {
        success: n(props.item.recent_success_count),
        failure: n(props.item.recent_failure_count),
      }),
)
const recoveryLabel = computed(() => {
  if (props.item.recovery.mode === 'none') return t('group.keys.recovery.none')
  if (props.item.recovery.at_ms !== null)
    return t('group.keys.recovery.at', {
      time: formatLocalInstant(props.item.recovery.at_ms, locale.value),
    })
  return t(`group.keys.recovery.${props.item.recovery.mode}`)
})
const failureLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.keys.none')
    : `${presentKeyFailureCategory(t, props.item.last_failure_category)}${
        props.item.last_status_code === null ? '' : ` · ${props.item.last_status_code}`
      }`,
)
const weightModeOptions = computed(() => [
  { value: 'auto', label: t('group.keys.weightEditor.auto'), disabled: props.busy },
  { value: 'manual', label: t('group.keys.weightEditor.manual'), disabled: props.busy },
])
const manualWeightValid = computed(() => {
  if (draftWeightMode.value === 'auto') return true
  const value = Number(draftWeight.value)
  return Number.isInteger(value) && value >= 1 && value <= 100
})

watch(weightEditorOpen, (open) => {
  if (!open) return
  draftWeightMode.value = props.item.weight_mode
  draftWeight.value = String(props.item.weight ?? 50)
})

function saveWeight(): void {
  if (props.busy || !manualWeightValid.value) return
  emit('weight', {
    item: props.item,
    value: draftWeightMode.value === 'auto' ? 'auto' : String(Number(draftWeight.value)),
  })
  weightEditorOpen.value = false
}
</script>

<template>
  <article
    class="ledger-record-list__record group-key-record"
    :class="{ 'group-key-record--expanded': expanded }"
    role="row"
    :aria-rowindex="rowIndex"
  >
    <div class="group-key-record__summary" role="presentation">
      <div class="ledger-record-list__cell group-key-record__select" role="cell">
        <label>
          <span class="sr-only">{{ t('group.keys.selectKey', { mask: item.mask }) }}</span>
          <input
            type="checkbox"
            :checked="selected"
            :disabled="busy"
            @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
          />
        </label>
      </div>

      <div class="ledger-record-list__cell group-key-record__mask" role="cell">
        <span class="group-key-record__mobile-label">{{ t('group.keys.columns.key') }}</span>
        <CopyChip
          :value="item.mask"
          :label="t('group.keys.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
          :resolve-value="() => resolveCopyValue(item.id)"
        />
      </div>

      <div class="ledger-record-list__cell group-key-record__status" role="cell">
        <span class="group-key-record__mobile-label">{{ t('group.keys.columns.status') }}</span>
        <StatusBadge :status="item.effective_status" size="compact">
          {{ t(`group.keys.effective.${item.effective_status}`) }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell group-key-record__weight" role="cell">
        <span class="group-key-record__mobile-label">{{ t('group.keys.columns.weight') }}</span>
        <AppTooltip v-if="item.weight === null" :content="t('group.keys.notScheduledHelp')">
          <span class="group-key-record__weight-none" tabindex="0">{{ weightLabel }}</span>
        </AppTooltip>
        <span v-else>{{ weightLabel }}</span>
      </div>

      <div class="ledger-record-list__cell group-key-record__recent" role="cell">
        <span class="group-key-record__mobile-label">{{ t('group.keys.columns.recent') }}</span>
        <span>{{ recentLabel }}</span>
      </div>

      <div class="ledger-record-list__cell group-key-record__actions" role="cell">
        <AppPopover v-model:open="weightEditorOpen" align="start">
          <template #trigger>
            <AppButton
              variant="secondary"
              tone="action"
              size="compact"
              :disabled="busy || item.configured_status === 'disabled'"
            >
              <SlidersHorizontal :size="15" aria-hidden="true" />{{ t('group.keys.editWeight') }}
            </AppButton>
          </template>
          <form class="group-key-weight-editor" @submit.prevent="saveWeight">
            <h3>{{ t('group.keys.weightEditor.title') }}</h3>
            <div class="group-key-weight-editor__control">
              <SegmentedControl
                v-model="draftWeightMode"
                :label="t('group.keys.weightEditor.mode')"
                :options="weightModeOptions"
                size="compact"
              />
              <label class="sr-only" :for="weightInputId">
                {{ t('group.keys.weightEditor.value') }}
              </label>
              <input
                :id="weightInputId"
                v-model="draftWeight"
                :class="{ 'is-concealed': draftWeightMode === 'auto' }"
                type="number"
                min="1"
                max="100"
                step="1"
                inputmode="numeric"
                :disabled="busy || draftWeightMode === 'auto'"
                :tabindex="draftWeightMode === 'auto' ? -1 : undefined"
                :aria-hidden="draftWeightMode === 'auto' ? 'true' : undefined"
                :aria-invalid="!manualWeightValid || undefined"
              />
            </div>
            <p
              class="group-key-weight-editor__message"
              :class="{ 'group-key-weight-editor__error': !manualWeightValid }"
              :role="!manualWeightValid ? 'alert' : undefined"
            >
              {{
                draftWeightMode === 'auto'
                  ? '\u00a0'
                  : manualWeightValid
                    ? t('group.keys.weightEditor.help')
                    : t('group.keys.weightEditor.invalid')
              }}
            </p>
            <div class="group-key-weight-editor__actions">
              <AppButton variant="secondary" size="compact" @click="weightEditorOpen = false">
                {{ t('group.keys.weightEditor.cancel') }}
              </AppButton>
              <AppButton type="submit" size="compact" :disabled="busy || !manualWeightValid">
                {{ t('group.keys.weightEditor.save') }}
              </AppButton>
            </div>
          </form>
        </AppPopover>
        <AppButton
          variant="secondary"
          :tone="item.configured_status === 'active' ? 'warning' : 'success'"
          size="compact"
          :disabled="busy"
          @click="emit('toggle', item)"
        >
          {{
            item.configured_status === 'active' ? t('group.keys.disable') : t('group.keys.enable')
          }}
        </AppButton>
        <IconButton
          v-if="isProblem"
          variant="ghost"
          tone="success"
          size="compact"
          :label="t('group.keys.restore')"
          :disabled="busy"
          @click="emit('restore', item)"
        >
          <RotateCcw :size="15" aria-hidden="true" />
        </IconButton>
        <IconButton
          variant="ghost"
          tone="danger"
          size="compact"
          :label="t('group.keys.delete')"
          :disabled="busy"
          @click="emit('remove', item)"
        >
          <Trash2 :size="15" aria-hidden="true" />
        </IconButton>
        <IconButton
          class="group-key-record__toggle"
          variant="ghost"
          size="compact"
          :label="expanded ? t('group.keys.collapse') : t('group.keys.expand')"
          :aria-expanded="expanded"
          :aria-controls="detailId"
          @click="expanded = !expanded"
        >
          <ChevronDown :size="16" aria-hidden="true" />
        </IconButton>
      </div>
    </div>

    <div
      class="group-key-record__disclosure"
      :class="{ 'group-key-record__disclosure--expanded': expanded }"
      role="presentation"
    >
      <div class="group-key-record__disclosure-inner" role="presentation">
        <div
          :id="detailId"
          class="ledger-record-list__cell group-key-record__details"
          role="cell"
          :aria-hidden="!expanded"
        >
          <dl>
            <div>
              <dt>{{ t('group.keys.detailsFailure') }}</dt>
              <dd>{{ failureLabel }}</dd>
            </div>
            <div>
              <dt>{{ t('group.keys.detailsRecovery') }}</dt>
              <dd>{{ recoveryLabel }}</dd>
            </div>
            <div>
              <dt>{{ t('group.keys.detailsConsecutive') }}</dt>
              <dd>{{ n(item.consecutive_failure_count) }}</dd>
            </div>
          </dl>
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.group-key-record {
  align-items: stretch;
  padding: 0;
}

.group-key-record__summary {
  position: relative;
  display: grid;
  min-height: 52px;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  padding: 8px 0;
}

.group-key-record__select {
  display: flex;
  justify-content: center;
}

.group-key-record__select label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}
.group-key-record__select input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}

.group-key-record__mask,
.group-key-record__weight,
.group-key-record__recent {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.group-key-record__weight-none {
  color: var(--color-text-faint);
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

.group-key-record__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-1);
}

.group-key-record__toggle svg {
  transition: transform var(--duration-normal) var(--easing-standard);
}

.group-key-record__toggle[aria-expanded='true'] svg {
  transform: rotate(180deg);
}

.group-key-record__details {
  min-width: 0;
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 14px 17px 16px 42px;
}

.group-key-record__disclosure {
  display: grid;
  grid-column: 1 / -1;
  grid-template-rows: 0fr;
  transition: grid-template-rows var(--duration-normal) var(--easing-standard);
}

.group-key-record__disclosure--expanded {
  grid-template-rows: 1fr;
}

.group-key-record__disclosure-inner {
  min-height: 0;
  overflow: hidden;
}

.group-key-record__details dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-5);
  margin: 0;
}

.group-key-record__details dl div {
  min-width: 0;
}

.group-key-record__details dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.group-key-record__details dd {
  margin: 3px 0 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 560;
}

.group-key-record__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.group-key-weight-editor {
  display: grid;
  gap: var(--space-3);
  width: min(320px, 100%);
}

.group-key-weight-editor h3,
.group-key-weight-editor p {
  margin: 0;
}

.group-key-weight-editor h3 {
  font-size: var(--text-body);
  font-weight: 650;
}

.group-key-weight-editor__control {
  display: flex;
  align-items: center;
  gap: 10px;
}

.group-key-weight-editor__control > input {
  width: 90px;
  min-height: var(--control-compact);
  flex: 0 0 90px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: var(--text-body) var(--font-mono);
}

.group-key-weight-editor__control > input.is-concealed {
  visibility: hidden;
  pointer-events: none;
}

.group-key-weight-editor__message {
  min-height: 1.5em;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.group-key-weight-editor__error {
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.group-key-weight-editor__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

@media (max-width: 860px) {
  .group-key-record {
    padding: 0;
  }

  .group-key-record__summary {
    grid-template-columns: var(--ledger-record-list-card-grid, minmax(0, 0.48fr) minmax(0, 1.52fr));
    align-items: start;
    gap: 14px 16px;
    padding: 16px 58px 16px 16px;
  }

  .group-key-record__select {
    position: absolute;
    top: 2px;
    right: 4px;
  }

  .group-key-record__select label {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .group-key-record__mask,
  .group-key-record__recent,
  .group-key-record__actions {
    grid-column: 1 / -1;
  }

  .group-key-record__mask,
  .group-key-record__status,
  .group-key-record__weight,
  .group-key-record__recent {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .group-key-record__recent,
  .group-key-record__actions {
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .group-key-record__actions {
    justify-content: flex-start;
  }

  .group-key-record__actions :deep(.app-button),
  .group-key-record__actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }

  .group-key-record__details {
    padding: 14px;
  }

  .group-key-record__details dl {
    grid-template-columns: 1fr;
  }

  .group-key-record__mobile-label {
    display: inline;
  }
}

@media (max-width: 560px) {
  .group-key-record__summary {
    padding: 14px 58px 14px 13px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-key-record__toggle svg,
  .group-key-record__disclosure {
    transition: none;
  }
}
</style>
