<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import {
  batchModelPrices,
  getModelPriceSources,
  type ModelPriceDto,
  type ModelPriceSourceDto,
} from '@/app/resources/model-prices'
import type { ModelUpstreamDto } from '@/app/resources/models'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import ModelPriceMatrix from './ModelPriceMatrix.vue'
import ModelPriceSlotsEditor from './ModelPriceSlotsEditor.vue'
import {
  buildModelPriceRequest,
  createEmptyTierDraft,
  createModelPriceDraft,
  modelPriceDraftIsAllNull,
  modelPriceFields,
  modelPriceFormHasErrors,
  validateModelPriceDraft,
} from './model-price-form'

const props = defineProps<{ sources: ModelUpstreamDto[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const mode = ref<'copy' | 'link'>('copy')
const search = ref('')
const queryText = ref('')
const debounce = useDebouncedAction(250)
const selected = ref<ModelPriceSourceDto>()
const draft = ref(createModelPriceDraft())
const baseline = ref('')
const edited = computed(
  () => Boolean(selected.value) && JSON.stringify(draft.value) !== baseline.value,
)
const dirty = computed(() => Boolean(selected.value))
const pending = ref(false)
const failure = ref('')
const unpricedConfirm = ref(false)
const errors = computed(() => validateModelPriceDraft(draft.value))
const guard = useUnsavedChanges(dirty, { blocked: pending })
const controller = new AbortController()
const ids = computed(() => [...new Set(props.sources.map((source) => source.price.id))])
const groupCount = computed(
  () =>
    new Set(props.sources.flatMap((source) => source.affected_groups.map((group) => group.id)))
      .size,
)
const choices = computed(() => [
  { value: 'copy', label: t('modelPrices.reuse.copy') },
  { value: 'link', label: t('modelPrices.reuse.link') },
])
const query = useQuery(
  computed(() => ({
    queryKey: ['model-price-sources', mode.value, queryText.value],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getModelPriceSources(client, mode.value, queryText.value, signal),
  })),
)
const label = (source: ModelPriceSourceDto) =>
  `${t('modelPrices.reuse.' + source.kind)} · ${source.provider_name} · ${source.model_id}`
const options = computed(() => {
  const items = [...(query.data.value?.items ?? [])]
  if (selected.value && !items.some((item) => item.key === selected.value!.key))
    items.unshift(selected.value)
  return [
    { value: '', label: t('modelPrices.reuse.select') },
    ...items.map((item) => ({ value: item.key, label: label(item) })),
  ]
})
const previewRows = computed(() =>
  selected.value
    ? [
        { ...selected.value, name: t('modelPrices.matrix.baseRow') },
        ...Object.entries(selected.value.mode_schedules).map(([name, schedule]) => ({
          name,
          ...schedule,
        })),
      ].flatMap((schedule) => [
        { label: schedule.name, prices: schedule.prices },
        ...schedule.context_tiers.map((tier) => ({
          label: `${schedule.name} ≥ ${tier.threshold_tokens}`,
          prices: tier.prices,
        })),
      ])
    : [],
)
const optionalModes = ['fast', 'ultrafast'] as const
function modeLabel(value: string): string {
  return value === 'fast'
    ? t('models.drawer.fastPrices')
    : value === 'ultrafast'
      ? t('models.drawer.ultrafastPrices')
      : value
}
watch(search, () =>
  debounce.schedule(() => {
    queryText.value = Array.from(search.value.trim()).slice(0, 200).join('')
  }),
)
async function choose(key: string): Promise<void> {
  if (
    pending.value ||
    key === selected.value?.key ||
    (edited.value && !(await guard.confirmDiscard()))
  )
    return
  const source = query.data.value?.items.find((item) => item.key === key)
  selected.value = source
  if (source && props.sources[0])
    draft.value = createModelPriceDraft({ ...props.sources[0].price, ...source } as ModelPriceDto)
  baseline.value = JSON.stringify(draft.value)
  failure.value = ''
}
async function changeMode(value: string): Promise<void> {
  if (
    pending.value ||
    value === mode.value ||
    (value !== 'copy' && value !== 'link') ||
    (edited.value && !(await guard.confirmDiscard()))
  )
    return
  mode.value = value
  selected.value = undefined
  failure.value = ''
}
async function close(): Promise<void> {
  if (await guard.confirmDiscard()) emit('close')
}
function addMode(value: string): void {
  draft.value.modeSchedules[value] = {
    base: { input: '', output: '', cache_read: '', cache_write: '' },
    tiers: [],
  }
}
async function save(confirmed = false): Promise<void> {
  if (pending.value || !selected.value) return
  if (mode.value === 'copy' && modelPriceFormHasErrors(errors.value)) return
  if (mode.value === 'copy' && modelPriceDraftIsAllNull(draft.value) && !confirmed) {
    unpricedConfirm.value = true
    return
  }
  pending.value = true
  failure.value = ''
  try {
    if (mode.value === 'copy') {
      const price = buildModelPriceRequest(draft.value, confirmed)
      if (!price) return
      await batchModelPrices(client, { ids: ids.value, mode: 'copy', price }, controller.signal)
    } else {
      await batchModelPrices(
        client,
        {
          ids: ids.value,
          mode: 'link',
          reference: { provider_id: selected.value.provider_id, model_id: selected.value.model_id },
        },
        controller.signal,
      )
    }
    if (controller.signal.aborted) return
    selected.value = undefined
    await applyInvalidationPlan(cache, mutationInvalidationPlans.modelPrice.update)
    if (!controller.signal.aborted) emit('saved')
  } catch (cause) {
    if (!controller.signal.aborted)
      failure.value = cause instanceof ApiError ? cause.message : t('modelPrices.reuse.failed')
  } finally {
    pending.value = false
  }
}
onScopeDispose(() => {
  debounce.cancel()
  controller.abort()
})
</script>

<template>
  <AppDrawer
    :open="true"
    appearance="ledger"
    :title="t('modelPrices.reuse.title')"
    :description="t('modelPrices.reuse.replace', { count: ids.length })"
    :close-label="t('modelPrices.reuse.close')"
    :dismissible="!pending"
    show-description
    @update:open="!$event && close()"
  >
    <template #filters>
      <div class="price-setup-controls">
        <SegmentedControl
          :model-value="mode"
          :options="choices"
          :label="t('modelPrices.reuse.title')"
          @update:model-value="changeMode"
        />
        <AppSearchInput
          v-model="search"
          :label="t('modelPrices.reuse.source')"
          :placeholder="t('modelPrices.reuse.search')"
          :clear-label="t('modelPrices.reuse.clear')"
          :disabled="pending"
        />
        <AppSelect
          :model-value="selected?.key ?? ''"
          :options="options"
          :label="t('modelPrices.reuse.source')"
          :disabled="pending || query.isFetching.value"
          @update:model-value="choose"
        />
        <p v-if="query.isFetching.value" class="price-setup-hint">
          {{ t('modelPrices.reuse.loading') }}
        </p>
        <InlineFeedback v-if="query.isError.value" tone="danger"
          >{{ t('modelPrices.reuse.failed')
          }}<template #action
            ><AppButton variant="link" @click="query.refetch()">{{
              t('common.retry')
            }}</AppButton></template
          ></InlineFeedback
        >
        <p v-if="query.data.value?.has_more" class="price-setup-hint">
          {{ t('modelPrices.reuse.more') }}
        </p>
      </div>
    </template>
    <div class="price-setup-body">
      <InlineFeedback :tone="mode === 'copy' ? 'warning' : 'info'">{{
        t('modelPrices.reuse.' + (mode === 'copy' ? 'replace' : 'follow'), { count: ids.length })
      }}</InlineFeedback>
      <div class="price-setup-targets">
        <span v-for="source in sources" :key="source.price.id"
          >{{ source.price.channel_name }} · {{ source.model_id }}</span
        >
      </div>
      <p class="price-setup-hint">{{ t('modelPrices.reuse.impact', { count: groupCount }) }}</p>
      <InlineFeedback v-if="failure" tone="danger">{{ failure }}</InlineFeedback>
      <template v-if="selected && mode === 'copy'">
        <ModelPriceMatrix
          v-model:draft="draft"
          v-model:unpriced-confirm-open="unpricedConfirm"
          :model-id="t('modelPrices.reuse.selected', { count: ids.length })"
          :errors="errors"
          :pending="pending"
          failure=""
          @add-tier="draft.tiers.push(createEmptyTierDraft())"
          @remove-tier="draft.tiers = draft.tiers.filter((tier) => tier.key !== $event)"
          @confirm-unpriced="save(true)"
        />
        <section
          v-for="(schedule, name) in draft.modeSchedules"
          :key="name"
          class="price-setup-mode"
        >
          <header>
            <strong>{{ modeLabel(String(name)) }}</strong
            ><AppButton
              variant="ghost"
              size="compact"
              :disabled="pending"
              @click="delete draft.modeSchedules[name]"
              >{{ t('modelPrices.reuse.removeMode') }}</AppButton
            >
          </header>
          <ModelPriceSlotsEditor
            v-model:draft="schedule.base"
            :id-prefix="'reuse-' + name"
            :errors="errors.modeSchedules[name]?.base ?? {}"
            :pending="pending"
          />
          <p v-for="tier in schedule.tiers" :key="tier.key" class="price-setup-hint">
            ≥ {{ tier.threshold }} ·
            <span v-for="field in modelPriceFields" :key="field"
              >{{ t('modelPrices.fields.' + field) }} {{ tier.slots[field] || '—' }}
            </span>
          </p>
        </section>
        <div class="price-setup-add">
          <AppButton
            v-for="name in optionalModes.filter((value) => !draft.modeSchedules[value])"
            :key="name"
            variant="secondary"
            size="compact"
            :disabled="pending"
            @click="addMode(name)"
            >{{ modeLabel(name) }} +</AppButton
          >
        </div>
      </template>
      <div v-else-if="selected" class="price-setup-preview">
        <p class="price-setup-hint">{{ t('modelPrices.matrix.unit') }}</p>
        <table>
          <thead>
            <tr>
              <th></th>
              <th v-for="field in modelPriceFields" :key="field">
                {{ t('modelPrices.fields.' + field) }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in previewRows" :key="row.label">
              <th>{{ row.label }}</th>
              <td v-for="field in modelPriceFields" :key="field">{{ row.prices[field] ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <template #footer>
      <AppButton variant="secondary" :disabled="pending" @click="close">{{
        t('common.cancel')
      }}</AppButton>
      <AppButton
        :busy="pending"
        :disabled="!selected || (mode === 'copy' && modelPriceFormHasErrors(errors))"
        @click="save()"
        >{{ t('modelPrices.matrix.save') }}</AppButton
      >
    </template>
  </AppDrawer>
</template>

<style scoped>
.price-setup-controls,
.price-setup-body {
  display: grid;
  gap: var(--space-3);
}
.price-setup-controls {
  padding: var(--space-3);
}
.price-setup-hint {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.price-setup-targets {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  max-height: 100px;
  overflow: auto;
}
.price-setup-targets span {
  font-size: var(--text-sm);
}
.price-setup-mode {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.price-setup-mode header,
.price-setup-add {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
}
.price-setup-preview {
  overflow-x: auto;
}
.price-setup-preview table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
.price-setup-preview th,
.price-setup-preview td {
  padding: var(--space-2);
  text-align: left;
  border-bottom: 1px solid var(--color-border-subtle);
  font-weight: 400;
  white-space: nowrap;
}
</style>
