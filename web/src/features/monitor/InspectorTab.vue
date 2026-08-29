<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { ArrowRight, ChevronRight, Route as RouteIcon } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessProtocol } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { accessKeyOptionsQueryOptions } from '@/app/resources/access-keys'
import { channelsQueryOptions } from '@/app/resources/channels'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import {
  inspectRoute,
  type RouteInspectCredentialDto,
  type RouteInspectGroupDto,
  type RouteInspectReasonCode,
  type RouteInspectRequest,
  type RouteInspectResponseDto,
} from '@/app/resources/route-inspection'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatISOInstant, formatInteger, formatLocalInstant, formatPercent } from '@/lib/format'

import { isValidMonitorText, normalizeMonitorText } from './filter-validation'
import InspectorForm from './InspectorForm.vue'
import MonitorSectionHeading from './MonitorSectionHeading.vue'
import {
  inspectorMonitorQuery,
  parseInspectorMonitorState,
  type InspectorMonitorState,
} from './monitor-route'

type InspectorField = 'protocol' | 'externalModel' | 'accessKey'
type InspectorErrors = Partial<Record<InspectorField, string>>
type StatusTone = 'success' | 'warning' | 'danger' | 'neutral'

const knownReasons = new Set<RouteInspectReasonCode>([
  'access_key_disabled',
  'access_key_expired',
  'protocol_filtered',
  'model_filtered',
  'model_required_by_filter',
  'operation_unsupported',
  'native_route_required',
  'no_route_target',
  'group_disabled',
  'group_filtered',
  'no_available_group',
  'no_credentials',
  'group_weight_zero',
  'credential_disabled',
  'credential_blacklisted',
  'credential_cooldown',
  'credential_auth_unavailable',
  'credential_weight_zero',
  'credential_not_allowed',
  'no_available_credential',
])
const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const routeState = computed(() => parseInspectorMonitorState(route.query))
const draftProtocol = ref(readProtocol(routeState.value.protocol))
const draftModel = ref(readText(routeState.value.externalModel))
const draftAccessKeyID = ref(readPositiveID(routeState.value.accessKeyID))
const fieldErrors = ref<InspectorErrors>({})
const pending = ref(false)
const failed = ref(false)
const resultStale = ref(false)
const submitted = ref<RouteInspectRequest>()
const observation = ref<RouteInspectResponseDto>()
const resultLoadingActive = computed(() => pending.value && observation.value === undefined)
const resultLoading = useStableLoading(resultLoadingActive)
const resultRefreshing = computed(() => pending.value && observation.value !== undefined)
const resultSummary = ref<HTMLHeadingElement | null>(null)
const observationDateTime = computed(() =>
  observation.value === undefined ? undefined : formatISOInstant(observation.value.observed_at_ms),
)
let owner = 0
let controller: AbortController | undefined

const accessKeyOptionsQuery = useQuery(accessKeyOptionsQueryOptions(client))
const groupOptionsQuery = useQuery(groupOptionsQueryOptions(client))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const protocolOptions = computed(() => [
  { value: '', label: t('monitor.inspector.form.selectProtocol') },
  ...enabledDataProtocols.map((value) => ({ value, label: value })),
])
const configuredModels = computed(() =>
  [...new Set((groupOptionsQuery.data.value ?? []).flatMap((group) => group.models))].sort(
    (left, right) => left.localeCompare(right),
  ),
)
const missingModelOption = computed(
  () =>
    groupOptionsQuery.isSuccess.value &&
    draftModel.value !== '' &&
    !configuredModels.value.includes(draftModel.value),
)
const modelOptions = computed(() => [
  { value: '', label: t('monitor.inspector.form.selectModel') },
  ...(missingModelOption.value
    ? [
        {
          value: draftModel.value,
          label: t('monitor.inspector.form.missingModelOption', { model: draftModel.value }),
        },
      ]
    : []),
  ...configuredModels.value.map((model) => ({ value: model, label: model })),
])
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
  return [
    { value: '', label: t('monitor.inspector.form.selectAccessKey') },
    ...(missingAccessKeyOption.value
      ? [
          {
            value: draftAccessKeyID.value,
            label: t('monitor.inspector.form.missingAccessKeyOption', {
              id: draftAccessKeyID.value,
            }),
          },
        ]
      : []),
    ...options,
  ]
})
const optionsPending = computed(
  () => accessKeyOptionsQuery.isPending.value || groupOptionsQuery.isPending.value,
)
const optionsFailed = computed(
  () => accessKeyOptionsQuery.isError.value || groupOptionsQuery.isError.value,
)
const channelsByID = computed<Record<string, string>>(() =>
  Object.fromEntries(
    (channelsQuery.data.value?.items ?? []).map((channel) => [channel.channel_id, channel.name]),
  ),
)
const inputChanged = computed(() => {
  const previous = submitted.value
  if (!previous) return false
  return (
    draftProtocol.value !== previous.protocol ||
    draftModel.value !== previous.external_model ||
    draftAccessKeyID.value !== String(previous.access_key_id)
  )
})
const includedGroups = computed(() =>
  (observation.value?.groups ?? []).filter((group) => group.included),
)
const excludedGroups = computed(() =>
  (observation.value?.groups ?? []).filter((group) => !group.included),
)
const orderedIncludedGroups = computed(() =>
  [...includedGroups.value].sort((left, right) => {
    const routeModeOrder = routeModePriority(left) - routeModePriority(right)
    if (routeModeOrder !== 0) return routeModeOrder
    if (left.routable !== right.routable) return left.routable ? -1 : 1
    const weightOrder = groupEffectiveWeight(right) - groupEffectiveWeight(left)
    return weightOrder !== 0 ? weightOrder : left.group_id - right.group_id
  }),
)
const activeRouteMode = computed<'native' | 'converted' | null>(() => {
  if (includedGroups.value.some((group) => group.routable && group.route_mode === 'native')) {
    return 'native'
  }
  if (includedGroups.value.some((group) => group.routable && group.route_mode === 'converted')) {
    return 'converted'
  }
  return null
})
const activeTierGroups = computed(() =>
  activeRouteMode.value === null
    ? []
    : includedGroups.value.filter(
        (group) => group.routable && group.route_mode === activeRouteMode.value,
      ),
)
const availableCredentialCount = computed(() =>
  activeTierGroups.value.reduce((total, group) => total + groupAvailableCredentialCount(group), 0),
)
const totalEffectiveWeight = computed(() =>
  activeTierGroups.value.reduce((total, group) => total + groupEffectiveWeight(group), 0),
)

