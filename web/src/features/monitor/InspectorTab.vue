<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { accessKeyOptionsQueryOptions } from '@/app/resources/access-keys'
import { enabledDataProtocols, protocolCatalog } from '@/api/control/protocols'
import {
  inspectRoute,
  type RouteInspectReasonCode,
  type RouteInspectRequest,
  type RouteInspectResponseDto,
} from '@/app/resources/route-inspection'
import type { AccessProtocol } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { monitorLocation } from '@/app/route-locations'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { formatISOInstant, formatLocalInstant } from '@/lib/format'

import InspectorForm from './InspectorForm.vue'

type InspectorField = 'protocol' | 'externalModel' | 'accessKey'
type InspectorErrors = Partial<Record<InspectorField, string>>

const knownReasons = new Set<RouteInspectReasonCode>([
  'access_key_disabled',
  'protocol_filtered',
  'model_filtered',
  'model_required_by_filter',
  'no_route_target',
  'group_disabled',
  'group_filtered',
  'no_available_group',
  'no_keys',
  'group_weight_zero',
  'key_disabled',
  'key_blacklisted',
  'key_cooldown',
  'key_weight_zero',
  'no_available_key',
])

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const draftProtocol = ref(readProtocol(route.query.protocol))
const draftModel = ref(readText(route.query.external_model))
const draftAccessKeyID = ref(readPositiveID(route.query.access_key_id))
const fieldErrors = ref<InspectorErrors>({})
const pending = ref(false)
const failed = ref(false)
const resultStale = ref(false)
const submitted = ref<RouteInspectRequest>()
const observation = ref<RouteInspectResponseDto>()
const resultSummary = ref<HTMLHeadingElement | null>(null)
const observationDateTime = computed(() =>
  observation.value === undefined ? undefined : formatISOInstant(observation.value.observed_at_ms),
)
let owner = 0
let controller: AbortController | undefined

const accessKeyOptionsQuery = useQuery(accessKeyOptionsQueryOptions(client))
const protocolOptions = computed(() =>
  protocolCatalog.map(({ value, labelKey }) => ({
    value,
    label: t(labelKey),
  })),
)
const missingAccessKeyOption = computed(
  () =>
    accessKeyOptionsQuery.isSuccess.value &&
    draftAccessKeyID.value !== '' &&
    !accessKeyOptionsQuery.data.value?.some(
      (accessKey) => String(accessKey.id) === draftAccessKeyID.value,
    ),
)
const accessKeyOptions = computed(() => {
  const options = (accessKeyOptionsQuery.data.value ?? []).map((accessKey) => ({
    value: String(accessKey.id),
    label: t('monitor.inspector.form.accessKeyOption', {
      name: accessKey.name,
      id: accessKey.id,
      status: t(`monitor.inspector.accessKeyStatus.${accessKey.status}`),
    }),
  }))
  if (!missingAccessKeyOption.value) return options
  return [
    {
      value: draftAccessKeyID.value,
      label: t('monitor.inspector.form.missingAccessKeyOption', {
        id: draftAccessKeyID.value,
      }),
    },
    ...options,
  ]
})
const inputChanged = computed(() => {
  const previous = submitted.value
  if (!previous) return false
  return (
    draftProtocol.value !== previous.protocol ||
    draftModel.value !== (previous.external_model ?? '') ||
    draftAccessKeyID.value !== String(previous.access_key_id)
  )
})

function readProtocol(raw: unknown): AccessProtocol | '' {
  return typeof raw === 'string' && enabledDataProtocols.some((protocol) => protocol === raw)
    ? (raw as AccessProtocol)
    : ''
}

function readText(raw: unknown): string {
  return typeof raw === 'string' && raw.trim() === raw && raw !== '' ? raw : ''
}

function readPositiveID(raw: unknown): string {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return ''
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? String(value) : ''
}

watch(
  () => [route.query.protocol, route.query.external_model, route.query.access_key_id] as const,
  ([rawProtocol, rawModel, rawAccessKeyID]) => {
    const protocol = readProtocol(rawProtocol)
    const model = readText(rawModel)
    const accessKeyID = readPositiveID(rawAccessKeyID)
    if (
      protocol === draftProtocol.value &&
      model === draftModel.value &&
      accessKeyID === draftAccessKeyID.value
    ) {
      return
    }

    owner += 1
    controller?.abort()
    controller = undefined
    draftProtocol.value = protocol
    draftModel.value = model
    draftAccessKeyID.value = accessKeyID
    fieldErrors.value = {}
    pending.value = false
    failed.value = false
    resultStale.value = false
    submitted.value = undefined
    observation.value = undefined
  },
)

