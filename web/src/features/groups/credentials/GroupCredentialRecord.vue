<script setup lang="ts">
import { ChevronDown, RotateCcw, SlidersHorizontal, Trash2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

import { presentCredentialFailureCategory } from './credential-failure-presenter'
import SubscriptionCredentialDetails from './SubscriptionCredentialDetails.vue'

const props = defineProps<{
  item: CredentialItemDto
  rowIndex: number
  selected: boolean
  busy: boolean
  expanded: boolean
  weightEditorOpen: boolean
  resolveCopyValue: (id: number) => Promise<string>
}>()
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  'update:expanded': [expanded: boolean]
  'update:weightEditorOpen': [open: boolean]
  weight: [payload: { item: CredentialItemDto; value: string }]
  toggle: [item: CredentialItemDto]
  restore: [item: CredentialItemDto]
  refresh: [item: CredentialItemDto]
  reauthorize: [item: CredentialItemDto]
  remove: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const draftWeightMode = ref<'auto' | 'manual'>('auto')
const draftWeight = ref('50')
const detailId = computed(() => `group-credential-details-${props.item.credential_id}`)
const weightInputId = computed(() => `group-credential-weight-${props.item.credential_id}`)
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)
const weightLabel = computed(() =>
  props.item.weight === null
    ? t('group.credentials.none')
    : t(`group.credentials.weight.${props.item.weight_mode}`, { weight: n(props.item.weight) }),
)
const recentLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.credentials.recentSuccessOnly', { success: n(props.item.recent_success_count) })
    : t('group.credentials.recent', {
        success: n(props.item.recent_success_count),
        failure: n(props.item.recent_failure_count),
      }),
)
const primaryQuota = computed(() =>
  props.item.observation?.snapshot?.quota_windows.find(({ is_primary }) => is_primary),
)
const entitlementLabel = computed(() => {
  if (props.item.connection_type !== 'subscription') return recentLabel.value
  const window = primaryQuota.value
  if (!window)
    return t(
      `group.credentials.subscription.observation.${props.item.observation?.state ?? 'unavailable'}`,
    )
  if (window.utilization !== undefined) {
    return `${window.label} · ${n(Math.round(window.utilization * 100))}%`
  }
  if (window.remaining !== undefined && window.limit !== undefined) {
    return `${window.label} · ${n(window.remaining)} / ${n(window.limit)}`
  }
  return `${window.label} · ${t(`group.credentials.subscription.quotaState.${window.state}`)}`
})
const recoveryLabel = computed(() => {
  if (props.item.recovery.mode === 'none') return t('group.credentials.recovery.none')
  if (props.item.recovery.at_ms !== null)
    return t('group.credentials.recovery.at', {
      time: formatLocalInstant(props.item.recovery.at_ms, locale.value),
    })
  return t(`group.credentials.recovery.${props.item.recovery.mode}`)
})
const failureLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.credentials.none')
    : `${presentCredentialFailureCategory(t, props.item.last_failure_category)}${
        props.item.last_status_code === null ? '' : ` · ${props.item.last_status_code}`
      }`,
)
const weightModeOptions = computed(() => [
  { value: 'auto', label: t('group.credentials.weightEditor.auto'), disabled: props.busy },
  { value: 'manual', label: t('group.credentials.weightEditor.manual'), disabled: props.busy },
])
const manualWeightValid = computed(() => {
  if (draftWeightMode.value === 'auto') return true
  const value = Number(draftWeight.value)
  return Number.isInteger(value) && value >= 1 && value <= 100
})

watch(
  () => props.weightEditorOpen,
  (open) => {
    if (!open) return
    draftWeightMode.value = props.item.weight_mode
    draftWeight.value = String(props.item.weight ?? 50)
  },
)

function saveWeight(): void {
  if (props.busy || !manualWeightValid.value) return
  emit('weight', {
    item: props.item,
    value: draftWeightMode.value === 'auto' ? 'auto' : String(Number(draftWeight.value)),
  })
  emit('update:weightEditorOpen', false)
}
</script>

