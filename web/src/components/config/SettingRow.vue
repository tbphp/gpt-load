<script setup lang="ts">
import { CircleHelp, PencilLine, RotateCcw } from '@lucide/vue'

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
        <button type="button" class="setting-row__hint" :aria-label="`${label} · ${help}`">
          <CircleHelp :size="13" aria-hidden="true" />
        </button>
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
      <AppTooltip v-if="!locked" :content="actionLabel">
        <button
          type="button"
          class="setting-row__action"
          :class="[
            `setting-row__action--${overridden ? 'warning' : 'action'}`,
            { 'setting-row__action--reveal': !overridden },
          ]"
          :aria-label="`${actionLabel} · ${label}`"
          :aria-pressed="overridden"
          :disabled="disabled"
          @click="emit('toggle')"
        >
          <RotateCcw v-if="overridden" :size="14" aria-hidden="true" />
          <PencilLine v-else :size="14" aria-hidden="true" />
        </button>
      </AppTooltip>
    </div>
  </div>
</template>

<style scoped>
.setting-row {
  display: grid;
  grid-template-columns: 216px minmax(0, 1fr);
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
  line-height: 1.4;
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
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-faint);
  padding: 0;
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
  /* 与控件实际高度对齐，折叠态和覆盖态才会完全等高。 */
  min-height: 28px;
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

.setting-row__action {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 27px;
  height: 27px;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.setting-row__action--action {
  color: var(--color-action);
}

.setting-row__action--action:hover:not(:disabled) {
  background: var(--color-action-soft);
}

.setting-row__action--warning {
  color: var(--color-warning);
}

.setting-row__action--warning:hover:not(:disabled) {
  background: var(--color-warning-bg);
}

.setting-row__action:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.setting-row__action:disabled {
  cursor: not-allowed;
  opacity: 0.46;
}

.setting-row__action--reveal {
  opacity: 0;
  transition:
    opacity var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.setting-row:hover .setting-row__action--reveal,
.setting-row:focus-within .setting-row__action--reveal {
  opacity: 1;
}

@media (hover: none) {
  .setting-row__action--reveal {
    opacity: 1;
  }
}

@media (max-width: 800px) {
  .setting-row {
    grid-template-columns: minmax(0, 1fr);
    row-gap: var(--space-2);
  }

  .setting-row__action--reveal {
    opacity: 1;
  }
}
</style>
