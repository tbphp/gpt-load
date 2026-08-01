<script setup lang="ts">
import { Ban, CircleCheck, CirclePause, Clock3 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KeyCounts } from '@/api/control/types'

import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  counts: KeyCounts
  label: string
}>()

const { n } = useI18n()

const hasVisibleStatus = computed(
  () =>
    props.counts.available +
      props.counts.cooldown +
      props.counts.blacklisted +
      props.counts.disabled >
    0,
)
</script>

<template>
  <AppTooltip :content="label">
    <div class="key-health-figure" role="img" tabindex="0" :aria-label="label">
      <div class="key-health-counts" aria-hidden="true">
        <strong class="key-health-counts__total">{{ n(counts.total) }}</strong>
        <span v-if="hasVisibleStatus" class="key-health-counts__divider"></span>
        <span v-if="counts.available > 0" class="key-health-count key-health-count--available">
          <CircleCheck :size="12" />
          <span>{{ n(counts.available) }}</span>
        </span>
        <span v-if="counts.cooldown > 0" class="key-health-count key-health-count--cooldown">
          <Clock3 :size="12" />
          <span>{{ n(counts.cooldown) }}</span>
        </span>
        <span v-if="counts.blacklisted > 0" class="key-health-count key-health-count--blacklisted">
          <Ban :size="12" />
          <span>{{ n(counts.blacklisted) }}</span>
        </span>
        <span v-if="counts.disabled > 0" class="key-health-count key-health-count--disabled">
          <CirclePause :size="12" />
          <span>{{ n(counts.disabled) }}</span>
        </span>
      </div>

      <div class="key-health-bar" aria-hidden="true">
        <span
          v-if="counts.available > 0"
          class="key-health-bar__segment key-health-bar__segment--available"
          :style="{ flexGrow: counts.available }"
        ></span>
        <span
          v-if="counts.cooldown > 0"
          class="key-health-bar__segment key-health-bar__segment--cooldown"
          :style="{ flexGrow: counts.cooldown }"
        ></span>
        <span
          v-if="counts.blacklisted > 0"
          class="key-health-bar__segment key-health-bar__segment--blacklisted"
          :style="{ flexGrow: counts.blacklisted }"
        ></span>
        <span
          v-if="counts.disabled > 0"
          class="key-health-bar__segment key-health-bar__segment--disabled"
          :style="{ flexGrow: counts.disabled }"
        ></span>
      </div>
    </div>
  </AppTooltip>
</template>

<style scoped>
.key-health-figure {
  display: grid;
  width: min(100%, 250px);
  gap: 7px;
  border-radius: 4px;
}

.key-health-counts {
  display: flex;
  align-items: center;
  gap: 7px;
  font-family: var(--font-mono);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.key-health-counts__total {
  color: var(--color-text);
  font-size: 13px;
  font-weight: 650;
}

.key-health-counts__divider {
  width: 1px;
  height: 14px;
  background: var(--color-border-control);
}

.key-health-count {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-weight: 600;
  white-space: nowrap;
}

.key-health-count svg {
  flex: none;
}

.key-health-count--available {
  color: var(--color-success);
}

.key-health-count--cooldown {
  color: var(--color-warning);
}

.key-health-count--blacklisted {
  color: var(--color-danger);
}

.key-health-count--disabled {
  color: var(--color-neutral);
}

.key-health-bar {
  display: flex;
  width: 100%;
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