function validatedRequest(): RouteInspectRequest | undefined {
  const errors: InspectorErrors = {}
  if (!enabledDataProtocols.some((protocol) => protocol === draftProtocol.value)) {
    errors.protocol = 'monitor.inspector.errors.protocol'
  }
  if (
    draftModel.value !== '' &&
    (draftModel.value.trim() !== draftModel.value || /[\u0000-\u001f\u007f]/.test(draftModel.value))
  ) {
    errors.externalModel = 'monitor.inspector.errors.model'
  }

  const accessKeyID = Number(draftAccessKeyID.value)
  if (
    !/^\d+$/.test(draftAccessKeyID.value) ||
    !Number.isSafeInteger(accessKeyID) ||
    accessKeyID <= 0 ||
    !accessKeyOptionsQuery.data.value?.some((accessKey) => accessKey.id === accessKeyID)
  ) {
    errors.accessKey = 'monitor.inspector.errors.accessKey'
  }
  fieldErrors.value = errors
  if (Object.keys(errors).length > 0) return undefined

  const request: RouteInspectRequest = {
    protocol: draftProtocol.value as AccessProtocol,
    access_key_id: accessKeyID,
  }
  if (draftModel.value !== '') request.external_model = draftModel.value
  return request
}

function setDraftProtocol(value: string): void {
  draftProtocol.value = value as AccessProtocol | ''
}

async function inspect(): Promise<void> {
  const request = validatedRequest()
  if (!request) return

  controller?.abort()
  const currentOwner = ++owner
  const currentController = new AbortController()
  controller = currentController
  pending.value = true
  failed.value = false
  const query: Record<string, string> = {
    tab: 'inspector',
    protocol: request.protocol,
    access_key_id: String(request.access_key_id),
  }
  if (request.external_model) query.external_model = request.external_model
  void router.replace(monitorLocation(query))

  try {
    const result = await inspectRoute(client, request, currentController.signal)
    if (currentOwner === owner && !currentController.signal.aborted) {
      submitted.value = request
      observation.value = result
      resultStale.value = false
      await nextTick()
      if (currentOwner === owner && !currentController.signal.aborted && !result.routable) {
        resultSummary.value?.focus()
      }
    }
  } catch (error: unknown) {
    if (
      currentOwner === owner &&
      !currentController.signal.aborted &&
      !(error instanceof RequestCancelledError)
    ) {
      failed.value = true
      resultStale.value = observation.value !== undefined
    }
  } finally {
    if (currentOwner === owner) {
      controller = undefined
      pending.value = false
    }
  }
}

function reasonLabel(reason: string | null): string {
  if (reason === null) return t('monitor.inspector.reasons.none')
  if (knownReasons.has(reason as RouteInspectReasonCode)) {
    return t(`monitor.inspector.reasons.${reason}`)
  }
  return t('monitor.inspector.reasons.unknown')
}

function nullableWeight(value: number | null): number | string {
  return value ?? t('monitor.inspector.weights.null')
}

function modelLabel(value: string | null): string {
  return value ?? t('monitor.inspector.result.modelNotSpecified')
}

function accessKeyStatusTone(status: 'active' | 'disabled'): 'success' | 'neutral' {
  return status === 'active' ? 'success' : 'neutral'
}

onBeforeUnmount(() => {
  owner += 1
  controller?.abort()
  controller = undefined
  pending.value = false
  failed.value = false
  resultStale.value = false
  submitted.value = undefined
  observation.value = undefined
})
</script>

