<script setup lang="ts">
import { ArrowRight, ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessProtocol } from '@/api/control/types'
import type {
  RequestConversionTraceDto,
  RequestLogReasoningDto,
  RequestLogUpstreamAPI,
  RequestParameterChangeDto,
  RequestParameterEntryDto,
  RequestParameterSnapshotDto,
} from '@/app/resources/request-logs'
import CopyButton from '@/components/ui/CopyButton.vue'

import { formatLogReasoningBudget, reasoningBudgetSemantic } from './log-format'

const props = defineProps<{
  clientProtocol: AccessProtocol
  upstreamApi: RequestLogUpstreamAPI | null
  clientModel: string | null
  upstreamModel: string | null
  clientReasoning: RequestLogReasoningDto | null
  upstreamReasoning: RequestLogReasoningDto | null
  clientParameters: RequestParameterSnapshotDto | null
  conversionTrace: RequestConversionTraceDto | null
}>()

const { locale, t } = useI18n()
const clientProtocolLabel = computed(() => props.clientProtocol)
const upstreamAPILabel = computed(() =>
  props.upstreamApi === null ? t('monitor.logs.protocolConversion.notRecorded') : props.upstreamApi,
)
const clientReasoningLabel = computed(() => formatReasoning(props.clientReasoning, false))
const upstreamReasoningLabel = computed(() => formatReasoning(props.upstreamReasoning, true))
const targetParameters = computed(() => props.conversionTrace?.target ?? null)
const parameterRows = computed(() => buildParameterRows())
const parameterCopyValue = computed(() =>
  JSON.stringify(
    {
      client: props.clientParameters,
      converted_upstream: props.conversionTrace,
    },
    null,
    2,
  ),
)
const parameterHistoryUnavailable = computed(
  () => props.clientParameters === null && props.conversionTrace === null,
)
const parameterTruncated = computed(
  () => props.clientParameters?.truncated === true || targetParameters.value?.truncated === true,
)
const parameterRedacted = computed(() =>
  [...(props.clientParameters?.entries ?? []), ...(targetParameters.value?.entries ?? [])].some(
    ({ value }) => value.includes('[REDACTED]') || value.includes('[redacted]'),
  ),
)

