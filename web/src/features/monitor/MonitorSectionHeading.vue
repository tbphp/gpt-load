<script setup lang="ts">
import { CircleHelp } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppTooltip from '@/components/ui/AppTooltip.vue'

withDefaults(
  defineProps<{
    title: string
    description?: string
    id?: string
    meta?: string
  }>(),
  {
    description: undefined,
    id: undefined,
    meta: undefined,
  },
)

const { t } = useI18n()
</script>

<template>
  <header class="monitor-section-heading">
    <div class="monitor-section-heading__title">
      <h2 :id="id">{{ title }}</h2>
      <AppTooltip v-if="description" :content="description" side="bottom" align="start">
        <button type="button" class="monitor-section-heading__help" :aria-label="t('monitor.help')">
          <CircleHelp :size="15" stroke-width="1.8" aria-hidden="true" />
        </button>
      </AppTooltip>
    </div>
    <span v-if="meta" class="monitor-section-heading__meta">{{ meta }}</span>
    <div v-if="$slots.actions" class="monitor-section-heading__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.monitor-section-heading {
  display: flex;
  min-width: 0;
  min-height: 38px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  background: color-mix(in srgb, var(--color-surface-sunken) 52%, var(--color-surface));
  border-radius: var(--radius-tag);
  padding: 6px 10px;
}

.monitor-section-heading__title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2-5);
}

.monitor-section-heading__title::before {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-action);
  content: '';
}

.monitor-section-heading h2 {
  min-width: 0;
  margin: 0;
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
  line-height: var(--line-compact);
}

.monitor-section-heading__help {
  display: inline-flex;
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  margin: -5px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--color-text-faint);
  cursor: help;
  padding: 0;
}

.monitor-section-heading__help:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
}

.monitor-section-heading__help:focus-visible {
  outline: 2px solid var(--color-action);
  outline-offset: 2px;
}

.monitor-section-heading__meta {
  flex: 0 0 auto;
  margin-left: auto;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}

.monitor-section-heading__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: var(--space-2);
}

@media (max-width: 520px) {
  .monitor-section-heading {
    align-items: flex-start;
  }

  .monitor-section-heading__meta,
  .monitor-section-heading__actions {
    margin-left: auto;
  }

  .monitor-section-heading__help {
    width: 44px;
    height: 44px;
    margin: -11px;
  }
}
</style>