<template>
  <div class="inspector-tab">
    <InspectorForm
      :protocol="draftProtocol"
      :model="draftModel"
      :access-key-id="draftAccessKeyID"
      :protocol-options="protocolOptions"
      :access-key-options="accessKeyOptions"
      :errors="fieldErrors"
      :options-pending="accessKeyOptionsQuery.isPending.value"
      :options-failed="accessKeyOptionsQuery.isError.value"
      :missing-access-key="missingAccessKeyOption"
      @update:protocol="setDraftProtocol"
      @update:model="draftModel = $event"
      @update:access-key-id="draftAccessKeyID = $event"
      @submit="inspect"
      @retry-options="accessKeyOptionsQuery.refetch()"
    />

    <QueryFeedback
      v-if="pending"
      state="loading"
      :message="t('monitor.inspector.request.loading')"
    />
    <QueryFeedback
      v-if="failed"
      state="error"
      :message="t('monitor.inspector.request.failed')"
      :retry-label="t('common.retry')"
      @retry="inspect"
    />

    <SurfaceCard v-if="observation" class="inspector-result" aria-live="polite" aria-atomic="true">
      <p v-if="inputChanged" class="inspector-input-changed" role="status">
        {{ t('monitor.inspector.result.inputChanged') }}
      </p>
      <p v-if="resultStale" class="inspector-input-changed" role="status">
        {{ t('monitor.inspector.result.stale') }}
      </p>

      <header class="inspector-heading inspector-result__heading">
        <div>
          <h2 ref="resultSummary" tabindex="-1">
            {{ t('monitor.inspector.result.title') }}
          </h2>
          <p>{{ t('monitor.inspector.boundary') }}</p>
        </div>
        <StatusBadge :tone="observation.routable ? 'success' : 'danger'">
          {{
            observation.routable
              ? t('monitor.inspector.result.routable')
              : t('monitor.inspector.result.notRoutable')
          }}
        </StatusBadge>
      </header>

      <div class="inspector-meta">
        <time :datetime="observationDateTime">{{
          t('monitor.inspector.result.observedAt', {
            time: formatLocalInstant(observation.observed_at_ms, locale),
          })
        }}</time>
        <span>{{
          t('monitor.inspector.result.revision', {
            revision: observation.snapshot_revision,
          })
        }}</span>
      </div>

      <dl class="inspector-facts">
        <div>
          <dt>{{ t('monitor.inspector.result.protocol') }}</dt>
          <dd>{{ observation.protocol }}</dd>
        </div>
        <div>
          <dt>{{ t('monitor.inspector.result.externalModel') }}</dt>
          <dd>{{ modelLabel(observation.external_model) }}</dd>
        </div>
        <div>
          <dt>{{ t('monitor.inspector.result.accessKey') }}</dt>
          <dd>{{ observation.access_key.name }} · #{{ observation.access_key.id }}</dd>
        </div>
        <div>
          <dt>{{ t('monitor.inspector.result.accessKeyStatus') }}</dt>
          <dd>
            <StatusBadge :tone="accessKeyStatusTone(observation.access_key.status)">
              {{ t(`monitor.inspector.accessKeyStatus.${observation.access_key.status}`) }}
            </StatusBadge>
          </dd>
        </div>
        <div class="inspector-facts__wide">
          <dt>{{ t('monitor.inspector.result.reason') }}</dt>
          <dd>
            {{ reasonLabel(observation.reason_code) }}
          </dd>
        </div>
      </dl>

      <section class="inspector-groups" aria-labelledby="inspector-groups-heading">
        <header class="inspector-section-heading">
          <h3 id="inspector-groups-heading">{{ t('monitor.inspector.groups.title') }}</h3>
          <p>{{ t('monitor.inspector.groups.description') }}</p>
        </header>

        <p v-if="observation.groups.length === 0" class="inspector-complete-empty">
          {{ t('monitor.inspector.groups.completeEmpty') }}
        </p>

        <div v-else class="inspector-group-list">
          <article
            v-for="group in observation.groups"
            :key="group.group_id"
            class="inspector-group"
          >
            <header class="inspector-group__heading">
              <div>
                <h4>{{ group.group_name }} · #{{ group.group_id }}</h4>
                <code>{{ modelLabel(group.upstream_model) }}</code>
              </div>
              <div class="inspector-statuses">
                <StatusBadge :tone="group.included ? 'success' : 'neutral'">
                  {{
                    group.included
                      ? t('monitor.inspector.groups.included')
                      : t('monitor.inspector.groups.excluded')
                  }}
                </StatusBadge>
                <StatusBadge :tone="group.routable ? 'success' : 'danger'">
                  {{
                    group.routable
                      ? t('monitor.inspector.result.routable')
                      : t('monitor.inspector.result.notRoutable')
                  }}
                </StatusBadge>
              </div>
            </header>

            <dl class="inspector-facts inspector-facts--compact">
              <div>
                <dt>{{ t('monitor.inspector.weights.manual') }}</dt>
                <dd>{{ nullableWeight(group.weight_manual) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.inspector.result.reason') }}</dt>
                <dd>{{ reasonLabel(group.reason_code) }}</dd>
              </div>
            </dl>

            <p v-if="group.keys.length === 0" class="inspector-keys-empty">
              {{ t('monitor.inspector.keys.noneReturned') }}
            </p>
            <div v-else class="inspector-key-list">
              <article v-for="key in group.keys" :key="key.key_id" class="inspector-key">
                <header class="inspector-key__heading">
                  <strong>{{ t('monitor.inspector.keys.identity', { id: key.key_id }) }}</strong>
                  <StatusBadge :tone="key.available ? 'success' : 'danger'">
                    {{
                      key.available
                        ? t('monitor.inspector.keys.available')
                        : t('monitor.inspector.keys.unavailable')
                    }}
                  </StatusBadge>
                </header>
                <dl class="inspector-key-facts">
                  <div>
                    <dt>{{ t('monitor.inspector.result.reason') }}</dt>
                    <dd>{{ reasonLabel(key.reason_code) }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.inspector.weights.manual') }}</dt>
                    <dd>
                      {{ nullableWeight(key.weight_manual) }}
                    </dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.inspector.weights.auto') }}</dt>
                    <dd>
                      {{ key.weight_auto }}
                    </dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.inspector.weights.effective') }}</dt>
                    <dd>
                      {{ key.effective_weight }}
                    </dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.inspector.keys.cooldownUntil') }}</dt>
                    <dd>
                      <AppDateTime
                        v-if="key.cooldown_until_ms !== null"
                        :instant="key.cooldown_until_ms"
                        :locale="locale"
                      />
                      <span v-else>{{ t('monitor.inspector.keys.none') }}</span>
                    </dd>
                  </div>
                </dl>
              </article>
            </div>
          </article>
        </div>
      </section>
    </SurfaceCard>
  </div>
</template>

<style scoped>
.inspector-tab,
.inspector-groups,
.inspector-group-list,
.inspector-key-list {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.inspector-result {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}

.inspector-heading,
.inspector-group__heading,
.inspector-key__heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-4);
}

.inspector-heading > div,
.inspector-group__heading > div {
  min-width: 0;
}

.inspector-heading h2,
.inspector-section-heading h3,
.inspector-group__heading h4 {
  margin: 0;
}

.inspector-heading p,
.inspector-section-heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}

.inspector-input-changed,
.inspector-complete-empty {
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-3);
}

