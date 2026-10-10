<script setup lang="ts">
import { Info } from '@lucide/vue'
import AppFormActions from './AppFormActions.vue'
import AppIcon from './AppIcon.vue'
import AppTooltip from './AppTooltip.vue'

defineProps<{
  title?: string
  hint?: string
  description?: string
  compact?: boolean
  addLabel?: string
  addDisabled?: boolean
}>()
defineEmits<{ add: [] }>()
</script>

<template>
  <section class="modern-form-section" :class="{ 'is-compact': compact }">
    <header
      v-if="title || description || addLabel || $slots.actions"
      class="modern-form-section-heading"
    >
      <div v-if="title || description">
        <div v-if="title" class="modern-form-section-title">
          <h3>{{ title }}</h3>
          <AppTooltip v-if="hint" :label="hint">
            <span
              class="modern-form-section-hint modern-help-trigger"
              tabindex="0"
              role="img"
              :aria-label="hint"
            >
              <AppIcon :icon="Info" size="xs" />
            </span>
          </AppTooltip>
        </div>
        <p v-if="description">{{ description }}</p>
      </div>
      <AppFormActions
        v-if="addLabel || $slots.actions"
        :add-label="addLabel"
        :add-disabled="addDisabled"
        @add="$emit('add')"
      >
        <slot name="actions" />
      </AppFormActions>
    </header>
    <div class="modern-form-section-body"><slot /></div>
  </section>
</template>

<style scoped>
.modern-form-section {
  min-width: 0;
}
.modern-form-section + .modern-form-section {
  padding-top: var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-form-section-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-3);
}
.modern-form-section-heading > div:first-child {
  flex: 1;
  min-width: 0;
}
.modern-form-section-heading h3 {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-form-section-title {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
}
.modern-form-section-hint {
  display: inline-flex;
  flex: none;
  color: var(--modern-muted);
  border-radius: var(--modern-radius-control);
  cursor: help;
}
.modern-form-section-heading p {
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-form-section-body {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-form-section.is-compact .modern-form-section-heading {
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-2);
}
.modern-form-section.is-compact .modern-form-section-body {
  gap: var(--modern-space-2);
}
.modern-form-section.is-compact + .modern-form-section.is-compact {
  padding-top: var(--modern-space-4);
}
</style>
