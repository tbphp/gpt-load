<script setup lang="ts">
import { CircleHelp } from '@lucide/vue'

import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

withDefaults(
  defineProps<{
    label: string
    value: string
    help?: string
    sourceLabel: string
    actionLabel: string
    overridden?: boolean
    pendingRestore?: boolean
    locked?: boolean
    disabled?: boolean
    divided?: boolean
  }>(),
  {
    help: undefined,
    overridden: false,
    pendingRestore: false,
    locked: false,
    disabled: false,
    divided: true,
  },
)
const emit = defineEmits<{ toggle: [] }>()
</script>

<template>
  <div
    class="setting-row"
    :class="{ 'setting-row--divided': divided, 'setting-row--editing': overridden }"
  >
    <div class="setting-row__identity">
      <span class="setting-row__label">{{ label }}</span>
      <AppTooltip v-if="help" :content="help">
        <span class="setting-row__hint" tabindex="0" :aria-label="help">
          <CircleHelp :size="13" aria-hidden="true" />
        </span>
      </AppTooltip>
    </div>

    <div class="setting-row__cluster">
      <StatusBadge
        v-if="pendingRestore || locked"
        size="compact"
        :tone="locked ? 'neutral' : 'warning'"
        :icon="locked ? 'off' : 'alert'"
      >
        {{ sourceLabel }}
      </StatusBadge>
      <div class="setting-row__value">
        <slot v-if="overridden" name="control" />
        <span v-else class="setting-row__plain">{{ value }}</span>
      </div>
      <AppButton
        v-if="!locked"
        variant="secondary"
        :tone="overridden ? 'warning' : 'action'"
        size="compact"
        :disabled="disabled"
        @click="emit('toggle')"
      >
        {{ actionLabel }}
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.setting-row {
  display: grid;
  grid-template-columns: 172px minmax(0, 1fr);
  align-items: center;
  column-gap: var(--space-4);
  border-left: 2px solid transparent;
  padding: 8px 10px 8px 12px;
}

.setting-row--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}

.setting-row--editing {
  border-left-color: var(--color-action);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
}

.setting-row__identity {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  font-weight: 600;
}

.setting-row--editing .setting-row__identity {
  color: var(--color-text);
}

.setting-row__hint {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: var(--radius-tag);
  color: var(--color-text-faint);
  cursor: help;
}

.setting-row__hint:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.setting-row__hint:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.setting-row__cluster {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  gap: var(--space-3);
}

.setting-row__value {
  min-width: 0;
}

.setting-row__plain {
  color: var(--color-text);
  font-size: var(--text-body);
  font-variant-numeric: tabular-nums;
}

@media (max-width: 800px) {
  .setting-row {
    grid-template-columns: minmax(0, 1fr);
    row-gap: var(--space-2);
  }
}
</style>
