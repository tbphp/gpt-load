<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageAggregate } from '@modern/api/usage'
import { AppPanel, AppSvg, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { percentage } from './usage-display'

const props = defineProps<{ summary: UsageAggregate }>()
const { t, locale } = useI18n()
const active = ref<string>()
const missing = computed(
  () =>
    !props.summary.total_tokens &&
    Boolean(props.summary.usage_missing_count || props.summary.partial_count),
)
const share = (value: number) =>
  props.summary.total_tokens ? (value / props.summary.total_tokens) * 100 : 0
const compact = (value: number) => (missing.value ? '—' : formatCompactNumber(value, locale.value))
const parts = computed(() => {
  let offset = 0
  return [
    { id: 'input', value: props.summary.uncached_input_tokens },
    { id: 'cache', value: props.summary.cache_read_tokens },
    {
      id: 'write',
      value:
        props.summary.cache_write_5m_tokens +
        props.summary.cache_write_1h_tokens +
        props.summary.cache_write_unknown_tokens,
    },
    { id: 'output', value: props.summary.output_tokens },
  ].map((part) => {
    const result = { ...part, share: share(part.value), offset }
    offset += result.share
    return result
  })
})
const hint = (part: { id: string; value: number }) =>
  `${t('usage.parts.' + part.id)} ${missing.value ? '—' : compact(part.value)} · ${props.summary.total_tokens ? percentage(share(part.value), locale.value) : '—'}`
const activePart = computed(() => parts.value.find((part) => part.id === active.value))
</script>

<template>
  <AppPanel :title="t('usage.composition')" compact>
    <div class="modern-usage-composition" @pointerleave="active = undefined">
      <AppTooltip :label="parts.map(hint).join('\n')">
        <div
          class="modern-usage-donut"
          role="img"
          :aria-label="parts.map(hint).join('; ')"
          tabindex="0"
        >
          <AppSvg viewBox="0 0 128 128" aria-hidden="true">
            <circle cx="64" cy="64" r="54" class="modern-usage-donut-track" />
            <circle
              v-for="part in parts.filter((part) => part.value > 0)"
              :key="part.id"
              cx="64"
              cy="64"
              r="54"
              pathLength="100"
              :stroke-dasharray="`${part.share} ${100 - part.share}`"
              :stroke-dashoffset="-part.offset"
              transform="rotate(-90 64 64)"
              :class="[
                'modern-usage-part-' + part.id,
                { 'is-muted': active && active !== part.id },
              ]"
              @pointerenter="active = part.id"
            />
          </AppSvg>
          <div class="modern-usage-donut-label">
            <strong>{{ compact(activePart?.value ?? summary.total_tokens) }}</strong>
            <span>{{ t(activePart ? 'usage.parts.' + activePart.id : 'usage.totalTokens') }}</span>
          </div>
        </div>
      </AppTooltip>
      <dl class="modern-usage-composition-legend">
        <div
          v-for="part in parts"
          :key="part.id"
          :class="{ 'is-active': active === part.id }"
          @pointerenter="active = part.id"
          @pointerleave="active = undefined"
          @focusin="active = part.id"
          @focusout="active = undefined"
        >
          <dt>
            <i :class="'modern-usage-part-' + part.id" aria-hidden="true" />{{
              t('usage.parts.' + part.id)
            }}
          </dt>
          <dd>{{ compact(part.value) }}</dd>
          <dd class="modern-usage-composition-share">
            {{ summary.total_tokens ? percentage(part.share, locale) : '—' }}
          </dd>
        </div>
      </dl>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-usage-composition {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-usage-donut {
  position: relative;
  width: 128px;
  height: 128px;
  margin-inline: auto;
}
.modern-usage-donut svg {
  display: block;
  width: 100%;
  height: 100%;
}
.modern-usage-donut circle {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-chart-donut-stroke);
}
.modern-usage-donut-track {
  color: var(--modern-progress-track);
}
.modern-usage-donut circle.is-muted {
  opacity: var(--modern-opacity-disabled);
}
.modern-usage-part-input {
  color: var(--modern-chart-input);
}
.modern-usage-part-cache {
  color: var(--modern-chart-cache);
}
.modern-usage-part-write {
  color: var(--modern-chart-write);
}
.modern-usage-part-output {
  color: var(--modern-chart-output);
}
.modern-usage-donut-label {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-1);
  pointer-events: none;
}
.modern-usage-donut-label strong {
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-usage-donut-label span {
  max-width: calc(100% - var(--modern-space-8));
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  text-align: center;
}
.modern-usage-composition-legend {
  display: grid;
  gap: var(--modern-space-1);
  margin: 0;
}
.modern-usage-composition-legend > div {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 72px 52px;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1);
  border-radius: var(--modern-radius-small);
  font-size: var(--modern-font-size-secondary);
}
.modern-usage-composition-legend > div.is-active {
  background: var(--modern-control-hover);
}
.modern-usage-composition-legend dt {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-usage-composition-legend i {
  width: var(--modern-space-2);
  height: var(--modern-space-2);
  flex: none;
  border-radius: var(--modern-radius-round);
  background: currentColor;
}
.modern-usage-composition-legend dd {
  min-width: 0;
  margin: 0;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.modern-usage-composition-share {
  color: var(--modern-muted);
}
@media (max-width: 1150px) and (width > 760px) {
  .modern-usage-composition {
    grid-template-columns: 1fr 2fr;
    align-items: center;
    gap: var(--modern-space-8);
  }
}
</style>
