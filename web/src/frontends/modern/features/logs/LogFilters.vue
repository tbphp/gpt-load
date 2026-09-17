<script setup lang="ts">
import { SlidersHorizontal } from '@lucide/vue'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogAccessKeyOption, LogFilterName, LogQuery } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import { getGroupCredentials } from '@modern/api/group-detail'
import { channelSearchOption } from '@modern/components/channel-options'
import {
  AppAdvancedFilters,
  AppAdvancedFilterSection,
  AppProtocolTag,
  AppOverflowText,
  AppChannelIcon,
  AppDateTimeRangePicker,
  AppModelSelect,
  AppIconButton,
  AppSearchSelect,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import type { SearchSelectOption } from '@modern/components/ui'
import {
  formatLocalDateTime,
  parseLocalDateTime,
  type DateRangePreset,
} from '@modern/components/ui/date-time'
import { protocolLabel } from '@modern/i18n/protocols'
import { useApiClient } from '@shared/http/client-context'
import {
  advancedLogFilters,
  logFilterErrors,
  nanoToUSD,
  usdToNano,
  type LogFilterDefinition,
} from './log-filters'

const props = defineProps<{
  filters: LogQuery
  more: boolean
  admin: boolean
  groups?: readonly GroupRow[]
  channels?: readonly GroupChannel[]
  accessKeys?: readonly LogAccessKeyOption[]
  models: readonly string[]
  groupsLoading: boolean
  keysLoading: boolean
  preset?: DateRangePreset
}>()
const emit = defineEmits<{
  change: [filters: LogQuery, preset: DateRangePreset | undefined]
  more: [value: boolean]
}>()
const { t, te } = useI18n()
const client = useApiClient()
const draft = ref<LogQuery>({})
const from = ref('')
const to = ref('')
const draftPreset = ref<DateRangePreset>()
const composing = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | undefined
let edited = false
const credentialLabels = ref(new Map<string, string>())
watch(
  () => props.filters,
  (filters) => {
    clearTimeout(searchTimer)
    edited = false
    draft.value = {
      ...filters,
      cost_min_nano_usd: nanoToUSD(filters.cost_min_nano_usd),
      cost_max_nano_usd: nanoToUSD(filters.cost_max_nano_usd),
    }
    from.value = formatLocalDateTime(Number(filters.from_ms))
    to.value = formatLocalDateTime(Number(filters.to_ms))
    draftPreset.value = props.preset
  },
  { immediate: true, deep: true },
)
const request = computed(() => {
  const result = Object.fromEntries(
    Object.entries(draft.value)
      .map(([key, value]) => [key, value?.trim()])
      .filter(([, value]) => value),
  ) as LogQuery
  for (const key of ['cost_min_nano_usd', 'cost_max_nano_usd'] as const)
    if (draft.value[key]?.trim()) result[key] = usdToNano(draft.value[key]!)
  return result
})
const errors = computed(() => {
  const result = logFilterErrors(request.value)
  for (const key of ['cost_min_nano_usd', 'cost_max_nano_usd'] as const)
    if (draft.value[key]?.trim() && usdToNano(draft.value[key]!) === undefined)
      result[key] = 'invalidAmount'
  return result
})
function apply(): void {
  clearTimeout(searchTimer)
  if (composing.value || Object.keys(errors.value).length) return
  edited = false
  emit('change', request.value, draftPreset.value)
}
function scheduleApply(): void {
  clearTimeout(searchTimer)
  if (!composing.value) searchTimer = setTimeout(apply, 200)
}
function startComposition(): void {
  composing.value = true
  clearTimeout(searchTimer)
}
function endComposition(): void {
  composing.value = false
  if (edited) scheduleApply()
}
function keyApply(event: KeyboardEvent): void {
  if (event.target instanceof HTMLInputElement && !event.defaultPrevented && !event.isComposing)
    apply()
}
function update(key: LogFilterName, value: string, immediate = true): void {
  if ((draft.value[key] ?? '') === value) return
  draft.value = { ...draft.value, [key]: value }
  if (key === 'group_id') draft.value.credential_id = ''
  edited = true
  if (immediate) apply()
  else scheduleApply()
}
function dateApply(): void {
  const start = parseLocalDateTime(from.value)
  const end = parseLocalDateTime(to.value)
  if (!start || !end) return
  draft.value = { ...draft.value, from_ms: String(start.getTime()), to_ms: String(end.getTime()) }
  apply()
}
onScopeDispose(() => clearTimeout(searchTimer))
function rangeLabel(field: LogFilterDefinition): string {
  // 下限字段代表整段范围；重试次数没有对应列，单独取筛选文案。
  if (field.key === 'retry_count_min') return t('logs.filters.retry_count')
  const column = field.key.startsWith('cost_')
    ? 'estimated_cost_nano_usd'
    : field.key.replace('_min', '')
  return t('logs.columns.' + column)
}
function rangeUnit(field: LogFilterDefinition): string {
  return field.key.endsWith('_ms') ? ' (ms)' : field.kind === 'money' ? ' ($)' : ''
}
function upperKey(key: LogFilterName): LogFilterName {
  return key.replace('_min', '_max') as LogFilterName
}
function valueLabel(value: string): string {
  return value === 'true' || value === 'false'
    ? t(value === 'true' ? 'logs.yes' : 'logs.no')
    : te('logs.values.' + value)
      ? t('logs.values.' + value)
      : value
}
function options(field: LogFilterDefinition) {
  return [
    { value: '', label: t('logs.all') },
    ...(field.values ?? []).map((value) => ({
      value,
      label: field.key === 'protocol' ? protocolLabel(value, t) : valueLabel(value),
    })),
  ]
}
function withCurrent(
  options: readonly SearchSelectOption[],
  selected: string | undefined,
  fallback: string,
  allLabel: string,
) {
  return [
    { value: '', label: allLabel },
    ...options,
    ...(selected && !options.some((option) => option.value === selected)
      ? [{ value: selected, label: fallback }]
      : []),
  ]
}
const groupMap = computed(() => new Map(props.groups?.map((row) => [String(row.id), row])))
const channelMap = computed(() => new Map(props.channels?.map((row) => [row.id, row])))
const groupOptions = computed(() =>
  withCurrent(
    (props.groups ?? []).map((row) => ({
      value: String(row.id),
      label: row.name,
      keywords: [row.channelName, row.channelID],
    })),
    draft.value.group_id,
    props.groups ? t('logs.deleted') : '—',
    t('logs.allGroups'),
  ),
)
const keyOptions = computed(() =>
  withCurrent(
    (props.accessKeys ?? []).map((row) => ({
      value: String(row.id),
      label: row.name,
      keywords: [row.suffix],
    })),
    draft.value.access_key_id,
    props.accessKeys ? t('logs.deleted') : '—',
    t('logs.allAccessKeys'),
  ),
)
const channelOptions = computed(() =>
  withCurrent(
    (props.channels ?? []).map(channelSearchOption),
    draft.value.channel_id,
    props.channels ? t('logs.deleted') : '—',
    t('logs.allChannels'),
  ),
)
const credentialOption = computed(() =>
  draft.value.credential_id
    ? {
        value: draft.value.credential_id,
        label:
          credentialLabels.value.get(draft.value.credential_id) ?? t('logs.selectedCredential'),
      }
    : undefined,
)
const sections = computed(() =>
  (['request', 'routing', 'result', 'metrics'] as const)
    .map((id) => ({
      id,
      fields: advancedLogFilters.filter(
        (field) => field.section === id && (props.admin || !field.admin),
      ),
    }))
    .filter((section) => section.fields.length || (section.id === 'routing' && props.admin)),
)
const loadCredentials = computed(() => {
  const groupID = Number(draft.value.group_id)
  return async (q: string, signal: AbortSignal) => {
    if (!groupID) return [{ value: '', label: t('logs.all') }]
    const page = await getGroupCredentials(
      client,
      groupID,
      { q, page: 1, pageSize: 100, sort: 'name', status: '', proxy: '', reset: '' },
      signal,
    )
    const values = page.items.map((row) => ({
      value: String(row.id),
      label: row.account || row.mask,
    }))
    credentialLabels.value = new Map([
      ...credentialLabels.value,
      ...values.map((row): [string, string] => [row.value, row.label]),
    ])
    return [{ value: '', label: t('logs.all') }, ...values]
  }
})
</script>

<template>
  <div
    class="modern-log-filter-area"
    role="search"
    @keydown.enter="keyApply"
    @compositionstart="startComposition"
    @compositionend="endComposition"
  >
    <div class="modern-log-primary-filters">
      <AppDateTimeRangePicker
        v-model:from="from"
        v-model:to="to"
        class="modern-log-filter-date"
        :label="t('logs.timeRange')"
        label-hidden
        size="md"
        :preset="draftPreset"
        @update:preset="draftPreset = $event"
        @apply="dateApply"
      />
      <AppSearchSelect
        v-if="admin"
        class="modern-log-filter-choice"
        :model-value="draft.group_id ?? ''"
        :options="groupOptions"
        :label="t('logs.filters.group_id')"
        :placeholder="t('logs.filters.group_id')"
        label-hidden
        :loading="groupsLoading"
        @update:model-value="update('group_id', $event)"
        ><template #option="{ option }"
          ><AppChannelIcon
            v-if="groupMap.get(option.value)"
            :icon="groupMap.get(option.value)!.channelIcon"
            :name="groupMap.get(option.value)!.channelName"
            :mark="groupMap.get(option.value)!.channelMark"
            size="sm"
          /><span>{{ option.label }}</span></template
        ></AppSearchSelect
      >
      <AppSearchSelect
        v-if="admin"
        class="modern-log-filter-choice"
        :model-value="draft.channel_id ?? ''"
        :options="channelOptions"
        :label="t('logs.filters.channel_id')"
        :placeholder="t('logs.filters.channel_id')"
        label-hidden
        @update:model-value="update('channel_id', $event)"
        ><template #option="{ option }"
          ><AppChannelIcon
            v-if="channelMap.get(option.value)"
            :icon="channelMap.get(option.value)!.icon"
            :name="channelMap.get(option.value)!.name"
            :mark="channelMap.get(option.value)!.mark"
            size="sm"
          /><span>{{ option.label }}</span></template
        ></AppSearchSelect
      >
      <AppSearchSelect
        v-if="admin"
        class="modern-log-filter-choice"
        :model-value="draft.access_key_id ?? ''"
        :options="keyOptions"
        :label="t('logs.filters.access_key_id')"
        :placeholder="t('logs.filters.access_key_id')"
        label-hidden
        :loading="keysLoading"
        @update:model-value="update('access_key_id', $event)"
      />
      <AppModelSelect
        class="modern-log-filter-model"
        :model-value="draft.client_model ?? ''"
        :models="models"
        :label="t('logs.filters.client_model')"
        :error="errors.client_model ? t('logs.errors.' + errors.client_model) : undefined"
        label-hidden
        fuzzy
        @update:model-value="update('client_model', $event, false)"
      />
      <div class="modern-log-filter-actions">
        <AppIconButton
          :icon="SlidersHorizontal"
          :label="t('logs.moreFilters')"
          :tooltip="true"
          :aria-expanded="more"
          aria-controls="modern-log-more-filters"
          @click="emit('more', !more)"
        />
        <slot name="actions" />
      </div>
    </div>
    <AppAdvancedFilters id="modern-log-more-filters" :open="more">
      <AppAdvancedFilterSection
        v-for="section in sections"
        :key="section.id"
        :title="t('logs.filterSections.' + section.id)"
      >
        <template v-if="section.id === 'routing' && admin">
          <AppSearchSelect
            :key="draft.group_id || 'all'"
            :model-value="draft.credential_id ?? ''"
            :options="[]"
            :load-options="loadCredentials"
            :selected-option="credentialOption"
            :label="t('logs.filters.credential_id')"
            :placeholder="t(draft.group_id ? 'logs.searchCredential' : 'logs.selectGroupFirst')"
            size="xs"
            :disabled="!draft.group_id"
            @update:model-value="update('credential_id', $event)"
          />
        </template>
        <!-- 任意分段内的下限字段都渲染成一行范围，上限并入其中。 -->
        <template v-for="field in section.fields" :key="field.key">
          <div v-if="field.key.includes('_min')" class="modern-log-filter-range">
            <span>{{ rangeLabel(field) }}{{ rangeUnit(field) }}</span>
            <div>
              <AppTextField
                :model-value="draft[field.key] ?? ''"
                :label="t('logs.filters.' + field.key)"
                label-hidden
                :placeholder="t('logs.minimum')"
                :inputmode="field.kind === 'money' ? 'decimal' : 'numeric'"
                :error="errors[field.key] ? t('logs.errors.' + errors[field.key]) : undefined"
                size="xs"
                @update:model-value="update(field.key, $event, false)"
              />
              <span aria-hidden="true">–</span>
              <AppTextField
                :model-value="draft[upperKey(field.key)] ?? ''"
                :label="t('logs.filters.' + upperKey(field.key))"
                label-hidden
                :placeholder="t('logs.maximum')"
                :inputmode="field.kind === 'money' ? 'decimal' : 'numeric'"
                :error="
                  errors[upperKey(field.key)]
                    ? t('logs.errors.' + errors[upperKey(field.key)])
                    : undefined
                "
                size="xs"
                @update:model-value="update(upperKey(field.key), $event, false)"
              />
            </div>
          </div>
          <AppSelect
            v-else-if="field.kind === 'select'"
            :model-value="draft[field.key] ?? ''"
            :options="options(field)"
            :label="t('logs.filters.' + field.key)"
            size="xs"
            @update:model-value="update(field.key, $event)"
          >
            <template #value="{ value, label }"
              ><AppProtocolTag
                v-if="field.key === 'protocol' && value"
                :protocol="value" /><AppOverflowText v-else :text="label"
            /></template>
            <template #option="{ option }"
              ><AppProtocolTag
                v-if="field.key === 'protocol' && option.value"
                :protocol="option.value"
              /><span v-else>{{ option.label }}</span></template
            >
          </AppSelect>
          <AppTextField
            v-else-if="!field.key.includes('_max')"
            :model-value="draft[field.key] ?? ''"
            :label="t('logs.filters.' + field.key)"
            :placeholder="t('logs.any')"
            :inputmode="
              field.kind === 'money' ? 'decimal' : field.kind === 'number' ? 'numeric' : undefined
            "
            :error="errors[field.key] ? t('logs.errors.' + errors[field.key]) : undefined"
            size="xs"
            autocomplete="off"
            @update:model-value="update(field.key, $event, false)"
          />
        </template>
      </AppAdvancedFilterSection>
    </AppAdvancedFilters>
  </div>
</template>

<style scoped>
.modern-log-filter-area {
  flex: none;
}
.modern-log-primary-filters {
  display: flex;
  flex: none;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-log-filter-date {
  flex: 2 1 240px;
  min-width: 0;
}
.modern-log-filter-model {
  flex: 1.3 1 200px;
  min-width: 0;
}
.modern-log-filter-choice {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-log-filter-choice + .modern-log-filter-choice {
  flex-basis: 144px;
}
.modern-log-filter-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  max-width: 100%;
  gap: var(--modern-space-2);
  flex: none;
  margin-left: auto;
}
.modern-log-filter-range {
  display: grid;
  grid-template-columns: 58px minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-log-filter-range > span {
  overflow: hidden;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-log-filter-range > div {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
}
</style>
