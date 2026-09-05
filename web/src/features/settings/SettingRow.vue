<script setup lang="ts">
import AppButton from '@/components/ui/AppButton.vue'
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
    :class="{
      'setting-row--divided': divided,
      'setting-row--active': overridden || pendingRestore || locked,
      'setting-row--editing': overridden,
    }"
  >
    <div class="setting-row__identity">
      <span class="setting-row__label">{{ label }}</span>
      <button
        v-if="help && !overridden"
        type="button"
        class="setting-row__hint"
        :title="help"
        :aria-label="help"
      >
        ?
      </button>
    </div>

    <div class="setting-row__value">
      <template v-if="overridden">
        <slot name="control" />
        <small v-if="help" class="setting-row__help">{{ help }}</small>
      </template>
      <span v-else class="setting-row__plain">{{ value }}</span>
    </div>

    <div class="setting-row__trailing">
      <StatusBadge
        v-if="overridden || pendingRestore || locked"
        size="compact"
        :tone="locked ? 'neutral' : pendingRestore ? 'warning' : 'info'"
        :icon="locked ? 'off' : pendingRestore ? 'alert' : 'edit'"
      >
        {{ sourceLabel }}
      </StatusBadge>
      <AppButton
        v-if="overridden || pendingRestore"
        variant="secondary"
        :tone="overridden ? 'warning' : 'action'"
        size="compact"
        :disabled="disabled"
        @click="emit('toggle')"
      >
        {{ actionLabel }}
      </AppButton>
      <AppButton
        v-else-if="!locked"
        class="setting-row__ghost-action"
        variant="link"
        size="inline"
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
  grid-template-columns: 172px minmax(0, 1fr) auto;
  align-items: center;
  column-gap: var(--space-4);
  border-left: 2px solid transparent;
  padding: 9px 10px 9px 12px;
}

.setting-row--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}

.setting-row--editing {
  align-items: start;
  border-left-color: var(--color-action);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding-block: 11px;
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

.setting-row--active .setting-row__identity {
  color: var(--color-text);
}

.setting-row--editing .setting-row__identity {
  padding-top: 6px;
}

.setting-row__hint {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: 1px solid var(--color-border-control);
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-faint);
  font-size: 9px;
  font-weight: 700;
  line-height: 1;
  padding: 0;
  cursor: help;
}

.setting-row__value {
  display: grid;
  min-width: 0;
  justify-items: start;
  gap: 6px;
}

.setting-row__plain {
  color: var(--color-text);
  font-size: var(--text-body);
  font-variant-numeric: tabular-nums;
}

.setting-row__help {
  max-width: 56ch;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}

.setting-row__trailing {
  display: flex;
  min-height: 30px;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

.setting-row--editing .setting-row__trailing {
  padding-top: 4px;
}

.setting-row__ghost-action {
  opacity: 0;
  transition: opacity var(--duration-fast) var(--easing-standard);
}

.setting-row:hover .setting-row__ghost-action,
.setting-row:focus-within .setting-row__ghost-action {
  opacity: 1;
}

@media (hover: none) {
  .setting-row__ghost-action {
    opacity: 1;
  }
}

@media (max-width: 800px) {
  .setting-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .setting-row__identity {
    grid-column: 1 / -1;
  }

  .setting-row__ghost-action {
    opacity: 1;
  }
}
</style>
