<script setup lang="ts">
import { RotateCcw, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupKeyItemDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import MobileRecordCard from '@/components/ui/MobileRecordCard.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

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
const problem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)
const weightValue = computed(() =>
  props.item.weight_mode === 'manual' && props.item.weight !== null
    ? String(props.item.weight)
    : 'auto',
)
const weight = computed(() =>
  props.item.weight === null
    ? t('group.keys.none')
    : t(`group.keys.weight.${props.item.weight_mode}`, { weight: n(props.item.weight) }),
)
const recent = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.keys.recentSuccessOnly', { success: n(props.item.recent_success_count) })
    : t('group.keys.recent', {
        success: n(props.item.recent_success_count),
        failure: n(props.item.recent_failure_count),
      }),
)
const recovery = computed(() =>
  props.item.recovery.at_ms === null
    ? t(`group.keys.recovery.${props.item.recovery.mode}`)
    : t('group.keys.recovery.at', {
        time: formatLocalInstant(props.item.recovery.at_ms, locale.value),
      }),
)
</script>

<template>
  <MobileRecordCard :label="t('group.keys.cardLabel', { mask: item.mask })">
    <template #header
      ><div>
        <strong class="group-key-card__mask">{{ item.mask }}</strong
        ><StatusBadge :status="item.effective_status">{{
          t(`group.keys.effective.${item.effective_status}`)
        }}</StatusBadge>
      </div>
      <input
        type="checkbox"
        :checked="selected"
        :disabled="busy"
        :aria-label="t('group.keys.selectKey', { mask: item.mask })"
        @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
    /></template>
    <dl>
      <dt>{{ t('group.keys.columns.weight') }}</dt>
      <dd>
        <AppTooltip v-if="item.weight === null" :content="t('group.keys.notScheduledHelp')"
          ><span tabindex="0">{{ weight }}</span></AppTooltip
        ><span v-else>{{ weight }}</span>
      </dd>
      <dt>{{ t('group.keys.columns.recent') }}</dt>
      <dd>{{ recent }}</dd>
      <dt>{{ t('group.keys.columns.failure') }}</dt>
      <dd>
        {{
          item.recent_failure_count === 0
            ? t('group.keys.none')
            : `${item.last_failure_category}${item.last_status_code === null ? '' : ` · ${item.last_status_code}`}`
        }}
      </dd>
      <dt>{{ t('group.keys.columns.recovery') }}</dt>
      <dd>{{ recovery }}</dd>
    </dl>
    <template #actions
      ><label class="sr-only" :for="`key-mobile-weight-${item.id}`">{{
        t('group.keys.weightFor', { mask: item.mask })
      }}</label
      ><select
        :id="`key-mobile-weight-${item.id}`"
        :value="weightValue"
        :disabled="busy || item.configured_status === 'disabled'"
        @change="emit('weight', { item, value: ($event.target as HTMLSelectElement).value })"
      >
        <option value="auto">{{ t('group.keys.auto') }}</option>
        <option v-for="value in weights" :key="value" :value="String(value)">
          {{ value }}
        </option></select
      ><AppButton
        variant="secondary"
        size="compact"
        :disabled="busy"
        @click="emit('toggle', item)"
        >{{
          item.configured_status === 'active' ? t('group.keys.disable') : t('group.keys.enable')
        }}</AppButton
      ><IconButton
        v-if="problem"
        variant="ghost"
        size="compact"
        :label="t('group.keys.restore')"
        :disabled="busy"
        @click="emit('restore', item)"
        ><RotateCcw :size="16" aria-hidden="true" /></IconButton
      ><IconButton
        variant="ghost"
        size="compact"
        :label="t('group.keys.delete')"
        :disabled="busy"
        @click="emit('remove', item)"
        ><Trash2 :size="16" aria-hidden="true" /></IconButton
    ></template>
  </MobileRecordCard>
</template>

<style scoped>
.group-key-card__mask {
  display: block;
  margin-bottom: var(--space-2);
  font-family: var(--font-mono);
}
.mobile-record-card :deep(select) {
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding-inline: var(--space-2);
  font: inherit;
}
</style>
