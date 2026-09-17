<script setup lang="ts">
import { Info, RotateCcw } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import {
  concurrencyQueryKey,
  concurrencyQueryOptions,
  type ConcurrencyScope,
  type ConcurrencyView,
} from '@modern/api/concurrency'
import {
  AppButton,
  AppIconButton,
  AppNotice,
  AppSelect,
  AppSegmentedField,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'

const props = withDefaults(
  defineProps<{
    scope: ConcurrencyScope
    id?: number
    editable?: boolean
    disabled?: boolean
    showLabel?: boolean
    showHelp?: boolean
    segmented?: boolean
    stacked?: boolean
  }>(),
  {
    id: 0,
    editable: false,
    disabled: false,
    showLabel: true,
    showHelp: true,
    segmented: false,
    stacked: false,
  },
)
const emit = defineEmits<{
  'state-change': [state: { dirty: boolean; valid: boolean }]
}>()
const { t, n } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const query = useQuery(computed(() => concurrencyQueryOptions(client, props.scope, props.id)))
const baseline = ref<number | null>()
const mode = ref('inherit')
const value = ref('1')
const valid = computed(
  () =>
    mode.value !== 'custom' ||
    (/^\d+$/u.test(value.value) && Number(value.value) >= 1 && Number(value.value) <= 1_000_000),
)
const maximum = computed(() =>
  mode.value === 'inherit'
    ? null
    : mode.value === 'unlimited'
      ? 0
      : valid.value
        ? Number(value.value)
        : undefined,
)
const dirty = computed(
  () => baseline.value !== undefined && (!valid.value || maximum.value !== baseline.value),
)
const locked = computed(() => props.disabled || baseline.value === undefined)
const label = computed(() => t(`concurrency.scopes.${props.scope}`))
const help = computed(() =>
  [
    label.value,
    ...(props.scope === 'global' || props.scope.startsWith('default_')
      ? [t('concurrency.default')]
      : []),
    t('concurrency.deferredHelp'),
    ...(query.data.value?.shared ? [t('concurrency.shared')] : []),
  ].join('\n'),
)
const options = computed(() => [
  {
    value: 'inherit',
    label: t(
      props.scope === 'global' || props.scope.startsWith('default_')
        ? props.segmented
          ? 'concurrency.defaultShort'
          : 'concurrency.default'
        : props.segmented
          ? 'concurrency.inheritShort'
          : 'concurrency.inherit',
    ),
  },
  { value: 'unlimited', label: t('concurrency.unlimited') },
  { value: 'custom', label: t('concurrency.custom') },
])
const summary = computed(() => {
  const data = query.data.value
  if (!data) return '—'
  if (props.scope.startsWith('default_'))
    return data.effective_max_concurrency === 0
      ? t('concurrency.unlimited')
      : n(data.effective_max_concurrency)
  return props.scope === 'upstream' || props.stacked
    ? n(data.current_concurrency)
    : `${n(data.current_concurrency)}/${data.effective_max_concurrency === 0 ? '--' : n(data.effective_max_concurrency)}`
})
function load(maximum: number | null): void {
  baseline.value = maximum
  mode.value = maximum === null ? 'inherit' : maximum === 0 ? 'unlimited' : 'custom'
  value.value = String(maximum || query.data.value?.effective_max_concurrency || 1)
}
watch(
  () => [props.scope, props.id, query.data.value, props.disabled] as const,
  ([scope, id, data], previous) => {
    if (previous && (previous[0] !== scope || previous[1] !== id)) {
      baseline.value = undefined
    }
    if (data?.scope === scope && data.id === id && !dirty.value && !props.disabled)
      load(data.max_concurrency)
  },
  { immediate: true },
)
watch(
  () => ({ dirty: dirty.value, valid: valid.value }),
  (state) => emit('state-change', state),
  { immediate: true },
)
function pendingValue(): number | null | undefined {
  return dirty.value ? maximum.value : undefined
}
function reset(): void {
  if (query.data.value) load(query.data.value.max_concurrency)
}
function restoreDefault(): void {
  mode.value = 'inherit'
}
function acceptSaved(saved: number | null): void {
  const queryKey = [...concurrencyQueryKey, props.scope, props.id]
  void cache.cancelQueries({ queryKey, exact: true })
  cache.setQueryData<ConcurrencyView>(queryKey, (data) =>
    data
      ? {
          ...data,
          max_concurrency: saved,
          effective_max_concurrency:
            saved ??
            (props.scope === 'global' || props.scope.startsWith('default_')
              ? 0
              : data.effective_max_concurrency),
          source: saved === null ? 'default' : 'override',
        }
      : undefined,
  )
  load(saved)
  // The write already succeeded. A display refresh cannot turn it into a failed save.
  void cache.invalidateQueries({ queryKey: concurrencyQueryKey })
}
defineExpose({ pendingValue, acceptSaved, reset })
</script>

<template>
  <div class="modern-concurrency" :class="{ 'is-editable': editable, 'is-stacked': stacked }">
    <div class="modern-concurrency-summary">
      <span v-if="showLabel" class="modern-concurrency-label">
        {{ label }}
        <AppIconButton v-if="editable && !showHelp" :icon="Info" :label="help" size="xxs" />
      </span>
      <AppTooltip
        :label="
          label +
          ' · ' +
          t(query.data.value?.shared ? 'concurrency.shared' : 'concurrency.description')
        "
      >
        <strong
          :class="{
            'is-full':
              query.data.value &&
              query.data.value.effective_max_concurrency > 0 &&
              query.data.value.current_concurrency >= query.data.value.effective_max_concurrency,
          }"
          >{{ summary }}</strong
        >
      </AppTooltip>
    </div>
    <span v-if="stacked" class="modern-concurrency-limit">{{
      query.data.value
        ? query.data.value.effective_max_concurrency === 0
          ? '--'
          : n(query.data.value.effective_max_concurrency)
        : '—'
    }}</span>
    <template v-if="editable">
      <div class="modern-concurrency-controls" :class="{ 'is-segmented': segmented }">
        <AppSegmentedField
          v-if="segmented"
          v-model="mode"
          :options="options"
          :label="t('concurrency.mode')"
          label-hidden
          size="sm"
          :disabled="locked"
        />
        <AppSelect
          v-else
          v-model="mode"
          :options="options"
          :label="t('concurrency.mode')"
          label-hidden
          size="sm"
          :disabled="locked"
        />
        <AppTextField
          v-if="mode === 'custom'"
          v-model="value"
          :label="t('concurrency.maximum')"
          label-hidden
          inputmode="numeric"
          size="sm"
          :disabled="locked"
          :error="valid ? undefined : t('concurrency.invalid')"
        />
        <span v-else aria-hidden="true" />
        <AppIconButton
          v-if="!segmented"
          :icon="RotateCcw"
          :label="t('concurrency.restoreDefault')"
          size="xxs"
          :disabled="locked || mode === 'inherit'"
          @click="restoreDefault"
        />
      </div>
      <p v-if="showHelp" class="modern-concurrency-help">
        {{ t('concurrency.deferredHelp') }}
      </p>
      <p v-if="showHelp && query.data.value?.shared" class="modern-concurrency-help">
        {{ t('concurrency.shared') }}
      </p>
    </template>
    <AppNotice v-if="editable && query.isError.value" tone="danger">
      {{ t('concurrency.loadFailed') }}
      <AppButton size="xs" variant="text" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </AppNotice>
  </div>
</template>

<style scoped>
.modern-concurrency {
  min-width: 0;
}
.modern-concurrency-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-concurrency-summary strong {
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.modern-concurrency-label {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-concurrency-summary .is-full {
  color: var(--modern-danger);
}
.modern-concurrency.is-stacked {
  display: grid;
  grid-template-rows: subgrid;
  grid-row: span 2;
  align-items: center;
}
.is-stacked .modern-concurrency-summary {
  font-size: var(--modern-font-size-section);
}
.modern-concurrency-limit {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
  line-height: var(--modern-leading-compact);
  font-variant-numeric: tabular-nums;
}
.modern-concurrency-controls {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--modern-space-2);
  margin-top: var(--modern-space-2);
}
.modern-concurrency-help {
  margin: var(--modern-space-2) 0 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-concurrency-controls.is-segmented {
  grid-template-columns: max-content minmax(0, 1fr);
  gap: var(--modern-space-3);
}
</style>
