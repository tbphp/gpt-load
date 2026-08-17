<script setup lang="ts">
import { computed } from 'vue'

import AppButton from '@/components/ui/AppButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    label: string
    detail: string
    sourceLabel: string
    actionLabel: string
    overridden: boolean
    disabled?: boolean
    appearance?: 'default' | 'ledger'
    valueLabel?: string
    divided?: boolean
    pendingRestore?: boolean
    locked?: boolean
  }>(),
  {
    disabled: false,
    appearance: 'default',
    valueLabel: undefined,
    divided: true,
    pendingRestore: false,
    locked: false,
  },
)

const emit = defineEmits<{ toggle: [] }>()
const sourceTone = computed<'neutral' | 'info' | 'warning'>(() => {
  if (props.locked) return 'neutral'
  if (props.pendingRestore) return 'warning'
  return props.overridden ? 'info' : 'neutral'
})
const sourceIcon = computed<'check' | 'edit' | 'alert' | 'off'>(() => {
  if (props.locked) return 'off'
  if (props.pendingRestore) return 'alert'
  return props.overridden ? 'edit' : 'check'
})
const actionTone = computed<'action' | 'warning'>(() => (props.overridden ? 'warning' : 'action'))
</script>

<template>
  <div
    class="runtime-override-row"
    :class="[`runtime-override-row--${appearance}`, { 'runtime-override-row--divided': divided }]"
  >
    <template v-if="appearance === 'ledger'">
      <div class="runtime-override-row__identity">
        <strong>{{ label }}</strong>
        <StatusBadge
          class="runtime-override-row__source"
          size="compact"
          :tone="sourceTone"
          :icon="sourceIcon"
        >
          {{ sourceLabel }}
        </StatusBadge>
      </div>
      <div class="runtime-override-row__value">
        <slot v-if="$slots.value" name="value" />
        <template v-else>
          <strong>{{ detail }}</strong>
          <small v-if="valueLabel">{{ valueLabel }}</small>
        </template>
      </div>
      <AppButton
        variant="secondary"
        :tone="actionTone"
        size="compact"
        :disabled="disabled"
        @click="emit('toggle')"
      >
        {{ actionLabel }}
      </AppButton>
    </template>
    <template v-else>
      <div class="runtime-override-row__identity">
        <strong>{{ label }}</strong>
        <small>{{ detail }}</small>
      </div>
      <StatusBadge
        class="runtime-override-row__source"
        size="compact"
        :tone="sourceTone"
        :icon="sourceIcon"
      >
        {{ sourceLabel }}
      </StatusBadge>
      <div v-if="$slots.value" class="runtime-override-row__value"><slot name="value" /></div>
      <AppButton
        variant="secondary"
        :tone="actionTone"
        size="lg"
        :disabled="disabled"
        @click="emit('toggle')"
      >
        {{ actionLabel }}
      </AppButton>
    </template>
  </div>
</template>

<style scoped>
.runtime-override-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, auto) auto;
  align-items: center;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.runtime-override-row__identity {
  display: grid;
  gap: var(--space-1);
}
.runtime-override-row__identity small {
  color: var(--color-text-muted);
}
.runtime-override-row__source {
  justify-self: start;
}
.runtime-override-row__value {
  min-width: 112px;
}
.runtime-override-row--ledger {
  grid-template-columns: minmax(160px, 1fr) minmax(140px, 0.8fr) auto;
  gap: var(--space-4);
  border: 0;
  border-radius: 0;
  padding: 11px 2px;
}
.runtime-override-row--ledger.runtime-override-row--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}
.runtime-override-row--ledger .runtime-override-row__identity strong {
  font-size: var(--text-meta);
}
.runtime-override-row--ledger .runtime-override-row__identity small,
.runtime-override-row--ledger .runtime-override-row__value small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.runtime-override-row--ledger .runtime-override-row__value {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}
.runtime-override-row--ledger .runtime-override-row__value > strong {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 520;
}
@media (max-width: 800px) {
  .runtime-override-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .runtime-override-row__identity {
    grid-column: 1 / -1;
  }
  .runtime-override-row__value {
    min-width: 0;
  }
  .runtime-override-row--ledger {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .runtime-override-row--ledger .runtime-override-row__identity {
    grid-column: 1;
    grid-row: 1;
  }
  .runtime-override-row--ledger .runtime-override-row__value {
    grid-column: 1;
    grid-row: 2;
  }
  .runtime-override-row--ledger > :deep(.app-button) {
    grid-column: 2;
    grid-row: 1 / span 2;
  }
  .runtime-override-row--ledger :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
