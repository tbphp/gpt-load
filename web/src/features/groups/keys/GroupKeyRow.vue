<script setup lang="ts">
import { RotateCcw, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupKeyItemDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import DisclosurePanel from '@/components/ui/DisclosurePanel.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

import { presentKeyFailureCategory } from './key-failure-presenter'

const props = defineProps<{ item: GroupKeyItemDto; selected: boolean; busy: boolean }>()
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  weight: [payload: { item: GroupKeyItemDto; value: string }]
  toggle: [item: GroupKeyItemDto]
  restore: [item: GroupKeyItemDto]
  remove: [item: GroupKeyItemDto]
}>()
const { locale, n, t } = useI18n()
const weights = Array.from({ length: 100 }, (_, index) => index + 1)
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)
const weightValue = computed(() =>
  props.item.weight_mode === 'manual' && props.item.weight !== null
    ? String(props.item.weight)
    : 'auto',
)
const weightLabel = computed(() =>
  props.item.weight === null
    ? t('group.keys.notScheduled')
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
function updateWeight(event: Event): void {
  emit('weight', { item: props.item, value: (event.target as HTMLSelectElement).value })
}
</script>

<template>
  <tr>
    <td>
      <input
        type="checkbox"
        :checked="selected"
        :disabled="busy"
        :aria-label="t('group.keys.selectKey', { mask: item.mask })"
        @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
      />
    </td>
    <td class="group-key-row__mask">{{ item.mask }}</td>
    <td>
      <StatusBadge :status="item.effective_status">{{
        t(`group.keys.effective.${item.effective_status}`)
      }}</StatusBadge>
    </td>
    <td>
      <AppTooltip v-if="item.weight === null" :content="t('group.keys.notScheduledHelp')"
        ><span class="group-key-row__weight group-key-row__weight--none" tabindex="0">{{
          t('group.keys.none')
        }}</span></AppTooltip
      >
      <span v-else class="group-key-row__weight">{{ weightLabel }}</span>
    </td>
    <td class="group-key-row__recent">{{ recentLabel }}</td>
    <td>
      <div class="group-key-row__actions">
        <label class="sr-only" :for="`key-weight-${item.id}`">{{
          t('group.keys.weightFor', { mask: item.mask })
        }}</label>
        <select
          :id="`key-weight-${item.id}`"
          :value="weightValue"
          :disabled="busy || item.configured_status === 'disabled'"
          @change="updateWeight"
        >
          <option value="auto">{{ t('group.keys.auto') }}</option>
          <option v-for="weight in weights" :key="weight" :value="String(weight)">
            {{ weight }}
          </option>
        </select>
        <AppButton variant="ghost" size="compact" :disabled="busy" @click="emit('toggle', item)">{{
          item.configured_status === 'active' ? t('group.keys.disable') : t('group.keys.enable')
        }}</AppButton>
        <IconButton
          v-if="isProblem"
          variant="ghost"
          size="compact"
          :label="t('group.keys.restore')"
          :disabled="busy"
          @click="emit('restore', item)"
          ><RotateCcw :size="15" aria-hidden="true"
        /></IconButton>
        <IconButton
          variant="ghost"
          size="compact"
          :label="t('group.keys.delete')"
          :disabled="busy"
          @click="emit('remove', item)"
          ><Trash2 :size="15" aria-hidden="true"
        /></IconButton>
      </div>
    </td>
  </tr>
  <tr class="group-key-row__detail">
    <td colspan="6">
      <DisclosurePanel :summary="t('group.keys.details')"
        ><dl>
          <div>
            <dt>{{ t('group.keys.detailsFailure') }}</dt>
            <dd>
              {{ failureLabel }}
            </dd>
          </div>
          <div>
            <dt>{{ t('group.keys.detailsRecovery') }}</dt>
            <dd>{{ recoveryLabel }}</dd>
          </div>
          <div>
            <dt>{{ t('group.keys.detailsConsecutive') }}</dt>
            <dd>{{ n(item.consecutive_failure_count) }}</dd>
          </div>
        </dl></DisclosurePanel
      >
    </td>
  </tr>
</template>

<style scoped>
.group-key-row__mask,
.group-key-row__recent,
.group-key-row__weight {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}
.group-key-row__weight--none {
  color: var(--color-text-faint);
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}
.group-key-row__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}
.group-key-row__actions select {
  min-height: var(--control-compact);
  max-width: 104px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding-inline: var(--space-1);
  font: inherit;
}
.group-key-row__detail td {
  padding-top: 0;
}
.group-key-row__detail dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}
.group-key-row__detail dl div {
  min-width: 0;
}
.group-key-row__detail dt {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.group-key-row__detail dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
}
@media (max-width: 1023px) {
  .group-key-row__detail dl {
    grid-template-columns: 1fr;
  }
}
</style>