<template>
  <article
    class="ledger-record-list__record group-credential-record"
    :class="{ 'group-credential-record--expanded': expanded }"
    role="row"
    :aria-rowindex="rowIndex"
  >
    <div class="group-credential-record__summary" role="presentation">
      <div class="ledger-record-list__cell group-credential-record__select" role="cell">
        <label>
          <span class="sr-only">{{
            t('group.credentials.selectCredential', { mask: item.mask })
          }}</span>
          <input
            type="checkbox"
            :checked="selected"
            :disabled="busy"
            @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
          />
        </label>
      </div>

      <div class="ledger-record-list__cell group-credential-record__mask" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.credential')
        }}</span>
        <CopyChip
          v-if="item.connection_type === 'api_key'"
          :value="item.mask"
          :label="t('group.credentials.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
          :resolve-value="() => resolveCopyValue(item.credential_id)"
        />
        <span v-else class="group-credential-record__account">
          <strong>{{ item.mask }}</strong>
          <small>
            {{
              item.observation?.snapshot?.plan_summary.name ||
              t(`group.credentials.subscription.auth.${item.auth_state}`)
            }}
          </small>
        </span>
      </div>

      <div class="ledger-record-list__cell group-credential-record__status" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.status')
        }}</span>
        <StatusBadge :status="item.effective_status" size="compact">
          {{ t(`group.credentials.effective.${item.effective_status}`) }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell group-credential-record__weight" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.weight')
        }}</span>
        <AppTooltip v-if="item.weight === null" :content="t('group.credentials.notScheduledHelp')">
          <span class="group-credential-record__weight-none" tabindex="0">{{ weightLabel }}</span>
        </AppTooltip>
        <span v-else>{{ weightLabel }}</span>
      </div>

      <div class="ledger-record-list__cell group-credential-record__recent" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.recent')
        }}</span>
        <span>{{ entitlementLabel }}</span>
      </div>

      <div class="ledger-record-list__cell group-credential-record__actions" role="cell">
        <AppPopover
          :open="weightEditorOpen"
          align="start"
          @update:open="emit('update:weightEditorOpen', $event)"
        >
          <template #trigger>
            <AppButton
              variant="secondary"
              tone="action"
              size="compact"
              :disabled="busy || item.configured_status === 'disabled'"
            >
              <SlidersHorizontal :size="15" aria-hidden="true" />{{
                t('group.credentials.editWeight')
              }}
            </AppButton>
          </template>
          <form class="group-credential-weight-editor" @submit.prevent="saveWeight">
            <h3>{{ t('group.credentials.weightEditor.title') }}</h3>
            <div class="group-credential-weight-editor__control">
              <SegmentedControl
                v-model="draftWeightMode"
                :label="t('group.credentials.weightEditor.mode')"
                :options="weightModeOptions"
                size="compact"
              />
              <label class="sr-only" :for="weightInputId">
                {{ t('group.credentials.weightEditor.value') }}
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
              class="group-credential-weight-editor__message"
              :class="{ 'group-credential-weight-editor__error': !manualWeightValid }"
              :role="!manualWeightValid ? 'alert' : undefined"
            >
              {{
                draftWeightMode === 'auto'
                  ? '\u00a0'
                  : manualWeightValid
                    ? t('group.credentials.weightEditor.help')
                    : t('group.credentials.weightEditor.invalid')
              }}
            </p>
            <div class="group-credential-weight-editor__actions">
              <AppButton
                variant="secondary"
                size="compact"
                @click="emit('update:weightEditorOpen', false)"
              >
                {{ t('group.credentials.weightEditor.cancel') }}
              </AppButton>
              <AppButton type="submit" size="compact" :disabled="busy || !manualWeightValid">
                {{ t('group.credentials.weightEditor.save') }}
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
            item.configured_status === 'active'
              ? t('group.credentials.disable')
              : t('group.credentials.enable')
          }}
        </AppButton>
        <IconButton
          v-if="isProblem"
          variant="ghost"
          tone="success"
          size="compact"
          :label="t('group.credentials.restore')"
          :disabled="busy"
          @click="emit('restore', item)"
        >
          <RotateCcw :size="15" aria-hidden="true" />
        </IconButton>
        <IconButton
          variant="ghost"
          tone="danger"
          size="compact"
          :label="t('group.credentials.delete')"
          :disabled="busy"
          @click="emit('remove', item)"
        >
          <Trash2 :size="15" aria-hidden="true" />
        </IconButton>
        <IconButton
          class="group-credential-record__toggle"
          variant="ghost"
          size="compact"
          :label="expanded ? t('group.credentials.collapse') : t('group.credentials.expand')"
          :aria-expanded="expanded"
          :aria-controls="detailId"
          @click="emit('update:expanded', !expanded)"
        >
          <ChevronDown :size="16" aria-hidden="true" />
        </IconButton>
      </div>
    </div>

    <div
      class="group-credential-record__disclosure"
      :class="{ 'group-credential-record__disclosure--expanded': expanded }"
      role="presentation"
    >
      <div class="group-credential-record__disclosure-inner" role="presentation">
        <div
          :id="detailId"
          class="ledger-record-list__cell group-credential-record__details"
          role="cell"
          :aria-hidden="!expanded"
          :inert="!expanded || undefined"
        >
          <SubscriptionCredentialDetails
            v-if="item.connection_type === 'subscription'"
            :item="item"
            :busy="busy"
            @refresh="emit('refresh', $event)"
            @reauthorize="emit('reauthorize', $event)"
          />
          <dl class="group-credential-record__runtime-details">
            <div>
              <dt>{{ t('group.credentials.detailsFailure') }}</dt>
              <dd>{{ failureLabel }}</dd>
            </div>
            <div>
              <dt>{{ t('group.credentials.detailsRecovery') }}</dt>
              <dd>{{ recoveryLabel }}</dd>
            </div>
            <div>
              <dt>{{ t('group.credentials.detailsConsecutive') }}</dt>
              <dd>{{ n(item.consecutive_failure_count) }}</dd>
            </div>
          </dl>
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.group-credential-record {
  align-items: stretch;
  padding: 0;
}