function readProtocol(raw: unknown): AccessProtocol | '' {
  return typeof raw === 'string' && enabledDataProtocols.some((protocol) => protocol === raw)
    ? (raw as AccessProtocol)
    : ''
}

function readText(raw: unknown): string {
  return normalizeMonitorText(raw) ?? ''
}

function readPositiveID(raw: unknown): string {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return ''
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? String(value) : ''
}

function channelName(channelID: string): string {
  return channelsByID.value[channelID]?.trim() || channelID
}

function includedGroupIdentity(group: RouteInspectGroupDto): string {
  return `#${group.group_id} · ${channelName(group.channel_id)} · ${modelLabel(group.upstream_model)}`
}

function excludedGroupIdentity(group: RouteInspectGroupDto): string {
  return `#${group.group_id} · ${channelName(group.channel_id)} · ${t(
    `monitor.inspector.routeModes.${group.route_mode}`,
  )} · ${modelLabel(group.upstream_model)}`
}

watch(
  () =>
    [
      routeState.value.protocol,
      routeState.value.externalModel,
      routeState.value.accessKeyID,
      routeState.value.run,
    ] as const,
  ([rawProtocol, rawModel, rawAccessKeyID, run]) => {
    const protocol = readProtocol(rawProtocol)
    const model = readText(rawModel)
    const accessKeyID = readPositiveID(rawAccessKeyID)
    const fieldsChanged =
      protocol !== draftProtocol.value ||
      model !== draftModel.value ||
      accessKeyID !== draftAccessKeyID.value
    if (!fieldsChanged && run) return

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
    draftModel.value === '' ||
    !isValidMonitorText(draftModel.value) ||
    !configuredModels.value.includes(draftModel.value)
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
    external_model: draftModel.value,
    access_key_id: accessKeyID,
  }
  return request
}

function setDraftProtocol(value: string): void {
  draftProtocol.value = value as AccessProtocol | ''
}

async function inspect(): Promise<void> {
  const request = validatedRequest()
  if (!request) return

  const nextState: InspectorMonitorState = {
    protocol: request.protocol,
    externalModel: request.external_model,
    accessKeyID: String(request.access_key_id),
    run: true,
    expandedGroupIDs: [],
  }
  const current = routeState.value
  if (
    current.run &&
    current.protocol === nextState.protocol &&
    current.externalModel === nextState.externalModel &&
    current.accessKeyID === nextState.accessKeyID
  ) {
    await runInspection(request)
    return
  }
  await router.push(monitorLocation(inspectorMonitorQuery(nextState)))
}

