<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeStatistics } from '@modern/api/home'
import { AppButton, AppPanel, AppSparkline } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'

const props = defineProps<{ report: HomeStatistics }>()
const { t, n, locale } = useI18n()
const clock = (value: number) =>
  new Date(value).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
const values = computed(() => props.report.series.map((point) => point.requests))
/* 每个桶一个悬停标签，读屏与鼠标共用同一句描述。 */
const pointLabels = computed(() =>
  props.report.series.map((point) =>
    t('home.trendPoint', {
      time: clock(point.startMs),
      requests: n(point.requests),
      failures: n(point.failures),
    }),
  ),
)
const rate = computed(() =>
  props.report.requests > 0
    ? (((props.report.requests - props.report.failures) / props.report.requests) * 100).toFixed(1)
    : undefined,
)
/* 窗口正好 24 小时，两端的时钟读数必然相同，右端写「现在」才说得通。 */
const startLabel = computed(() => clock(props.report.fromMs))
</script>

<template>
  <AppPanel :title="t('home.trend')" :description="t('home.trendWindow')" compact>
    <template #actions
      ><AppButton as-child size="xs" variant="text"
        ><RouterLink :to="{ name: 'modern-usage' }">{{
          t('pages.usage.title')
        }}</RouterLink></AppButton
      ></template
    >
    <div class="modern-home-trend">
      <p class="modern-home-trend-facts">
        <span
          ><strong>{{ formatCompactNumber(report.requests, locale) }}</strong
          >{{ t('home.requests') }}</span
        >
        <span v-if="rate"
          ><strong>{{ rate }}%</strong>{{ t('home.successRate') }}</span
        >
      </p>
      <AppSparkline
        :label="t('home.trend')"
        :values="values"
        :point-labels="pointLabels"
        class="modern-home-trend-chart"
      />
      <p class="modern-home-trend-axis">
        <span>{{ startLabel }}</span
        ><span>{{ t('home.now') }}</span>
      </p>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-home-trend {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-home-trend-facts {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--modern-space-1) var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-trend-facts strong {
  margin-inline-end: var(--modern-space-1-5);
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-home-trend-axis {
  display: flex;
  justify-content: space-between;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
</style>
