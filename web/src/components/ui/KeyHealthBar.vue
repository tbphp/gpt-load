<script setup lang="ts">
import type { KeyCounts } from '@/api/control/types'

defineProps<{
  counts: KeyCounts
  label: string
}>()
</script>

<template>
  <div class="key-health-bar" role="img" :aria-label="label">
    <span
      v-if="counts.available > 0"
      class="key-health-bar__segment key-health-bar__segment--available"
      :style="{ flexGrow: counts.available }"
      aria-hidden="true"
    ></span>
    <span
      v-if="counts.cooldown > 0"
      class="key-health-bar__segment key-health-bar__segment--cooldown"
      :style="{ flexGrow: counts.cooldown }"
      aria-hidden="true"
    ></span>
    <span
      v-if="counts.blacklisted > 0"
      class="key-health-bar__segment key-health-bar__segment--blacklisted"
      :style="{ flexGrow: counts.blacklisted }"
      aria-hidden="true"
    ></span>
    <span
      v-if="counts.disabled > 0"
      class="key-health-bar__segment key-health-bar__segment--disabled"
      :style="{ flexGrow: counts.disabled }"
      aria-hidden="true"
    ></span>
  </div>
</template>

<style scoped>
.key-health-bar {
  display: flex;
  width: min(100%, 250px);
  height: var(--health-bar-height);
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-neutral-bg);
}

.key-health-bar__segment {
  min-width: 0;
  flex-basis: 0;
}

.key-health-bar__segment--available {
  background: var(--color-success);
}

.key-health-bar__segment--cooldown {
  background: var(--color-warning);
}

.key-health-bar__segment--blacklisted {
  background: var(--color-danger);
}

.key-health-bar__segment--disabled {
  background: var(--color-neutral);
}
</style>