.inspector-input-changed {
  border-color: var(--color-warning);
  background: var(--color-warning-bg);
  color: var(--color-text);
}

.inspector-meta,
.inspector-statuses {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.inspector-meta {
  color: var(--color-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.8125rem;
}

.inspector-facts,
.inspector-key-facts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.inspector-facts > div,
.inspector-key-facts > div {
  min-width: 0;
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: var(--space-3);
}

.inspector-facts__wide {
  grid-column: span 2;
}

.inspector-facts dt,
.inspector-key-facts dt {
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}

.inspector-facts dd,
.inspector-key-facts dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.inspector-group {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface-sunken);
  padding: var(--space-4);
}

.inspector-group__heading code {
  display: inline-block;
  margin-top: var(--space-1);
  color: var(--color-code);
  overflow-wrap: anywhere;
}

.inspector-group__heading h4 {
  overflow-wrap: anywhere;
}

.inspector-facts--compact {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.inspector-key {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.inspector-key-facts {
  grid-template-columns: repeat(5, minmax(0, 1fr));
}

.inspector-keys-empty {
  margin: 0;
  color: var(--color-text-muted);
}

.inspector-tab :deep(.query-feedback--error > span) {
  color: var(--color-text);
}

@media (max-width: 960px) {
  .inspector-facts,
  .inspector-key-facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .inspector-facts,
  .inspector-key-facts,
  .inspector-facts--compact {
    grid-template-columns: minmax(0, 1fr);
  }

  .inspector-heading,
  .inspector-group__heading {
    flex-direction: column;
  }

  .inspector-facts__wide {
    grid-column: auto;
  }
}
</style>
