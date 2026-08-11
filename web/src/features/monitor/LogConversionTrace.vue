<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { protocolLabelKey, upstreamAPILabelKey } from '@/api/control/protocols'
import type { AccessProtocol } from '@/api/control/types'
import type { RequestLogReasoningDto, RequestLogUpstreamAPI } from '@/app/resources/request-logs'

import { formatLogReasoningBudget, reasoningBudgetSemantic } from './log-format'

const props = defineProps<{
  clientProtocol: AccessProtocol
  upstreamApi: RequestLogUpstreamAPI | null
  clientModel: string | null
  upstreamModel: string | null
  clientReasoning: RequestLogReasoningDto | null
  upstreamReasoning: RequestLogReasoningDto | null
}>()

const { locale, t } = useI18n()
const knownReasoningValues = new Set([
  'none',
  'disabled',
  'off',
  'enabled',
  'auto',
  'adaptive',
  'pro',
  'standard',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
])

const clientProtocolLabel = computed(() => t(protocolLabelKey(props.clientProtocol)))
const upstreamAPILabel = computed(() =>
  props.upstreamApi === null
    ? t('monitor.logs.protocolConversion.notRecorded')
    : t(upstreamAPILabelKey(props.upstreamApi)),
)
const clientReasoningLabel = computed(() => formatReasoning(props.clientReasoning, false))
const upstreamReasoningLabel = computed(() => formatReasoning(props.upstreamReasoning, true))

function localizedReasoningValue(value: string): string {
  return knownReasoningValues.has(value) ? t(`monitor.logs.reasoningValue.${value}`) : value
}

function formatReasoning(value: RequestLogReasoningDto | null, upstream: boolean): string {
  if (value === null) {
    return t(
      upstream
        ? 'monitor.logs.protocolConversion.noneOrNotRecorded'
        : 'monitor.logs.drawer.reasoningNotSpecified',
    )
  }
  const details: string[] = []
  if (value.mode !== null) details.push(localizedReasoningValue(value.mode))
  if (value.effort !== null) details.push(localizedReasoningValue(value.effort))
  if (value.budget_tokens !== null) {
    const semantic = reasoningBudgetSemantic(value.budget_tokens)
    if (semantic === 'dynamic') {
      details.push(t('monitor.logs.reasoningValue.auto'))
    } else if (semantic === 'disabled') {
      details.push(t('monitor.logs.reasoningValue.disabled'))
    } else {
      details.push(
        t('monitor.logs.drawer.reasoningBudgetValue', {
          value: formatLogReasoningBudget(value.budget_tokens, locale.value),
        }),
      )
    }
  }
  return details.join(' · ') || t('monitor.logs.drawer.reasoningNotSpecified')
}
</script>

<template>
  <section
    class="log-conversion-trace"
    :aria-label="t('monitor.logs.protocolConversion.comparisonLabel')"
  >
    <header class="log-conversion-trace__header" aria-hidden="true">
      <span></span>
      <span>{{ t('monitor.logs.protocolConversion.clientRequest') }}</span>
      <ArrowRight :size="14" :stroke-width="1.8" />
      <span>{{ t('monitor.logs.protocolConversion.convertedUpstream') }}</span>
    </header>
    <dl>
      <div class="log-conversion-trace__row">
        <dt>{{ t('monitor.logs.protocolConversion.protocolAPI') }}</dt>
        <dd>
          <code>{{ clientProtocolLabel }}</code>
        </dd>
        <ArrowRight :size="14" :stroke-width="1.8" aria-hidden="true" />
        <dd :class="{ 'log-conversion-trace__missing': upstreamApi === null }">
          <code>{{ upstreamAPILabel }}</code>
        </dd>
      </div>
      <div class="log-conversion-trace__row">
        <dt>{{ t('monitor.logs.protocolConversion.model') }}</dt>
        <dd>
          <code>{{ clientModel ?? t('monitor.logs.drawer.modelNotSpecified') }}</code>
        </dd>
        <ArrowRight :size="14" :stroke-width="1.8" aria-hidden="true" />
        <dd>
          <code>{{ upstreamModel ?? t('monitor.logs.drawer.modelNotSpecified') }}</code>
        </dd>
      </div>
      <div class="log-conversion-trace__row">
        <dt>{{ t('monitor.logs.protocolConversion.reasoning') }}</dt>
        <dd>{{ clientReasoningLabel }}</dd>
        <ArrowRight :size="14" :stroke-width="1.8" aria-hidden="true" />
        <dd>{{ upstreamReasoningLabel }}</dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.log-conversion-trace {
  min-width: 0;
  border: 1px solid color-mix(in srgb, var(--color-warning) 24%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--color-warning) 5%, transparent);
  padding: 10px 12px;
}

.log-conversion-trace__header,
.log-conversion-trace__row {
  display: grid;
  grid-template-columns: minmax(72px, 0.55fr) minmax(0, 1fr) 16px minmax(0, 1fr);
  align-items: center;
  gap: 8px;
}

.log-conversion-trace__header {
  margin-bottom: 7px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.log-conversion-trace dl {
  display: grid;
  gap: 7px;
  margin: 0;
}

.log-conversion-trace__row dt,
.log-conversion-trace__row dd {
  min-width: 0;
  margin: 0;
  font-size: var(--text-label-xs);
  overflow-wrap: anywhere;
}

.log-conversion-trace__row dt {
  color: var(--color-text-faint);
}

.log-conversion-trace__row dd {
  color: var(--color-text);
}

.log-conversion-trace__row > svg,
.log-conversion-trace__header > svg {
  color: var(--color-warning);
}

.log-conversion-trace__missing {
  color: var(--color-warning) !important;
}

@media (max-width: 520px) {
  .log-conversion-trace__header {
    display: none;
  }

  .log-conversion-trace__row {
    grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1fr);
  }

  .log-conversion-trace__row dt {
    grid-column: 1 / -1;
  }
}
</style>