interface ParameterRow {
  key: string
  sourcePath: string | null
  targetPath: string | null
  source: RequestParameterEntryDto | null
  target: RequestParameterEntryDto | null
  change: RequestParameterChangeDto
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
  if (value.mode !== null) details.push(value.mode)
  if (value.effort !== null) details.push(value.effort)
  if (value.budget_tokens !== null) {
    const semantic = reasoningBudgetSemantic(value.budget_tokens)
    if (semantic === 'dynamic') {
      details.push('auto')
    } else if (semantic === 'disabled') {
      details.push('disabled')
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

function buildParameterRows(): ParameterRow[] {
  const source = new Map(
    (props.clientParameters?.entries ?? []).map((entry) => [entry.path, entry]),
  )
  const target = new Map(
    (targetParameters.value?.entries ?? []).map((entry) => [entry.path, entry]),
  )
  const changes = props.conversionTrace?.changes ?? []
  if (changes.length > 0) {
    return changes.map((change, index) => ({
      key: `${change.source_path ?? ''}:${change.target_path ?? ''}:${index}`,
      sourcePath: change.source_path,
      targetPath: change.target_path,
      source: change.source_path === null ? null : (source.get(change.source_path) ?? null),
      target: change.target_path === null ? null : (target.get(change.target_path) ?? null),
      change,
    }))
  }
  const paths = [...new Set([...source.keys(), ...target.keys()])].sort()
  return paths.map((path) => {
    const sourceEntry = source.get(path) ?? null
    const targetEntry = target.get(path) ?? null
    const disposition: RequestParameterChangeDto['disposition'] =
      sourceEntry && targetEntry ? 'preserved' : sourceEntry ? 'dropped' : 'added'
    return {
      key: path,
      sourcePath: sourceEntry ? path : null,
      targetPath: targetEntry ? path : null,
      source: sourceEntry,
      target: targetEntry,
      change: {
        disposition,
        source_path: sourceEntry ? path : null,
        target_path: targetEntry ? path : null,
      },
    }
  })
}

function captureStateLabel(snapshot: RequestParameterSnapshotDto | null, trace = false): string {
  const state = trace ? props.conversionTrace?.state : snapshot?.state
  if (!state) return t('monitor.logs.protocolConversion.historyUnavailable')
  return t(`monitor.logs.protocolConversion.captureState.${state}`)
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

    <details class="log-conversion-parameters">
      <summary>
        <ChevronRight class="log-conversion-parameters__chevron" :size="15" aria-hidden="true" />
        <span>{{ t('monitor.logs.protocolConversion.parameters') }}</span>
        <span
          v-if="parameterHistoryUnavailable"
          class="log-conversion-parameters__badge log-conversion-parameters__badge--muted"
        >
          {{ t('monitor.logs.protocolConversion.history') }}
        </span>
        <span
          v-if="parameterTruncated"
          class="log-conversion-parameters__badge log-conversion-parameters__badge--warning"
        >
          {{ t('monitor.logs.protocolConversion.truncated') }}
        </span>
        <span
          v-if="parameterRedacted"
          class="log-conversion-parameters__badge log-conversion-parameters__badge--muted"
        >
          {{ t('monitor.logs.protocolConversion.redacted') }}
        </span>
      </summary>

      <div class="log-conversion-parameters__toolbar">
        <div>
          <span>{{ captureStateLabel(clientParameters) }}</span>
          <ArrowRight :size="13" aria-hidden="true" />
          <span>{{ captureStateLabel(targetParameters, true) }}</span>
        </div>
        <CopyButton
          v-if="!parameterHistoryUnavailable"
          :value="parameterCopyValue"
          :label="t('monitor.logs.protocolConversion.copyParameters')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
        />
      </div>

      <p v-if="parameterRows.length === 0" class="log-conversion-parameters__empty">
        {{
          parameterHistoryUnavailable
            ? t('monitor.logs.protocolConversion.historyUnavailable')
            : t('monitor.logs.protocolConversion.noSafeParameters')
        }}
      </p>
      <div v-else class="log-conversion-parameters__scroll">
        <div class="log-conversion-parameters__columns" aria-hidden="true">
          <span>{{ t('monitor.logs.protocolConversion.parameter') }}</span>
          <span>{{ t('monitor.logs.protocolConversion.clientRequest') }}</span>
          <span>{{ t('monitor.logs.protocolConversion.convertedUpstream') }}</span>
          <span>{{ t('monitor.logs.protocolConversion.dispositionLabel') }}</span>
        </div>
        <div v-for="row in parameterRows" :key="row.key" class="log-conversion-parameters__row">
          <code class="log-conversion-parameters__path">
            {{
              row.sourcePath === row.targetPath
                ? row.sourcePath
                : `${row.sourcePath ?? '—'} → ${row.targetPath ?? '—'}`
            }}
          </code>
          <code :data-label="t('monitor.logs.protocolConversion.clientRequest')">
            {{ row.source?.value ?? '—' }}
          </code>
          <code :data-label="t('monitor.logs.protocolConversion.convertedUpstream')">
            {{ row.target?.value ?? '—' }}
          </code>
          <span
            class="log-conversion-parameters__disposition"
            :class="`log-conversion-parameters__disposition--${row.change.disposition}`"
          >
            {{ t(`monitor.logs.protocolConversion.disposition.${row.change.disposition}`) }}
          </span>
        </div>
      </div>
    </details>
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

.log-conversion-parameters {
  margin-top: 10px;
  border-top: 1px solid color-mix(in srgb, var(--color-border-subtle) 82%, transparent);
  padding-top: 9px;
}

.log-conversion-parameters summary {
  display: flex;
  min-height: 28px;
  align-items: center;
  gap: 6px;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--text-label-xs);
  list-style: none;
}

.log-conversion-parameters summary::-webkit-details-marker {
  display: none;
}

.log-conversion-parameters__chevron {
  transition: transform 140ms ease;
}

.log-conversion-parameters[open] .log-conversion-parameters__chevron {
  transform: rotate(90deg);
}

.log-conversion-parameters__badge,
.log-conversion-parameters__disposition {
  border-radius: 999px;
  padding: 2px 7px;
  font-size: 10px;
  line-height: 1.35;
}

.log-conversion-parameters__badge--muted {
  background: color-mix(in srgb, var(--color-text-faint) 12%, transparent);
  color: var(--color-text-faint);
}

.log-conversion-parameters__badge--warning {
  background: color-mix(in srgb, var(--color-warning) 14%, transparent);
  color: var(--color-warning);
}

.log-conversion-parameters__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 5px 0 8px;
  color: var(--color-text-faint);
  font-size: 11px;
}

.log-conversion-parameters__toolbar > div {
  display: flex;
  align-items: center;
  gap: 6px;
}

.log-conversion-parameters__scroll {
  max-height: 344px;
  overflow: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}

.log-conversion-parameters__columns,
.log-conversion-parameters__row {
  display: grid;
  grid-template-columns: minmax(130px, 0.9fr) minmax(100px, 1fr) minmax(100px, 1fr) minmax(
      76px,
      auto
    );
  gap: 8px;
  align-items: center;
  padding: 7px 9px;
}

.log-conversion-parameters__columns {
  position: sticky;
  top: 0;
  z-index: 1;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
  color: var(--color-text-faint);
  font-size: 10px;
}

.log-conversion-parameters__row {
  border-bottom: 1px solid color-mix(in srgb, var(--color-border-subtle) 65%, transparent);
  font-size: 11px;
}

.log-conversion-parameters__row:last-child {
  border-bottom: 0;
}

.log-conversion-parameters__row code {
  min-width: 0;
  overflow-wrap: anywhere;
}

.log-conversion-parameters__path {
  color: var(--color-text-muted);
}

.log-conversion-parameters__disposition {
  justify-self: start;
  background: color-mix(in srgb, var(--color-text-faint) 10%, transparent);
  color: var(--color-text-muted);
  white-space: nowrap;
}

.log-conversion-parameters__disposition--mapped,
.log-conversion-parameters__disposition--normalized {
  background: color-mix(in srgb, var(--color-warning) 13%, transparent);
  color: var(--color-warning);
}

.log-conversion-parameters__disposition--dropped {
  background: color-mix(in srgb, var(--color-danger) 11%, transparent);
  color: var(--color-danger);
}

.log-conversion-parameters__disposition--added {
  background: color-mix(in srgb, var(--color-success) 11%, transparent);
  color: var(--color-success);
}

.log-conversion-parameters__empty {
  margin: 6px 0 2px;
  color: var(--color-text-faint);
  font-size: 11px;
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

  .log-conversion-parameters__columns {
    display: none;
  }

  .log-conversion-parameters__row {
    grid-template-columns: minmax(0, 1fr) minmax(76px, auto);
    align-items: start;
  }

  .log-conversion-parameters__path {
    grid-column: 1 / -1;
  }

  .log-conversion-parameters__row code:nth-of-type(2)::before,
  .log-conversion-parameters__row code:nth-of-type(3)::before {
    display: block;
    color: var(--color-text-faint);
    content: attr(data-label);
  }

  .log-conversion-parameters__disposition {
    grid-column: 2;
    grid-row: 2 / span 2;
  }

  .log-conversion-parameters__toolbar {
    align-items: flex-start;
  }
}
</style>
