<script setup lang="ts">
import { Ban, CircleCheck, CirclePause, Clock3 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialCounts, HealthCredentialCountsDto } from '@/api/control/types'

import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  counts: CredentialCounts | HealthCredentialCountsDto
  label: string
}>()

const { n } = useI18n()
const normalizedCounts = computed<CredentialCounts>(() =>
  'total' in props.counts
    ? props.counts
    : {
        total: props.counts.credentials,
        available: props.counts.available,
        cooldown: props.counts.cooldown,
        blacklisted: props.counts.blacklisted,
        disabled: 0,
      },
)

const hasVisibleStatus = computed(
  () =>
    normalizedCounts.value.available +
      normalizedCounts.value.cooldown +
      normalizedCounts.value.blacklisted +
      normalizedCounts.value.disabled >
    0,
)
</script>

<template>
  <AppTooltip :content="label">
    <div class="credential-health-figure" role="img" tabindex="0" :aria-label="label">
      <div class="credential-health-counts" aria-hidden="true">
        <strong class="credential-health-counts__total">{{ n(normalizedCounts.total) }}</strong>
        <span v-if="hasVisibleStatus" class="credential-health-counts__divider"></span>
        <span
          v-if="normalizedCounts.available > 0"
          class="credential-health-count credential-health-count--available"
        >
          <CircleCheck :size="12" />
          <span>{{ n(normalizedCounts.available) }}</span>
        </span>
        <span
          v-if="normalizedCounts.cooldown > 0"
          class="credential-health-count credential-health-count--cooldown"
        >
          <Clock3 :size="12" />
          <span>{{ n(normalizedCounts.cooldown) }}</span>
        </span>
        <span
          v-if="normalizedCounts.blacklisted > 0"
          class="credential-health-count credential-health-count--blacklisted"
        >
          <Ban :size="12" />
          <span>{{ n(normalizedCounts.blacklisted) }}</span>
        </span>
        <span
          v-if="normalizedCounts.disabled > 0"
          class="credential-health-count credential-health-count--disabled"
        >
          <CirclePause :size="12" />
          <span>{{ n(normalizedCounts.disabled) }}</span>
        </span>
      </div>

      <div class="credential-health-bar" aria-hidden="true">
        <span
          v-if="normalizedCounts.available > 0"
          class="credential-health-bar__segment credential-health-bar__segment--available"
          :style="{ flexGrow: normalizedCounts.available }"
        ></span>
        <span
          v-if="normalizedCounts.cooldown > 0"
          class="credential-health-bar__segment credential-health-bar__segment--cooldown"
          :style="{ flexGrow: normalizedCounts.cooldown }"
        ></span>
        <span
          v-if="normalizedCounts.blacklisted > 0"
          class="credential-health-bar__segment credential-health-bar__segment--blacklisted"
          :style="{ flexGrow: normalizedCounts.blacklisted }"
        ></span>
        <span
          v-if="normalizedCounts.disabled > 0"
          class="credential-health-bar__segment credential-health-bar__segment--disabled"
          :style="{ flexGrow: normalizedCounts.disabled }"
        ></span>
      </div>
    </div>
  </AppTooltip>
</template>

<style scoped>
.credential-health-figure {
  display: grid;
  width: min(100%, 250px);
  gap: 7px;
  border-radius: 4px;
}

.credential-health-counts {
  display: flex;
  align-items: center;
  gap: 7px;
  font-family: var(--font-mono);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.credential-health-counts__total {
  color: var(--color-text);
  font-size: 13px;
  font-weight: 650;
}

.credential-health-counts__divider {
  width: 1px;
  height: 14px;
  background: var(--color-border-control);
}

.credential-health-count {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-weight: 600;
  white-space: nowrap;
}

.credential-health-count svg {
  flex: none;
}

.credential-health-count--available {
  color: var(--color-success);
}

.credential-health-count--cooldown {
  color: var(--color-warning);
}

.credential-health-count--blacklisted {
  color: var(--color-danger);
}

.credential-health-count--disabled {
  color: var(--color-neutral);
}

.credential-health-bar {
  display: flex;
  width: 100%;
  height: var(--health-bar-height);
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-neutral-bg);
}

.credential-health-bar__segment {
  min-width: 0;
  flex-basis: 0;
}

.credential-health-bar__segment--available {
  background: var(--color-success);
}

.credential-health-bar__segment--cooldown {
  background: var(--color-warning);
}

.credential-health-bar__segment--blacklisted {
  background: var(--color-danger);
}

.credential-health-bar__segment--disabled {
  background: var(--color-neutral);
}
</style>
