<script setup lang="ts">
import { numberFormatter, dateFormatter } from '@modern/components/ui/intl-formatters'
import { ChevronDown, ChevronUp, KeyRound, UserRound } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, useId } from 'vue'
import { useURLState } from '@modern/app/url-state'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { getGroupModelNames, type GroupRow, type GroupUsage } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCopyValue,
  AppIcon,
  AppInlineNumber,
  AppOverflowText,
  AppSegmentedBar,
  AppTooltip,
  AppSwitch,
  AppTextField,
} from '@modern/components/ui'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import type { SemanticTone } from '@modern/components/ui/types'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{
  group: GroupRow
  expanded: boolean
  pending?: 'toggle' | 'weight'
  enabledOverride?: boolean
  usage?: GroupUsage
  usageLoading: boolean
  usageIncomplete: boolean
  weightError?: string
}>()
const emit = defineEmits<{
  expand: []
  toggle: [value: boolean]
  weight: [value: number]
  weightEditing: [value: boolean]
  weightDirty: [value: boolean]
  clearWeightError: []
}>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const route = useRoute()
const id = useId()
const searchKey = 'models_' + props.group.id
const allKey = 'all_models_' + props.group.id
const modelView = useURLState(
  [searchKey, allKey],
  (query) => ({
    search: typeof query[searchKey] === 'string' ? (query[searchKey] as string) : '',
    all: query[allKey] === '1',
  }),
  (value) => ({
    ...(value.search ? { [searchKey]: value.search } : {}),
    ...(value.all ? { [allKey]: '1' } : {}),
  }),
)
const modelSearch = computed({
  get: () => modelView.value.search,
  set: (search: string) => {
    modelView.value = { ...modelView.value, search }
  },
})
const allModels = computed({
  get: () => modelView.value.all,
  set: (all: boolean) => {
    modelView.value = { ...modelView.value, all }
  },
})
const models = useQuery(
  computed(() => ({
    queryKey: ['modern', 'group-model-names', props.group.id],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getGroupModelNames(client, props.group.id, signal),
    enabled: props.expanded,
  })),
)
const matchedModels = computed(() =>
  (models.data.value ?? []).filter((model) =>
    model.name.toLocaleLowerCase().includes(modelSearch.value.trim().toLocaleLowerCase()),
  ),
)
const visibleModels = computed(() =>
  allModels.value || modelSearch.value ? matchedModels.value : matchedModels.value.slice(0, 30),
)
const state = computed(() =>
  props.enabledOverride !== undefined ? 'syncing' : props.group.availability,
)
const tone = computed<SemanticTone>(() =>
  state.value === 'ready'
    ? 'success'
    : state.value === 'limited'
      ? 'warning'
      : state.value === 'paused' || state.value === 'syncing'
        ? 'neutral'
        : 'danger',
)
const credentialSegments = computed(() =>
  (
    [
      ['available', 'success'],
      ['cooldown', 'warning'],
      ['blacklisted', 'danger'],
      ['disabled', 'neutral'],
    ] as const
  ).map(([key, tone]) => ({ key, tone, value: props.group.credentials[key] })),
)
const credentialSummary = computed(() =>
  [
    ...credentialSegments.value.map(
      (segment) => t('groups.credentials.' + segment.key) + ' ' + n(segment.value),
    ),
    ...(props.group.credentials.modelCooldown
      ? [t('groups.modelCooldown', { count: n(props.group.credentials.modelCooldown) })]
      : []),
  ].join(' · '),
)
function numberDetail(value?: number): string | undefined {
  return value !== undefined && formatCompactNumber(value, locale.value) !== n(value)
    ? n(value)
    : undefined
}
const host = computed(() => {
  try {
    return new URL(props.group.endpoint).host
  } catch {
    return props.group.endpoint
  }
})
const unknownCount = computed(
  () => !props.usage || (props.usageIncomplete && props.usage.requests === 0),
)
const partial = computed(() => props.usageIncomplete || props.usage?.incomplete)
const successRate = computed(() =>
  !props.usage?.requests || unknownCount.value
    ? '—'
    : numberFormatter(locale.value, { style: 'percent', maximumFractionDigits: 1 }).format(
        props.usage.successes / props.usage.requests,
      ),
)
const lastActive = computed(() =>
  props.group.lastActiveHour === null
    ? t('groups.board.neverActive')
    : dateFormatter(locale.value, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        hour12: false,
      }).format(props.group.lastActiveHour),
)
</script>