async function runInspection(request: RouteInspectRequest): Promise<void> {
  controller?.abort()
  const currentOwner = ++owner
  const currentController = new AbortController()
  controller = currentController
  pending.value = true
  failed.value = false
  submitted.value = request

  try {
    const result = await inspectRoute(client, request, currentController.signal)
    if (currentOwner === owner && !currentController.signal.aborted) {
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

watch(
  [
    () => routeState.value.run,
    () => routeState.value.protocol,
    () => routeState.value.externalModel,
    () => routeState.value.accessKeyID,
    () => accessKeyOptionsQuery.data.value,
    () => groupOptionsQuery.data.value,
  ],
  ([run]) => {
    if (!run) return
    const request = validatedRequest()
    if (!request) return
    if (pending.value && sameInspectionRequest(submitted.value, request)) {
      return
    }
    if (observation.value !== undefined && sameInspectionRequest(submitted.value, request)) {
      return
    }
    void runInspection(request)
  },
  { immediate: true },
)

function sameInspectionRequest(
  left: RouteInspectRequest | undefined,
  right: RouteInspectRequest,
): boolean {
  return (
    left?.protocol === right.protocol &&
    left.external_model === right.external_model &&
    left.access_key_id === right.access_key_id
  )
}

function retryOptions(): void {
  void Promise.all([accessKeyOptionsQuery.refetch(), groupOptionsQuery.refetch()])
}

function reasonLabel(reason: string | null): string {
  if (reason === null) return t('monitor.inspector.reasons.none')
  if (knownReasons.has(reason as RouteInspectReasonCode)) {
    return t(`monitor.inspector.reasons.${reason}`)
  }
  return t('monitor.inspector.reasons.unknown')
}

function modelLabel(value: string | null): string {
  return value ?? t('monitor.inspector.result.modelNotSpecified')
}

function protocolLabel(value: AccessProtocol): string {
  return value
}

function formattedInteger(value: number): string {
  return formatInteger(value, locale.value)
}

function nullableWeight(value: number | null): string {
  return value === null ? t('monitor.inspector.weights.none') : formattedInteger(value)
}

function accessKeyStatusTone(status: 'active' | 'disabled'): 'success' | 'neutral' {
  return status === 'active' ? 'success' : 'neutral'
}

function routeModePriority(group: RouteInspectGroupDto): number {
  return group.route_mode === 'native' ? 0 : 1
}

function routePriorityTone(group: RouteInspectGroupDto): StatusTone {
  if (!group.routable) return 'neutral'
  if (group.route_mode !== activeRouteMode.value) return 'neutral'
  return group.route_mode === 'native' ? 'success' : 'warning'
}

function routePriorityLabel(group: RouteInspectGroupDto): string {
  return t(`monitor.inspector.groups.priority.${group.route_mode}`)
}

function groupStatusLabel(group: RouteInspectGroupDto): string {
  if (!group.routable) return t('monitor.inspector.result.notRoutable')
  return group.route_mode === activeRouteMode.value
    ? t('monitor.inspector.groups.weightedCandidate')
    : t('monitor.inspector.groups.fallbackCandidate')
}

function credentialTone(credential: RouteInspectCredentialDto): StatusTone {
  if (credential.available) return 'success'
  if (credential.reason_code === 'credential_cooldown') return 'warning'
  if (credential.reason_code === 'credential_blacklisted') return 'danger'
  return 'neutral'
}

function credentialStatusLabel(credential: RouteInspectCredentialDto): string {
  return credential.available
    ? t('monitor.inspector.credentials.available')
    : reasonLabel(credential.reason_code)
}

function groupAvailableCredentialCount(group: RouteInspectGroupDto): number {
  return group.credentials.filter((credential) => credential.available).length
}

function groupEffectiveWeight(group: RouteInspectGroupDto): number {
  return group.credentials.reduce(
    (total, credential) => total + (credential.available ? credential.effective_weight : 0),
    0,
  )
}

function groupShare(group: RouteInspectGroupDto): number {
  if (group.route_mode !== activeRouteMode.value) return 0
  const total = totalEffectiveWeight.value
  if (total <= 0) return 0
  return Math.round((groupEffectiveWeight(group) / total) * 1_000) / 10
}

function groupShareLabel(group: RouteInspectGroupDto): string {
  if (group.route_mode !== activeRouteMode.value) return t('monitor.inspector.groups.standbyShare')
  return formatPercent(groupEffectiveWeight(group), totalEffectiveWeight.value, locale.value)
}

function candidateCredentialSummary(group: RouteInspectGroupDto): string {
  const available = groupAvailableCredentialCount(group)
  return t('monitor.inspector.credentials.summary', {
    available: formattedInteger(available),
    unavailable: formattedInteger(group.credentials.length - available),
  })
}

function orderedCredentials(group: RouteInspectGroupDto): RouteInspectCredentialDto[] {
  return [...group.credentials].sort((left, right) => {
    if (left.available !== right.available) return left.available ? -1 : 1
    const weightOrder = right.effective_weight - left.effective_weight
    return weightOrder !== 0 ? weightOrder : left.credential_id - right.credential_id
  })
}

function groupExpanded(groupID: number): boolean {
  return routeState.value.expandedGroupIDs.includes(groupID)
}

function setGroupExpanded(groupID: number, event: Event): void {
  const expanded = (event.currentTarget as HTMLDetailsElement).open
  const current = new Set(routeState.value.expandedGroupIDs)
  if (expanded === current.has(groupID)) return
  if (expanded) current.add(groupID)
  else current.delete(groupID)
  void router.push(
    monitorLocation(inspectorMonitorQuery({ ...routeState.value, expandedGroupIDs: [...current] })),
  )
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
      :model-options="modelOptions"
      :access-key-options="accessKeyOptions"
      :errors="fieldErrors"
      :options-pending="optionsPending"
      :options-failed="optionsFailed"
      :missing-access-key="missingAccessKeyOption"
      :submit-pending="pending"
      @update:protocol="setDraftProtocol"
      @update:model="draftModel = $event"
      @update:access-key-id="draftAccessKeyID = $event"
      @submit="inspect"
      @retry-options="retryOptions"
    />

    <div class="inspector-stack">
      <AsyncRefreshIndicator
        :active="resultRefreshing"
        :label="t('monitor.inspector.request.loading')"
      />
      <SkeletonSurface
        v-if="resultLoadingActive || resultLoading"
        variant="detail"
        min-height="330px"
        :concealed="!resultLoading"
        :label="t('monitor.inspector.request.loading')"
      />
      <QueryFeedback
        v-else-if="failed && !observation"
        state="error"
        :message="t('monitor.inspector.request.failed')"
        :retry-label="t('common.retry')"
        @retry="inspect"
      />

      <EmptyState
        v-else-if="!observation"
        class="inspector-empty"
        variant="ledger"
        heading-as="h2"
        :title="t('monitor.inspector.empty.title')"
        :description="t('monitor.inspector.empty.description')"
      >
        <template #icon><RouteIcon :size="22" stroke-width="1.7" /></template>
      </EmptyState>

      <template v-else>
        <InlineFeedback v-if="pending" tone="info" appearance="ledger">
          {{ t('monitor.inspector.request.loading') }}
        </InlineFeedback>
        <InlineFeedback v-if="inputChanged && !pending" tone="warning" appearance="ledger">
          {{ t('monitor.inspector.result.inputChanged') }}
        </InlineFeedback>
        <InlineFeedback v-if="resultStale" tone="warning" appearance="ledger">
          {{ t('monitor.inspector.result.stale') }}
          <template #action>
            <AppButton variant="link" size="inline" @click="inspect">
              {{ t('common.retry') }}
            </AppButton>
          </template>
        </InlineFeedback>

        <section
          class="route-summary"
          :class="observation.routable ? 'route-summary--success' : 'route-summary--danger'"
          aria-labelledby="route-summary-title"
        >
          <header class="route-summary__header">
            <div class="route-summary__content">
              <div class="route-summary__title-row">
                <h2 id="route-summary-title" ref="resultSummary" tabindex="-1">
                  {{
                    observation.routable
                      ? t('monitor.inspector.result.routableTitle')
                      : t('monitor.inspector.result.notRoutableTitle')
                  }}
                </h2>
                <StatusBadge :tone="observation.routable ? 'success' : 'danger'" size="compact">
                  {{
                    observation.routable
                      ? t('monitor.inspector.result.routable')
                      : t('monitor.inspector.result.notRoutable')
                  }}
                </StatusBadge>
              </div>
              <p class="route-summary__reason">
                {{
                  t('monitor.inspector.result.reasonLine', {
                    reason: reasonLabel(observation.reason_code),
                  })
                }}
                <code v-if="observation.reason_code">{{ observation.reason_code }}</code>
              </p>
            </div>
            <div class="route-summary__meta">
              <time :datetime="observationDateTime">
                {{
                  t('monitor.inspector.result.observedAt', {
                    time: formatLocalInstant(observation.observed_at_ms, locale),
                  })
                }}
              </time>
              <span>
                {{
                  t('monitor.inspector.result.revision', {
                    revision: observation.snapshot_revision,
                  })
                }}
              </span>
            </div>
          </header>

          <dl class="route-facts">
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.accessKey') }}</dt>
              <OverflowTooltip
                as="dd"
                :content="`${observation.access_key.name} · #${observation.access_key.id}`"
              >
                {{ observation.access_key.name }} · #{{ observation.access_key.id }}
              </OverflowTooltip>
            </div>
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.accessKeyStatus') }}</dt>
              <OverflowTooltip
                as="dd"
                :content="t(`monitor.inspector.accessKeyStatus.${observation.access_key.status}`)"
              >
                <StatusBadge
                  :tone="accessKeyStatusTone(observation.access_key.status)"
                  size="compact"
                >
                  {{ t(`monitor.inspector.accessKeyStatus.${observation.access_key.status}`) }}
                </StatusBadge>
              </OverflowTooltip>
            </div>
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.protocol') }}</dt>
              <OverflowTooltip
                as="dd"
                class="route-fact__mono"
                :content="`${protocolLabel(observation.protocol)} · ${observation.operation}`"
              >
                {{ protocolLabel(observation.protocol) }} · {{ observation.operation }}
              </OverflowTooltip>
            </div>
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.externalModel') }}</dt>
              <OverflowTooltip
                as="dd"
                class="route-fact__mono"
                :content="modelLabel(observation.external_model)"
              >
                {{ modelLabel(observation.external_model) }}
              </OverflowTooltip>
            </div>
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.candidateGroups') }}</dt>
              <OverflowTooltip
                as="dd"
                class="route-fact__number"
                :content="formattedInteger(includedGroups.length)"
              >
                {{ formattedInteger(includedGroups.length) }}
              </OverflowTooltip>
            </div>
            <div class="route-fact">
              <dt>{{ t('monitor.inspector.result.availableCredentials') }}</dt>
              <OverflowTooltip
                as="dd"
                class="route-fact__number"
                :content="formattedInteger(availableCredentialCount)"
              >
                {{ formattedInteger(availableCredentialCount) }}
              </OverflowTooltip>
            </div>
          </dl>
        </section>

        <section class="route-section" aria-labelledby="route-candidates-title">
          <MonitorSectionHeading
            id="route-candidates-title"
            :title="t('monitor.inspector.groups.title')"
            :description="t('monitor.inspector.groups.description')"
            :meta="
              t('monitor.inspector.groups.count', {
                count: formattedInteger(includedGroups.length),
              })
            "
          />

          <InlineFeedback v-if="includedGroups.length === 0" tone="neutral" appearance="ledger">
            {{ t('monitor.inspector.groups.completeEmpty') }}
          </InlineFeedback>

          <div
            v-else
            class="route-candidate-ledger"
            role="table"
            :aria-label="t('monitor.inspector.groups.tableLabel')"
            :aria-rowcount="orderedIncludedGroups.length + 1"
          >
            <div class="route-candidate-ledger__header" role="row" aria-rowindex="1">
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.group') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.status') }}</span>
              <span role="columnheader">{{
                t('monitor.inspector.groups.columns.credentials')
              }}</span>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.weight') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.share') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.actions') }}</span>
            </div>

            <details
              v-for="(group, index) in orderedIncludedGroups"
              :key="group.group_id"
              class="route-candidate"
              role="row"
              :aria-rowindex="index + 2"
              :open="groupExpanded(group.group_id)"
              @toggle="setGroupExpanded(group.group_id, $event)"
            >
              <summary class="route-candidate__summary">
                <div class="route-candidate__identity" role="cell">
                  <OverflowTooltip as="strong" :content="group.group_name">
                    {{ group.group_name }}
                  </OverflowTooltip>
                  <OverflowTooltip as="small" :content="includedGroupIdentity(group)">
                    {{ includedGroupIdentity(group) }}
                  </OverflowTooltip>
                </div>
                <div class="route-candidate__status" role="cell">
                  <span class="route-cell-label">{{
                    t('monitor.inspector.groups.columns.status')
                  }}</span>
                  <StatusBadge :tone="routePriorityTone(group)" size="compact">
                    {{ routePriorityLabel(group) }}
                  </StatusBadge>
                  <small>{{ groupStatusLabel(group) }}</small>
                </div>
                <div class="route-candidate__measure" role="cell">
                  <span class="route-cell-label">{{
                    t('monitor.inspector.groups.columns.credentials')
                  }}</span>
                  <strong>
                    {{ formattedInteger(groupAvailableCredentialCount(group)) }} /
                    {{ formattedInteger(group.credentials.length) }}
                  </strong>
                  <small>{{ t('monitor.inspector.groups.availableTotal') }}</small>
                </div>
                <div class="route-candidate__measure" role="cell">
                  <span class="route-cell-label">{{
                    t('monitor.inspector.groups.columns.weight')
                  }}</span>
                  <strong>{{ formattedInteger(groupEffectiveWeight(group)) }}</strong>
                  <small>{{ t('monitor.inspector.groups.currentTotal') }}</small>
                </div>
                <div class="route-candidate__share" role="cell">
                  <span class="route-cell-label">{{
                    t('monitor.inspector.groups.columns.share')
                  }}</span>
                  <div
                    class="route-share-meter"
                    role="progressbar"
                    :aria-label="
                      t('monitor.inspector.groups.shareLabel', {
                        name: group.group_name,
                        share: groupShareLabel(group),
                      })
                    "
                    aria-valuemin="0"
                    aria-valuemax="100"
                    :aria-valuenow="groupShare(group)"
                  >
                    <i :style="{ width: `${groupShare(group)}%` }" />
                  </div>
                  <small>{{ groupShareLabel(group) }}</small>
                </div>
                <span class="route-candidate__disclosure" role="cell">
                  <ChevronRight :size="17" aria-hidden="true" />
                </span>
              </summary>

              <div class="route-credential-details">
                <header class="route-credential-details__header">
                  <div>
                    <strong>{{ t('monitor.inspector.credentials.title') }}</strong>
                    <span>{{ candidateCredentialSummary(group) }}</span>
                  </div>
                  <span>
                    {{
                      t('monitor.inspector.weights.groupManual', {
                        value: nullableWeight(group.weight_manual),
                      })
                    }}
                  </span>
                </header>

                <p v-if="group.credentials.length === 0" class="route-credential-details__empty">
                  {{ t('monitor.inspector.credentials.noneReturned') }}
                </p>

                <LedgerRecordList
                  v-else
                  :label="t('monitor.inspector.credentials.tableLabel', { name: group.group_name })"
                  :row-count="group.credentials.length + 1"
                  :scroll-hint="t('monitor.scrollHint')"
                  grid-class="route-credential-grid"
                >
                  <template #header>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.credential')
                    }}</span>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.status')
                    }}</span>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.manual')
                    }}</span>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.auto')
                    }}</span>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.effective')
                    }}</span>
                    <span role="columnheader">{{
                      t('monitor.inspector.credentials.columns.cooldown')
                    }}</span>
                  </template>

                  <article
                    v-for="(credential, credentialIndex) in orderedCredentials(group)"
                    :key="credential.credential_id"
                    class="ledger-record-list__record route-credential-record"
                    role="row"
                    :aria-rowindex="credentialIndex + 2"
                  >
                    <div
                      class="ledger-record-list__cell route-credential-record__identity"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.credential')
                      }}</span>
                      <code>#{{ credential.credential_id }}</code>
                    </div>
                    <div
                      class="ledger-record-list__cell route-credential-record__status"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.status')
                      }}</span>
                      <StatusBadge :tone="credentialTone(credential)" size="compact">
                        {{ credentialStatusLabel(credential) }}
                      </StatusBadge>
                      <code v-if="credential.reason_code">{{ credential.reason_code }}</code>
                    </div>
                    <div
                      class="ledger-record-list__cell route-credential-record__weight"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.manual')
                      }}</span>
                      <span>{{ nullableWeight(credential.weight_manual) }}</span>
                    </div>
                    <div
                      class="ledger-record-list__cell route-credential-record__weight"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.auto')
                      }}</span>
                      <span>{{ formattedInteger(credential.weight_auto) }}</span>
                    </div>
                    <div
                      class="ledger-record-list__cell route-credential-record__weight"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.effective')
                      }}</span>
                      <span>{{ formattedInteger(credential.effective_weight) }}</span>
                    </div>
                    <div
                      class="ledger-record-list__cell route-credential-record__cooldown"
                      role="cell"
                    >
                      <span class="route-credential-label">{{
                        t('monitor.inspector.credentials.columns.cooldown')
                      }}</span>
                      <AppDateTime
                        v-if="credential.cooldown_until_ms !== null"
                        :instant="credential.cooldown_until_ms"
                        :locale="locale"
                      />
                      <span v-else>{{ t('monitor.inspector.credentials.none') }}</span>
                    </div>
                  </article>
                </LedgerRecordList>

                <footer class="route-credential-details__footer">
                  <RouterLink
                    v-slot="{ navigate }"
                    :to="groupDetailLocation(group.group_id)"
                    custom
                  >
                    <AppButton role="link" variant="secondary" size="compact" @click="navigate">
                      {{ t('monitor.inspector.groups.viewGroup') }}
                      <ArrowRight :size="15" aria-hidden="true" />
                    </AppButton>
                  </RouterLink>
                </footer>
              </div>
            </details>
          </div>
        </section>

        <section class="route-section" aria-labelledby="route-exclusions-title">
          <MonitorSectionHeading
            id="route-exclusions-title"
            :title="t('monitor.inspector.excluded.title')"
            :description="t('monitor.inspector.excluded.description')"
            :meta="
              t('monitor.inspector.groups.count', {
                count: formattedInteger(excludedGroups.length),
              })
            "
          />

          <InlineFeedback v-if="excludedGroups.length === 0" tone="neutral" appearance="ledger">
            {{ t('monitor.inspector.excluded.empty') }}
          </InlineFeedback>

          <LedgerRecordList
            v-else
            :label="t('monitor.inspector.excluded.tableLabel')"
            :row-count="excludedGroups.length + 1"
            :scroll-hint="t('monitor.scrollHint')"
            grid-class="route-exclusion-grid"
          >
            <template #header>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.group') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.groups.columns.status') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.result.reason') }}</span>
              <span role="columnheader">{{ t('monitor.inspector.excluded.reasonCode') }}</span>
            </template>

            <article
              v-for="(group, index) in excludedGroups"
              :key="group.group_id"
              class="ledger-record-list__record route-exclusion-record"
              role="row"
              :aria-rowindex="index + 2"
            >
              <div class="ledger-record-list__cell route-exclusion-record__identity" role="cell">
                <span class="route-credential-label">{{
                  t('monitor.inspector.groups.columns.group')
                }}</span>
                <OverflowTooltip as="strong" :content="group.group_name">
                  {{ group.group_name }}
                </OverflowTooltip>
                <OverflowTooltip as="small" :content="excludedGroupIdentity(group)">
                  {{ excludedGroupIdentity(group) }}
                </OverflowTooltip>
              </div>
              <div class="ledger-record-list__cell" role="cell">
                <span class="route-credential-label">{{
                  t('monitor.inspector.groups.columns.status')
                }}</span>
                <StatusBadge tone="neutral" size="compact">
                  {{ t('monitor.inspector.groups.excluded') }}
                </StatusBadge>
              </div>
              <div class="ledger-record-list__cell route-exclusion-record__reason" role="cell">
                <span class="route-credential-label">{{
                  t('monitor.inspector.result.reason')
                }}</span>
                {{ reasonLabel(group.reason_code) }}
              </div>
              <div class="ledger-record-list__cell route-exclusion-record__code" role="cell">
                <span class="route-credential-label">{{
                  t('monitor.inspector.excluded.reasonCode')
                }}</span>
                <code>{{ group.reason_code ?? '—' }}</code>
              </div>
            </article>
          </LedgerRecordList>
        </section>
      </template>
    </div>
  </div>
