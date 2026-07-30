<script setup lang="ts">
import { computed } from 'vue'

import type { HealthProblemKeyDto } from '@/app/resources/health'
import StatusBadge from '@/components/ui/StatusBadge.vue'

export interface ProblemKeyRowLabels {
  consecutiveFailures: string
  failureCategory: string
  statusCode: string
  statusUnavailable: string
  recoversAt: string
  validationProbe: string
  failureUnit?: string
  automaticRecovery?: string
  probeRecovery?: string
}

const props = withDefaults(
  defineProps<{
    problemKey: HealthProblemKeyDto
    tone: 'warning' | 'danger'
    statusLabel: string
    failureCategoryLabel: string
    labels: ProblemKeyRowLabels
    locale: string
    appearance?: 'detail' | 'compact'
  }>(),
  {
    appearance: 'detail',
  },
)

const recoveryTime = computed(() => {
  const value = props.problemKey.recovery.at
  if (value === null) return null
  return new Intl.DateTimeFormat(props.locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
})
const recoveryClock = computed(() => {
  const value = props.problemKey.recovery.at
  if (value === null) return null
  return new Intl.DateTimeFormat(props.locale, {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(value))
})
const compactFailureSummary = computed(() => {
  const unit = props.labels.failureUnit ? ` ${props.labels.failureUnit}` : ''
  const status = props.problemKey.last_status_code ?? props.labels.statusUnavailable
  return `${props.labels.consecutiveFailures} ${props.problemKey.consecutive_failure_count}${unit} · ${status}`
})
</script>

<template>
  <div
    class="problem-key-row"
    :class="[`problem-key-row--${tone}`, `problem-key-row--${appearance}`]"
    :aria-label="appearance === 'compact' ? `${statusLabel}: ${problemKey.mask}` : undefined"
  >
    <template v-if="appearance === 'compact'">
      <code class="problem-key-row__compact-mask" data-problem-key-mask>
        {{ problemKey.mask }}
      </code>
      <span class="problem-key-row__compact-summary" data-problem-key-summary>
        {{ compactFailureSummary }}
      </span>
      <span class="problem-key-row__compact-recovery" data-problem-key-recovery>
        <time v-if="problemKey.recovery.at && recoveryClock" :datetime="problemKey.recovery.at">
          {{ recoveryClock }} {{ labels.automaticRecovery ?? labels.recoversAt }}
        </time>
        <template v-else>{{ labels.probeRecovery ?? labels.validationProbe }}</template>
      </span>
    </template>

    <template v-else>
      <div class="problem-key-row__identity">
        <code data-problem-key-mask>{{ problemKey.mask }}</code>
        <StatusBadge :tone="tone" data-problem-key-status>
          {{ statusLabel }}
        </StatusBadge>
      </div>

      <dl class="problem-key-row__facts">
        <div>
          <dt>{{ labels.consecutiveFailures }}</dt>
          <dd data-problem-key-failures>{{ problemKey.consecutive_failure_count }}</dd>
        </div>
        <div>
          <dt>{{ labels.failureCategory }}</dt>
          <dd data-problem-key-category>{{ failureCategoryLabel }}</dd>
        </div>
        <div>
          <dt>{{ labels.statusCode }}</dt>
          <dd data-problem-key-http-status>
            {{ problemKey.last_status_code ?? labels.statusUnavailable }}
          </dd>
        </div>
        <div>
          <dt>{{ labels.recoversAt }}</dt>
          <dd data-problem-key-recovery>
            <time v-if="problemKey.recovery.at && recoveryTime" :datetime="problemKey.recovery.at">
              {{ recoveryTime }}
            </time>
            <template v-else>{{ labels.validationProbe }}</template>
          </dd>
        </div>
      </dl>
    </template>
  </div>
</template>

<style scoped>
.problem-key-row {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(150px, 0.8fr) minmax(0, 2.2fr);
  align-items: center;
  gap: var(--space-4);
  border-left: 3px solid var(--color-neutral);
  padding: var(--space-3) var(--space-4);
}
.problem-key-row--warning {
  border-left-color: var(--color-warning);
}
.problem-key-row--danger {
  border-left-color: var(--color-danger);
}
.problem-key-row__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.problem-key-row__identity code {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-md);
  font-weight: 700;
  overflow-wrap: anywhere;
}
.problem-key-row__facts {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(100px, 1fr));
  gap: var(--space-3);
  margin: 0;
}
.problem-key-row__facts div {
  min-width: 0;
}
.problem-key-row__facts dt {
  color: var(--color-text-faint);
  font-size: var(--text-xs);
}
.problem-key-row__facts dd {
  margin: var(--space-1) 0 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
@media (max-width: 900px) {
  .problem-key-row:not(.problem-key-row--compact) {
    grid-template-columns: minmax(0, 1fr);
  }
  .problem-key-row__facts {
    grid-template-columns: repeat(2, minmax(120px, 1fr));
  }
}
@media (max-width: 520px) {
  .problem-key-row__facts {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