<template>
  <article class="modern-group-entry" :aria-labelledby="`${id}-name`">
    <div class="modern-group-row">
      <div class="modern-group-identity">
        <AppChannelIcon
          class="modern-group-avatar"
          :icon="group.channelIcon"
          :mark="group.channelMark"
          :name="group.channelName"
        />
        <div class="modern-group-heading">
          <div class="modern-group-name-line">
            <h2 :id="`${id}-name`">
              <AppButton variant="text" :disabled="Boolean(pending)" as-child>
                <RouterLink
                  :to="{
                    name: 'modern-group-detail',
                    params: { id: group.id },
                    query: { from: route.fullPath },
                  }"
                  ><AppOverflowText :text="group.name"
                /></RouterLink>
              </AppButton>
            </h2>
            <AppBadge :tone="tone" variant="plain" size="xs" dot>{{
              t(`groups.row.state.${state}`)
            }}</AppBadge>
          </div>
          <div class="modern-group-secondary modern-group-connection">
            <AppBadge
              :icon="group.connectionType === 'subscription' ? UserRound : KeyRound"
              :tone="group.connectionType === 'subscription' ? 'brand' : 'neutral'"
              >{{ t(`groups.connection.${group.connectionType}`) }}</AppBadge
            >
            <AppCopyValue
              v-if="group.endpoint"
              :value="group.endpoint"
              :display="host"
              :label="t('groups.copyURL')"
            />
            <span v-else>{{ t('groups.defaultEndpoint') }}</span>
          </div>
        </div>
      </div>
      <div class="modern-group-metric" role="group" :aria-label="t('groups.board.credentials')">
        <span class="modern-group-mobile-label">{{ t('groups.board.credentials') }}</span>
        <strong>{{ n(group.credentials.total) }}</strong>
        <div class="modern-group-bar-line">
          <AppSegmentedBar :segments="credentialSegments" :label="credentialSummary" />
        </div>
      </div>
      <div class="modern-group-metric">
        <span class="modern-group-mobile-label">{{ t('groups.columns.models') }}</span>
        <AppButton
          variant="text"
          :aria-label="t('groups.board.modelDirectory') + ': ' + n(group.modelCount)"
          :aria-expanded="expanded"
          :aria-controls="`${id}-models`"
          @click="emit('expand')"
        >
          <strong>{{ n(group.modelCount) }}</strong
          ><AppIcon
            :icon="expanded ? ChevronUp : ChevronDown"
            size="xs"
            :label="t(expanded ? 'groups.board.collapse' : 'groups.board.expand')"
          />
        </AppButton>
        <AppOverflowText
          class="modern-group-secondary"
          :text="t('groups.board.price', { value: group.priceMultiplier })"
        />
      </div>
      <div
        class="modern-group-metric"
        role="group"
        :aria-label="t('groups.row.requests24h')"
        :aria-busy="usageLoading || undefined"
      >
        <span class="modern-group-mobile-label">{{ t('groups.row.requests24h') }}</span>
        <AppTooltip :label="usage && !unknownCount ? numberDetail(usage.requests) : undefined">
          <strong :class="{ 'is-loading': usageLoading && !usage }">{{
            unknownCount ? '—' : formatCompactNumber(usage!.requests, locale)
          }}</strong>
        </AppTooltip>
        <span class="modern-group-secondary">{{
          t('groups.row.successRate', { value: successRate })
        }}</span>
      </div>
      <div
        class="modern-group-metric"
        role="group"
        :aria-label="t('groups.row.usage24h')"
        :aria-busy="usageLoading || undefined"
      >
        <span class="modern-group-mobile-label">{{ t('groups.row.usage24h') }}</span>
        <AppTooltip
          :label="usage && !(partial && !usage.tokens) ? numberDetail(usage.tokens) : undefined"
        >
          <strong :class="{ 'is-loading': usageLoading && !usage }"
            >{{
              !usage || (partial && !usage.tokens)
                ? '—'
                : formatCompactNumber(usage.tokens, locale)
            }}<span class="modern-group-unit"> Tokens</span></strong
          >
        </AppTooltip>
        <span class="modern-group-secondary modern-group-cost">
          {{
            !usage || (partial && usage.costNanoUSD === '0')
              ? '—'
              : formatNanoUSD(usage.costNanoUSD, locale)
          }}
        </span>
      </div>
      <div class="modern-group-actions">
        <AppSwitch
          :model-value="enabledOverride ?? group.enabled"
          :label="t('groups.toggle', { name: group.name })"
          :loading="pending === 'toggle'"
          :disabled="Boolean(pending)"
          @update:model-value="emit('toggle', $event)"
        />
        <AppInlineNumber
          :model-value="group.weight"
          :label="t('groups.edit.weight')"
          :min="1"
          :max="100"
          :pending="pending === 'weight'"
          :disabled="Boolean(pending)"
          :error="weightError"
          @submit="emit('weight', $event)"
          @editing="emit('weightEditing', $event)"
          @dirty="emit('weightDirty', $event)"
          @clear-error="emit('clearWeightError')"
        />
      </div>
    </div>
    <div v-if="expanded" :id="`${id}-models`" class="modern-group-expanded">
      <div class="modern-group-details">
        <span>{{ group.channelName }} · {{ t('groups.board.activity') }} {{ lastActive }}</span>
        <AppTextField
          v-if="models.data.value?.length"
          v-model="modelSearch"
          :label="t('groups.row.searchModels')"
          label-hidden
          :placeholder="t('groups.row.searchModels')"
          size="sm"
        />
      </div>
      <p v-if="models.isPending.value" role="status">{{ t('collection.loading') }}</p>
      <div v-else-if="models.isError.value" class="modern-group-model-error" role="alert">
        {{ t('groups.board.modelsFailed')
        }}<AppButton size="xs" @click="models.refetch()">{{ t('collection.retry') }}</AppButton>
      </div>
      <div v-else class="modern-group-model-directory">
        <AppCopyValue v-for="model in visibleModels" :key="model.id" :value="model.name" />
        <span v-if="!visibleModels.length">{{ t('ui.select.empty') }}</span>
        <AppButton
          v-if="!allModels && !modelSearch && matchedModels.length > 30"
          variant="text"
          @click="allModels = true"
          >{{ t('groups.board.allModels', { count: n(matchedModels.length) }) }}</AppButton
        >
      </div>
    </div>
  </article>
