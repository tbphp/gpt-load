<script setup lang="ts">
import { CircleHelp, PencilLine, RotateCcw } from '@lucide/vue'

import AppTooltip from '@/components/ui/AppTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

withDefaults(
  defineProps<{
    title: string
    help?: string
    meta?: string
    sourceLabel: string
    actionLabel: string
    overridden?: boolean
    pendingRestore?: boolean
    disabled?: boolean
  }>(),
  {
    help: undefined,
    meta: undefined,
    overridden: false,
    pendingRestore: false,
    disabled: false,
  },
)
const emit = defineEmits<{ toggle: [] }>()
</script>

<template>
  <article class="setting-block" :class="{ 'setting-block--editing': overridden }">
    <header class="setting-block__heading">
      <div class="setting-block__identity">
        <strong>{{ title }}</strong>
        <AppTooltip v-if="help" :content="help">
          <button type="button" class="setting-block__hint" :aria-label="`${title} · ${help}`">
            <CircleHelp :size="13" aria-hidden="true" />
          </button>
        </AppTooltip>
      </div>
      <div class="setting-block__meta">
        <span v-if="meta" class="setting-block__count">{{ meta }}</span>
        <StatusBadge v-if="pendingRestore" size="compact" tone="warning" icon="alert">
          {{ sourceLabel }}
        </StatusBadge>
        <AppTooltip :content="actionLabel">
          <button
            type="button"
            class="setting-block__action"
            :class="`setting-block__action--${overridden ? 'warning' : 'action'}`"
            :aria-label="`${actionLabel} · ${title}`"
            :aria-pressed="overridden"
            :disabled="disabled"
            @click="emit('toggle')"
          >
            <RotateCcw v-if="overridden" :size="14" aria-hidden="true" />
            <PencilLine v-else :size="14" aria-hidden="true" />
          </button>
        </AppTooltip>
      </div>
    </header>

    <slot />
  </article>
</template>

<style scoped>
.setting-block {
  display: grid;
}

.setting-block__identity {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
}

.setting-block {
  gap: var(--space-3);
  border-left: 2px solid transparent;
  padding-left: 12px;
}

.setting-block--editing {
  border-left-color: var(--color-action);
}

.setting-block__heading {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-4);
}

.setting-block__identity strong {
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  font-weight: 600;
}

.setting-block--editing .setting-block__identity strong {
  color: var(--color-text);
}

.setting-block__hint {
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

.setting-block__hint:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.setting-block__hint:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.setting-block__meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.setting-block__count {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.setting-block__action {
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

.setting-block__action--action {
  color: var(--color-action);
}

.setting-block__action--action:hover:not(:disabled) {
  background: var(--color-action-soft);
}

.setting-block__action--warning {
  color: var(--color-warning);
}

.setting-block__action--warning:hover:not(:disabled) {
  background: var(--color-warning-bg);
}

.setting-block__action:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.setting-block__action:disabled {
  cursor: not-allowed;
  opacity: 0.46;
}

@media (max-width: 560px) {
  .setting-block__heading {
    grid-template-columns: minmax(0, 1fr);
  }

  .setting-block__meta {
    justify-content: flex-start;
  }
}
</style>