</template>

<style scoped>
.inspector-tab {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(272px, 300px) minmax(0, 1fr);
  align-items: start;
  gap: var(--space-6);
}

.inspector-tab > :deep(.inspector-form-panel) {
  position: sticky;
  top: calc(var(--topbar-height) + var(--space-4));
}

.inspector-stack,
.route-section {
  display: grid;
  min-width: 0;
}

.inspector-stack {
  gap: var(--space-5);
}

.route-section {
  gap: var(--space-3);
}

.inspector-empty {
  min-height: 330px;
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-card);
}

.route-summary {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-left-width: 3px;
  border-radius: var(--radius-card);
  background: var(--color-surface);
}

.route-summary--success {
  border-left-color: var(--color-success);
}

.route-summary--danger {
  border-left-color: var(--color-danger);
}

.route-summary__header {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-5);
  padding: 18px 20px;
}

.route-summary__content {
  min-width: 0;
}

.route-summary__title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.route-summary h2 {
  margin: 0;
  font-size: 1.18rem;
  font-weight: 650;
  letter-spacing: -0.015em;
  line-height: var(--line-compact);
}

.route-summary__reason {
  margin: var(--space-2) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.route-summary__reason code {
  margin-left: var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.route-summary__meta {
  display: grid;
  flex: none;
  justify-items: end;
  gap: var(--space-1);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.route-facts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin: 0;
  border-top: 1px solid var(--color-border-subtle);
}

.route-fact {
  min-width: 0;
  min-height: 74px;
  border-right: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 12px 16px;
}

.route-fact:nth-child(3n) {
  border-right: 0;
}

.route-fact:nth-last-child(-n + 3) {
  border-bottom: 0;
}

.route-fact dt {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.route-fact dd {
  margin: 7px 0 0;
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--text-meta);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.route-fact__mono,
.route-fact__number {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.route-fact__number {
  font-size: 1rem !important;
}

.route-candidate-ledger {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-control);
}

.route-candidate-ledger__header,
.route-candidate__summary {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(170px, 1.35fr) 128px 96px 112px minmax(132px, 0.95fr) 34px;
  align-items: center;
  column-gap: var(--space-4);
}

.route-candidate-ledger__header {
  min-height: 38px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-weight: 500;
  letter-spacing: 0.04em;
}

.route-candidate {
  min-width: 0;
  border-top: 1px solid var(--color-border-subtle);
}

.route-candidate:first-of-type {
  border-top-color: var(--color-border-control);
}

.route-candidate__summary {
  min-height: 80px;
  padding: 12px 0;
  cursor: pointer;
  list-style: none;
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.route-candidate__summary::-webkit-details-marker {
  display: none;
}

.route-candidate__summary:hover {
  background: var(--color-surface-sunken);
}

.route-candidate__summary:focus-visible {
  border-radius: var(--radius-control);
  outline: 2px solid var(--color-action);
  outline-offset: -2px;
}

.route-candidate__identity,
.route-candidate__measure,
.route-candidate__share,
.route-candidate__status {
  min-width: 0;
}

.route-candidate__identity {
  display: grid;
  gap: var(--space-1);
}

.route-candidate__status {
  display: grid;
  justify-items: start;
  gap: var(--space-1);
}

.route-candidate__status small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.route-candidate__identity strong,
.route-exclusion-record__identity strong {
  overflow: hidden;
  font-weight: 620;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.route-candidate__identity small,
.route-exclusion-record__identity small,
.route-candidate__measure small,
.route-candidate__share small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.route-candidate__identity small,
.route-exclusion-record__identity small {
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.route-candidate__measure,
.route-candidate__share {
  display: grid;
  gap: var(--space-1);
}

.route-candidate__measure strong {
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}

.route-share-meter {
  width: 100%;
  height: 5px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border-subtle);
}

.route-share-meter i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--color-action);
}

.route-candidate__disclosure {
  display: grid;
  color: var(--color-text-faint);
  place-items: center;
}

.route-candidate__disclosure svg {
  transition: transform var(--duration-fast) var(--easing-standard);
}

.route-candidate[open] .route-candidate__disclosure svg {
  transform: rotate(90deg);
}

.route-cell-label,
.route-credential-label {
  display: none;
}

.route-credential-details {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-surface-sunken) 62%, var(--color-surface));
  padding: 14px 16px 16px;
}

.route-credential-details__header,
.route-credential-details__header > div,
.route-credential-details__footer {
  display: flex;
  min-width: 0;
  align-items: center;
}

.route-credential-details__header {
  justify-content: space-between;
  gap: var(--space-4);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.route-credential-details__header > div {
  flex-wrap: wrap;
  gap: var(--space-2);
}

.route-credential-details__header strong {
  color: var(--color-text);
  font-weight: 620;
}

.route-credential-details__empty {
  margin: 0;
  color: var(--color-text-muted);
  padding: var(--space-3) 0;
  font-size: var(--text-sm);
}

.route-credential-details__footer {
  justify-content: flex-end;
}

.route-credential-grid {
  --ledger-record-list-grid: 88px minmax(170px, 1.4fr) 92px 92px 108px minmax(148px, 1fr);
  --ledger-record-list-column-gap: 14px;
}

.route-credential-record {
  --ledger-record-list-record-min-height: 58px;
  --ledger-record-list-record-padding: 9px 0;
}

.route-credential-record__identity code,
.route-credential-record__weight,
.route-credential-record__cooldown,
.route-exclusion-record__code {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}

.route-credential-record__status {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.route-credential-record__status code {
  color: var(--color-text-faint);
  font-size: 10px;
}

.route-exclusion-grid {
  --ledger-record-list-grid: minmax(190px, 1.2fr) 120px minmax(220px, 1.45fr) minmax(160px, 0.9fr);
}

.route-exclusion-record {
  --ledger-record-list-record-min-height: 72px;
  --ledger-record-list-record-padding: 11px 0;
}

.route-exclusion-record__identity {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}

.route-exclusion-record__reason {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.route-exclusion-record__code code {
  color: var(--color-text-faint);
}

@media (max-width: 1180px) {
  .route-candidate-ledger__header,
  .route-candidate__summary {
    grid-template-columns: minmax(160px, 1.3fr) 120px 88px 104px minmax(120px, 0.9fr) 30px;
    column-gap: var(--space-3);
  }
}

@media (max-width: 1120px) {
  .inspector-tab {
    grid-template-columns: minmax(0, 1fr);
  }

  .inspector-tab > :deep(.inspector-form-panel) {
    position: static;
  }
}

@media (max-width: 860px) {
  .route-summary__header {
    flex-direction: column;
    gap: var(--space-3);
  }

  .route-summary__meta {
    justify-items: start;
  }

  .route-facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .route-fact:nth-child(3n) {
    border-right: 1px solid var(--color-border-subtle);
  }

  .route-fact:nth-child(2n) {
    border-right: 0;
  }

  .route-fact:nth-last-child(-n + 3) {
    border-bottom: 1px solid var(--color-border-subtle);
  }

  .route-fact:nth-last-child(-n + 2) {
    border-bottom: 0;
  }

  .route-candidate-ledger {
    display: grid;
    gap: 10px;
    border: 0;
  }

  .route-candidate-ledger__header {
    display: none;
  }

  .route-candidate {
    overflow: hidden;
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    background: var(--color-surface);
  }

  .route-candidate:first-of-type {
    border-top-color: var(--color-border-subtle);
  }

  .route-candidate__summary {
    grid-template-columns: minmax(0, 1.5fr) minmax(112px, 0.7fr) 28px;
    gap: 12px 16px;
    padding: 15px;
  }

  .route-candidate__identity {
    grid-column: 1;
  }

  .route-candidate__status {
    grid-column: 2;
  }

  .route-candidate__disclosure {
    grid-column: 3;
    grid-row: 1 / span 2;
  }

  .route-candidate__measure,
  .route-candidate__share {
    align-self: end;
  }

  .route-candidate__measure:nth-of-type(3) {
    grid-column: 1;
  }

  .route-candidate__measure:nth-of-type(4) {
    grid-column: 2;
  }

  .route-candidate__share {
    grid-column: 1 / span 2;
  }

  .route-cell-label,
  .route-credential-label {
    display: block;
    color: var(--color-text-faint);
    font-family: var(--font-sans);
    font-size: var(--text-label-xs);
  }

  .route-credential-record__identity,
  .route-credential-record__weight,
  .route-credential-record__cooldown,
  .route-exclusion-record > .ledger-record-list__cell {
    display: grid;
    gap: var(--space-1);
  }

  .route-credential-record__identity,
  .route-credential-record__status,
  .route-exclusion-record__identity,
  .route-exclusion-record__reason {
    grid-column: 1 / -1;
  }

  .route-credential-record__status {
    align-items: flex-start;
  }

  .route-exclusion-record {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .route-summary__header {
    padding: 16px 14px;
  }

  .route-facts {
    grid-template-columns: minmax(0, 1fr);
  }

  .route-fact,
  .route-fact:nth-child(2n),
  .route-fact:nth-child(3n),
  .route-fact:nth-last-child(-n + 2),
  .route-fact:nth-last-child(-n + 3) {
    border-right: 0;
    border-bottom: 1px solid var(--color-border-subtle);
  }

  .route-fact:last-child {
    border-bottom: 0;
  }

  .route-candidate__summary {
    grid-template-columns: minmax(0, 1fr) 28px;
  }

  .route-candidate__identity,
  .route-candidate__status,
  .route-candidate__measure:nth-of-type(3),
  .route-candidate__measure:nth-of-type(4),
  .route-candidate__share {
    grid-column: 1;
  }

  .route-candidate__disclosure {
    grid-column: 2;
    grid-row: 1 / span 5;
  }

  .route-credential-details {
    padding-inline: 13px;
  }

  .route-credential-details__header {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-2);
  }

  .route-exclusion-record {
    grid-template-columns: minmax(0, 1fr);
  }

  .route-exclusion-record > .ledger-record-list__cell {
    grid-column: 1;
  }
}
</style>
