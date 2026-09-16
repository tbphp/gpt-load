<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyCostLimitRuleStatusDto } from '@/api/control/types'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import { formatLocalInstant } from '@/lib/format'

const props = defineProps<{ rule: AccessKeyCostLimitRuleStatusDto }>()
const { locale, t } = useI18n()
const previewStartedAtMS = ref(Date.now())

type PeriodUnit = 'day' | 'hour' | 'minute' | 'second'

function relativePeriod(seconds: number): { value: number; unit: PeriodUnit } | null {
  if (!Number.isSafeInteger(seconds) || seconds <= 0) return null
  for (const candidate of [
    { seconds: 86_400, unit: 'day' as const },
    { seconds: 3_600, unit: 'hour' as const },
    { seconds: 60, unit: 'minute' as const },
  ]) {
    if (seconds % candidate.seconds === 0) {
      return { value: seconds / candidate.seconds, unit: candidate.unit }
    }
  }
  return { value: seconds, unit: 'second' }
}

const previewLabel = computed(() => {
  const period = relativePeriod(props.rule.period_seconds)
  if (!period) return t('accessKeys.costLimits.status.inactive')
  return new Intl.RelativeTimeFormat(locale.value, { numeric: 'always' }).format(
    period.value,
    period.unit,
  )
})

const previewEndsAtMS = computed(() => {
  const durationMS = props.rule.period_seconds * 1_000
  const endsAtMS = previewStartedAtMS.value + durationMS
  return Number.isSafeInteger(durationMS) && Number.isSafeInteger(endsAtMS) ? endsAtMS : null
})

const actualTooltip = computed(() => {
  if (props.rule.window_started_at_ms === null || props.rule.window_ends_at_ms === null) {
    return undefined
  }
  return t('accessKeys.costLimits.windowPeriod', {
    start: formatLocalInstant(props.rule.window_started_at_ms, locale.value),
    end: formatLocalInstant(props.rule.window_ends_at_ms, locale.value),
  })
})

const previewTooltip = computed(() => {
  if (previewEndsAtMS.value === null) return previewLabel.value
  return t('accessKeys.costLimits.inactiveWindowPeriod', {
    start: formatLocalInstant(previewStartedAtMS.value, locale.value),
    end: formatLocalInstant(previewEndsAtMS.value, locale.value),
  })
})

function refreshPreview(): void {
  previewStartedAtMS.value = Date.now()
}
</script>

<template>
  <AppRelativeTime
    v-if="rule.window_ends_at_ms !== null"
    :instant="rule.window_ends_at_ms"
    :locale="locale"
    :empty-label="t('accessKeys.costLimits.status.inactive')"
    :tooltip-content="actualTooltip"
    hint
  />
  <AppTooltip v-else :content="previewTooltip">
    <span
      class="access-key-cost-limit-window-time--preview"
      tabindex="0"
      @pointerenter="refreshPreview"
      @focus="refreshPreview"
    >
      {{ previewLabel }}
    </span>
  </AppTooltip>
</template>

<style scoped>
.access-key-cost-limit-window-time--preview {
  cursor: help;
  text-decoration: underline dotted;
  text-decoration-color: var(--color-border-control);
  text-underline-offset: 3px;
  white-space: nowrap;
}

.access-key-cost-limit-window-time--preview:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
  border-radius: 3px;
}
</style>