</template>

<style scoped>
.modern-group-entry {
  min-width: var(--modern-group-list-width);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-row {
  text-align: left;
  display: grid;
  grid-template-columns: var(--modern-group-columns);
  align-items: center;
  gap: var(--modern-space-4);
  padding: var(--modern-space-4) var(--modern-space-3);
}
.modern-group-row:hover {
  background: var(--modern-subtle);
}
.modern-group-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-group-avatar {
  font-size: var(--modern-channel-avatar);
}
.modern-group-heading {
  min-width: 0;
  flex: 1;
}
.modern-group-name-line {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-group-name-line h2 {
  min-width: 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-compact);
}
.modern-group-name-line h2 > * {
  max-width: 100%;
}
.modern-group-name-line h2 span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-group-secondary {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-compact);
}
.modern-group-connection {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-group-connection > span:first-child {
  flex: none;
  white-space: nowrap;
}
.modern-group-metric {
  display: grid;
  grid-template-rows: var(--modern-control-xs) var(--modern-badge-xs);
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-1);
  font-variant-numeric: tabular-nums;
}
.modern-group-metric strong {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-unit {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-group-cost {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-group-actions {
  display: grid;
  grid-template-columns: var(--modern-group-action-columns);
  align-items: center;
  justify-items: start;
  gap: var(--modern-space-4);
  text-align: left;
}
.modern-group-bar-line {
  display: flex;
  min-height: var(--modern-badge-xs);
  align-items: center;
}

.modern-group-mobile-label {
  display: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-expanded {
  display: grid;
  gap: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-subtle);
  padding: var(--modern-space-4) var(--modern-space-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-group-details {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-4);
}
.modern-group-details > :first-child {
  margin-right: auto;
}
.modern-group-model-directory {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-4);
  font-family: var(--modern-font-mono);
}
.modern-group-model-error {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  color: var(--modern-danger);
}
.is-loading {
  color: var(--modern-muted);
}
@media (max-width: 760px) {
  .modern-group-entry {
    min-width: 0;
  }
  .modern-group-row {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--modern-space-4);
    padding: var(--modern-space-4) var(--modern-space-1);
  }
  .modern-group-identity {
    grid-column: 1 / -1;
  }
  .modern-group-mobile-label {
    display: inline;
  }
  .modern-group-metric {
    grid-template-rows: auto var(--modern-control-xs) var(--modern-badge-xs);
  }
  .modern-group-actions {
    grid-column: 1 / -1;
    justify-content: start;
  }
  .modern-group-details > :first-child {
    margin-right: 0;
  }
}
</style>
