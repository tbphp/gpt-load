<script setup lang="ts">
defineProps<{
  label: string
  value: string | number
  detail?: string
}>()
</script>

<template>
  <div class="stat-figure">
    <span class="stat-figure__label" data-stat-label>{{ label }}</span>
    <strong class="stat-figure__value" data-stat-value>{{ value }}</strong>
    <span v-if="detail" class="stat-figure__detail" data-stat-detail>{{ detail }}</span>
  </div>
</template>

<style scoped>
.stat-figure {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--space-2);
}
.stat-figure__label {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-weight: 650;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.stat-figure__value {
  color: var(--color-text);
  font-family: var(--font-serif);
  font-size: clamp(2.25rem, 5vw, 3.5rem);
  font-weight: 500;
  letter-spacing: -0.035em;
  line-height: 1;
}
.stat-figure__detail {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
</style>