.group-credential-record__summary {
  position: relative;
  display: grid;
  min-height: 52px;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  padding: 8px 0;
}

.group-credential-record__select {
  display: flex;
  justify-content: center;
}

.group-credential-record__select label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}
.group-credential-record__select input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}

.group-credential-record__mask,
.group-credential-record__weight,
.group-credential-record__recent {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.group-credential-record__account {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.group-credential-record__account strong {
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-credential-record__account small {
  overflow: hidden;
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-credential-record__weight-none {
  color: var(--color-text-faint);
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

.group-credential-record__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-1);
}

.group-credential-record__toggle svg {
  transition: transform var(--duration-normal) var(--easing-standard);
}

.group-credential-record__toggle[aria-expanded='true'] svg {
  transform: rotate(180deg);
}

.group-credential-record__details {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 14px 17px 16px 42px;
}

.group-credential-record__disclosure {
  display: grid;
  grid-column: 1 / -1;
  grid-template-rows: 0fr;
  transition: grid-template-rows var(--duration-normal) var(--easing-standard);
}

.group-credential-record__disclosure--expanded {
  grid-template-rows: 1fr;
}

.group-credential-record__disclosure-inner {
  min-height: 0;
  overflow: hidden;
}

.group-credential-record__details dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-5);
  margin: 0;
}

.group-credential-record__details dl div {
  min-width: 0;
}

.group-credential-record__details dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.group-credential-record__details dd {
  margin: 3px 0 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 560;
}

.group-credential-record__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.group-credential-weight-editor {
  display: grid;
  gap: var(--space-3);
  width: min(320px, 100%);
}

.group-credential-weight-editor h3,
.group-credential-weight-editor p {
  margin: 0;
}

.group-credential-weight-editor h3 {
  font-size: var(--text-body);
  font-weight: 650;
}

.group-credential-weight-editor__control {
  display: flex;
  align-items: center;
  gap: 10px;
}

.group-credential-weight-editor__control > input {
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

.group-credential-weight-editor__control > input.is-concealed {
  visibility: hidden;
  pointer-events: none;
}

.group-credential-weight-editor__message {
  min-height: 1.5em;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.group-credential-weight-editor__error {
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.group-credential-weight-editor__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

@media (max-width: 860px) {
  .group-credential-record {
    padding: 0;
  }

  .group-credential-record__summary {
    grid-template-columns: var(--ledger-record-list-card-grid, minmax(0, 0.48fr) minmax(0, 1.52fr));
    align-items: start;
    gap: 14px 16px;
    padding: 16px 58px 16px 16px;
  }

  .group-credential-record__select {
    position: absolute;
    top: 2px;
    right: 4px;
  }

  .group-credential-record__select label {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .group-credential-record__mask,
  .group-credential-record__recent,
  .group-credential-record__actions {
    grid-column: 1 / -1;
  }

  .group-credential-record__mask,
  .group-credential-record__status,
  .group-credential-record__weight,
  .group-credential-record__recent {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .group-credential-record__recent,
  .group-credential-record__actions {
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .group-credential-record__actions {
    justify-content: flex-start;
  }

  .group-credential-record__actions :deep(.app-button),
  .group-credential-record__actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }

  .group-credential-record__details {
    padding: 14px;
  }

  .group-credential-record__details dl {
    grid-template-columns: 1fr;
  }

  .group-credential-record__mobile-label {
    display: inline;
  }
}

@media (max-width: 560px) {
  .group-credential-record__summary {
    padding: 14px 58px 14px 13px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-credential-record__toggle svg,
  .group-credential-record__disclosure {
    transition: none;
  }
}
</style>
