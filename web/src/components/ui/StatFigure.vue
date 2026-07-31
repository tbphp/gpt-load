<script setup lang="ts">
defineProps<{
  label: string
  value: string | number
  detail?: string
  detailTitle?: string
  detailAriaLabel?: string
}>()
</script>

<template>
  <div class="stat-figure">
    <span class="stat-figure__label">{{ label }}</span>
    <strong class="stat-figure__value">{{ value }}</strong>
    <span
      v-if="detail"
      class="stat-figure__detail"
      :title="detailTitle"
      :aria-label="detailAriaLabel"
    >
      {{ detail }}
    </span>
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
  font-weight: 400;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.stat-figure__value {
  color: var(--color-text);
  font-family: var(--font-serif);
  font-size: var(--stat-value);
  font-weight: 400;
  letter-spacing: -0.02em;
  line-height: 1;
}
.stat-figure__detail {
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  font-weight: 400;
}
</style>
