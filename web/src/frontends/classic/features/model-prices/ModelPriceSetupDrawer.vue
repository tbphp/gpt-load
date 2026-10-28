<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, useId, watch } from 'vue'
import { ChevronDown, ChevronUp, Plus } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import {
  batchModelPrices,
  getModelPriceSources,
  modelPriceSourcesKey,
  type ModelPriceSourceDto,
} from '@/app/resources/model-prices'
import type { ModelUpstreamDto } from '@/app/resources/models'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import ModelPriceTargets from './ModelPriceTargets.vue'
import ModelPriceSummary from './ModelPriceSummary.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import ModelPriceMatrix from './ModelPriceMatrix.vue'
import ModelPriceDetails from './ModelPriceDetails.vue'
import ModelPriceSlotsEditor from './ModelPriceSlotsEditor.vue'
import {
  buildModelPriceRequest,
  createEmptyTierDraft,
  createModelPriceDraft,
  modelPriceDraftIsAllNull,
  modelPriceFormHasErrors,
  validateModelPriceDraft,
} from './model-price-form'

const props = withDefaults(defineProps<{ sources?: ModelUpstreamDto[] }>(), { sources: () => [] })
const bulk = props.sources.length === 0
const ids = ref([...new Set(props.sources.map((source) => source.price.id))])
const targetsOpen = ref(bulk)
const targetsId = useId()
const formId = useId()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const initialPrice = ids.value.length === 1 ? props.sources[0]?.price : undefined
const mode = ref<'copy' | 'link'>(initialPrice?.source_price_id ? 'link' : 'copy')
const search = ref('')
const selected = ref<ModelPriceSourceDto>()
const draft = ref(createModelPriceDraft(initialPrice))
const baseline = ref(JSON.stringify(draft.value))
const modeCount = computed(() => Object.keys(draft.value.modeSchedules).length)
const modesOpen = ref(modeCount.value > 0)
const modesId = useId()
const tiersOpen = ref(draft.value.tiers.length > 0)
const tiersId = useId()
const priceForm = ref<HTMLFormElement>()
const settingsTouched = ref(false)
const edited = computed(() => JSON.stringify(draft.value) !== baseline.value)
const dirty = computed(() => settingsTouched.value || edited.value)
const pending = ref(false)
const failure = ref('')
const unpricedConfirm = ref(false)
const errors = computed(() => validateModelPriceDraft(draft.value))
watch(
  () => draft.value.modeSchedules,
  (schedules) => {
    if (Object.keys(schedules).length) modesOpen.value = true
  },
)
watch(
  () => draft.value.tiers,
  (tiers) => {
    if (tiers.length) tiersOpen.value = true
  },
)
const guard = useUnsavedChanges(dirty, { blocked: pending })
const controller = new AbortController()
const items = computed(() => query.data.value ?? [])
const byID = computed(() => new Map(items.value.map((item) => [item.price.id, item])))
const singleTarget = computed(() =>
  ids.value.length === 1 ? byID.value.get(ids.value[0]!) : undefined,
)
const affected = computed(() => {
  const result = new Set(ids.value)
  let changed = true
  while (changed) {
    changed = false
    for (const item of items.value) {
      if (
        item.price.source_price_id &&
        result.has(item.price.source_price_id) &&
        !result.has(item.price.id)
      ) {
        result.add(item.price.id)
        changed = true
      }
    }
  }
  return items.value.filter((item) => result.has(item.price.id))
})
const groupCount = computed(
  () => new Set(affected.value.flatMap((source) => source.groups.map((group) => group.id))).size,
)
const selectionValid = computed(
  () => ids.value.length > 0 && ids.value.every((id) => byID.value.has(id)),
)
function conflicts(id: number): boolean {
  const seen = new Set<number>()
  let current: number | undefined = id
  while (current !== undefined) {
    if (ids.value.includes(current) || seen.has(current)) return true
    seen.add(current)
    current = byID.value.get(current)?.price.source_price_id
  }
  return false
}
function hasPrice(source: ModelPriceSourceDto): boolean {
  return (
    source.price.pricing_status === 'configured' && source.price.method !== 'user_marked_unpriced'
  )
}
function existingReference(source: ModelPriceSourceDto): boolean {
  return (
    ids.value.length > 0 &&
    ids.value.every((id) => byID.value.get(id)?.price.source_price_id === source.price.id)
  )
}
const canLink = computed(() =>
  Boolean(selected.value && hasPrice(selected.value) && !conflicts(selected.value.price.id)),
)
const choices = computed(() => [
  { value: 'copy', label: t('modelPrices.reuse.copy'), disabled: pending.value },
  { value: 'link', label: t('modelPrices.reuse.link'), disabled: pending.value },
])
const query = useQuery({
  queryKey: modelPriceSourcesKey,
  queryFn: ({ signal }) => getModelPriceSources(client, signal),
})
const label = (source: ModelPriceSourceDto) =>
  `${source.price.channel_name} · ${source.price.model_id}`
const options = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  const candidates = items.value.filter((item) => {
    const text = [label(item), ...item.aliases, ...item.groups.map((group) => group.name)]
      .join(' ')
      .toLocaleLowerCase()
    return (
      terms.every((term) => text.includes(term)) &&
      (mode.value !== 'link' || !conflicts(item.price.id))
    )
  })
  if (selected.value && !candidates.some((item) => item.price.id === selected.value!.price.id))
    candidates.unshift(selected.value)
  return [
    {
      value: '',
      label: t('modelPrices.reuse.' + (mode.value === 'copy' ? 'noReference' : 'select')),
    },
    ...candidates.map((item) => ({
      value: String(item.price.id),
      label: label(item),
      disabled: !hasPrice(item),
    })),
  ]
})
const optionalModes = ['fast', 'ultrafast'] as const
function modeLabel(value: string): string {
  return value === 'fast'
    ? t('models.drawer.fastPrices')
    : value === 'ultrafast'
      ? t('models.drawer.ultrafastPrices')
      : value
}
watch(
  [query.data, query.isFetching, query.isError],
  ([rows, fetching, failed]) => {
    if (!rows) return
    // 缓存先用于回显，等刷新成功再判断目标是否真的被移除。
    if (!fetching && !failed) {
      const currentIDs = new Set(rows.map((item) => item.price.id))
      if (ids.value.some((id) => !currentIDs.has(id))) {
        ids.value = ids.value.filter((id) => currentIDs.has(id))
        failure.value = t('modelPrices.reuse.targetsChanged')
      }
    }
    if (
      !settingsTouched.value &&
      mode.value === 'link' &&
      !selected.value &&
      initialPrice?.source_price_id
    ) {
      selected.value = rows.find((item) => item.price.id === initialPrice.source_price_id)
    }
    if (!fetching && !failed && mode.value === 'link' && selected.value) {
      const source = rows.find((item) => item.price.id === selected.value?.price.id)
      if (
        !source ||
        conflicts(source.price.id) ||
        (!hasPrice(source) && !existingReference(source))
      ) {
        selected.value = undefined
        failure.value = t('modelPrices.reuse.reselect')
      } else selected.value = source
    }
  },
  { immediate: true },
)
watch(ids, () => {
  if (mode.value === 'link' && selected.value && conflicts(selected.value.price.id)) {
    selected.value = undefined
    failure.value = t('modelPrices.reuse.reselect')
  }
})
async function choose(key: string): Promise<void> {
  if (pending.value || key === String(selected.value?.price.id ?? '')) return
  if (!key) {
    selected.value = undefined
    settingsTouched.value = true
    failure.value = ''
    return
  }
  const source = byID.value.get(Number(key))
  if (
    !source ||
    !hasPrice(source) ||
    (mode.value === 'link' && conflicts(source.price.id)) ||
    (edited.value && !(await guard.confirmDiscard()))
  )
    return
  selected.value = source
  settingsTouched.value = true
  if (mode.value === 'copy') {
    draft.value = createModelPriceDraft(source.price)
    baseline.value = JSON.stringify(draft.value)
  }
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
  settingsTouched.value = true
  if (value === 'link' && selected.value && conflicts(selected.value.price.id))
    selected.value = undefined
  if (value === 'copy') {
    const price =
      selected.value?.price ??
      (ids.value.length === 1 ? byID.value.get(ids.value[0]!)?.price : undefined)
    draft.value = createModelPriceDraft(price)
    baseline.value = JSON.stringify(draft.value)
  }
  unpricedConfirm.value = false
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
  if (pending.value || !selectionValid.value || (mode.value === 'link' && !canLink.value)) return
  if (mode.value === 'copy' && modelPriceFormHasErrors(errors.value)) {
    if (Object.keys(errors.value.tiers).length) tiersOpen.value = true
    if (Object.keys(errors.value.modeSchedules).length) modesOpen.value = true
    await nextTick()
    priceForm.value?.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus()
    return
  }
  if (mode.value === 'copy' && modelPriceDraftIsAllNull(draft.value) && !confirmed) {
    unpricedConfirm.value = true
    return
  }
  unpricedConfirm.value = false
  pending.value = true
  failure.value = ''
  try {
    if (mode.value === 'copy') {
      const price = buildModelPriceRequest(draft.value, confirmed)
      if (!price) return
      await batchModelPrices(client, { ids: ids.value, mode: 'copy', price }, controller.signal)
    } else {
      const source = selected.value
      if (!source) return
      await batchModelPrices(
        client,
        {
          ids: ids.value,
          mode: 'link',
          reference: { price_id: source.price.id },
        },
        controller.signal,
      )
    }
    if (controller.signal.aborted) return
    selected.value = undefined
    settingsTouched.value = false
    baseline.value = JSON.stringify(draft.value)
    await applyInvalidationPlan(cache, mutationInvalidationPlans.modelPrice.update)
    if (!controller.signal.aborted) emit('saved')
  } catch (cause) {
    if (!controller.signal.aborted)
      failure.value = cause instanceof ApiError ? cause.message : t('modelPrices.reuse.failed')
  } finally {
    pending.value = false
  }
}
async function confirmDiscardSwitch(): Promise<boolean> {
  if (!(await guard.confirmDiscard())) return false
  settingsTouched.value = false
  baseline.value = JSON.stringify(draft.value)
  return true
}
defineExpose({
  confirmDiscardSwitch,
  hasUnsavedChanges: () => dirty.value || pending.value,
})
onScopeDispose(() => {
  controller.abort()
})
</script>

<template>
  <AppDialog
    :open="true"
    size="wide"
    :title="t('modelPrices.reuse.' + (bulk ? 'bulk' : 'title'))"
    :close-label="t('modelPrices.reuse.close')"
    :dismissible="!pending"
    @update:open="!$event && close()"
  >
    <template #body>
      <div v-if="!query.data.value" class="price-setup-loading">
        <InlineFeedback v-if="query.isPending.value" tone="info">{{
          t('modelPrices.reuse.loading')
        }}</InlineFeedback>
        <InlineFeedback v-else tone="danger">
          {{ t('modelPrices.reuse.loadFailed') }}
          <template #action
            ><AppButton variant="link" @click="query.refetch()">{{
              t('common.retry')
            }}</AppButton></template
          >
        </InlineFeedback>
      </div>
      <div v-else class="price-setup-workspace">
        <section
          class="price-setup-selection"
          :class="{ 'is-collapsed': !targetsOpen }"
          :aria-label="t('modelPrices.reuse.chooseTargets')"
        >
          <header class="price-setup-selection-header">
            <h3>{{ t('modelPrices.reuse.chooseTargets') }}</h3>
            <AppButton
              class="price-setup-toggle"
              variant="ghost"
              :aria-expanded="targetsOpen"
              :aria-controls="targetsId"
              @click="targetsOpen = !targetsOpen"
            >
              <component
                :is="targetsOpen ? ChevronUp : ChevronDown"
                :size="16"
                aria-hidden="true"
              />
              {{ t('modelPrices.reuse.' + (targetsOpen ? 'collapseTargets' : 'chooseTargets')) }}
            </AppButton>
            <span v-if="!targetsOpen" class="price-setup-selection-summary" aria-live="polite">
              <OverflowTooltip v-if="singleTarget" as="span" :content="label(singleTarget)">{{
                label(singleTarget)
              }}</OverflowTooltip>
              <template v-else>{{
                t('modelPrices.reuse.selected', { count: ids.length })
              }}</template>
            </span>
          </header>
          <div :id="targetsId" class="price-setup-selection-list">
            <ModelPriceTargets v-model="ids" :items="items" :disabled="pending" />
          </div>
        </section>
        <section class="price-setup-settings" :aria-label="t('modelPrices.reuse.title')">
          <div class="price-setup-controls">
            <InlineFeedback v-if="query.isError.value" tone="warning">
              {{ t('modelPrices.reuse.loadFailed') }}
              <template #action
                ><AppButton
                  variant="link"
                  :disabled="query.isFetching.value"
                  @click="query.refetch()"
                  >{{ t('common.retry') }}</AppButton
                ></template
              >
            </InlineFeedback>
            <div class="price-setup-method">
              <h3>{{ t('modelPrices.reuse.title') }}</h3>
              <SegmentedControl
                :model-value="mode"
                :options="choices"
                :label="t('modelPrices.reuse.title')"
                size="sm"
                @update:model-value="changeMode"
              />
            </div>
            <div class="price-setup-reference">
              <AppSearchInput
                v-model="search"
                size="xs"
                :label="t('modelPrices.reuse.' + (mode === 'copy' ? 'sourceOptional' : 'source'))"
                :placeholder="t('modelPrices.reuse.search')"
                :clear-label="t('modelPrices.reuse.clearSearch')"
                :disabled="pending"
              />
              <AppSelect
                size="compact"
                :model-value="selected ? String(selected.price.id) : ''"
                :options="options"
                :label="t('modelPrices.reuse.' + (mode === 'copy' ? 'sourceOptional' : 'source'))"
                :disabled="pending"
                @update:model-value="choose"
              >
                <template #option="{ option }">
                  <span class="price-source-option">
                    <span class="price-source-identity">
                      <OverflowTooltip
                        as="span"
                        :content="byID.get(Number(option.value))?.price.model_id ?? option.label"
                        >{{
                          byID.get(Number(option.value))?.price.model_id ?? option.label
                        }}</OverflowTooltip
                      >
                      <small v-if="byID.get(Number(option.value))"
                        ><OverflowTooltip
                          as="span"
                          :content="byID.get(Number(option.value))!.price.channel_name"
                          >{{ byID.get(Number(option.value))!.price.channel_name }}</OverflowTooltip
                        ></small
                      >
                    </span>
                    <ModelPriceSummary
                      v-if="byID.get(Number(option.value))"
                      :prices="byID.get(Number(option.value))!.price.prices"
                    />
                  </span>
                </template>
              </AppSelect>
            </div>
            <p v-if="mode === 'link'" class="price-setup-hint">
              {{ t('modelPrices.reuse.linkHint') }}
            </p>
            <InlineFeedback
              v-if="mode === 'link' && selected && !hasPrice(selected)"
              tone="warning"
              >{{ t('modelPrices.reuse.sourceUnpriced') }}</InlineFeedback
            >
            <InlineFeedback v-if="failure" tone="danger">{{ failure }}</InlineFeedback>
          </div>
          <div v-if="mode === 'copy' || selected" class="price-setup-prices">
            <form
              v-if="mode === 'copy'"
              :id="formId"
              ref="priceForm"
              class="price-setup-form"
              novalidate
              @submit.prevent="save()"
            >
              <header class="price-setup-base-header">
                <h4>{{ t('modelPrices.reuse.basePrices') }}</h4>
                <span>{{ t('modelPrices.matrix.unit') }}</span>
              </header>
              <ModelPriceSlotsEditor
                v-model:draft="draft.base"
                :id-prefix="formId + '-base'"
                :errors="errors.base"
                :pending="pending"
              />
              <p class="price-setup-hint">{{ t('modelPrices.matrix.oneHourRule') }}</p>

              <section class="price-setup-section">
                <AppButton
                  class="price-setup-section-toggle"
                  variant="ghost"
                  size="compact"
                  :disabled="pending"
                  :aria-expanded="tiersOpen"
                  :aria-controls="tiersId"
                  @click="tiersOpen = !tiersOpen"
                >
                  <component
                    :is="tiersOpen ? ChevronUp : ChevronDown"
                    :size="14"
                    aria-hidden="true"
                  />
                  <span>{{ t('modelPrices.reuse.tierPrices') }}</span>
                  <small v-if="draft.tiers.length">{{
                    t('modelPrices.reuse.tierCount', { count: draft.tiers.length })
                  }}</small>
                </AppButton>
                <div v-show="tiersOpen" :id="tiersId" class="price-setup-section-body">
                  <ModelPriceMatrix
                    v-if="draft.tiers.length"
                    :draft="draft"
                    tiers-only
                    :errors="errors"
                    :pending="pending"
                    failure=""
                    @update:draft="draft.tiers = $event.tiers"
                    @remove-tier="draft.tiers = draft.tiers.filter((tier) => tier.key !== $event)"
                  />
                  <div class="price-setup-add">
                    <AppButton
                      variant="link"
                      size="xs"
                      :disabled="pending"
                      @click="draft.tiers.push(createEmptyTierDraft())"
                      ><Plus :size="14" aria-hidden="true" />{{
                        t('modelPrices.matrix.addTier')
                      }}</AppButton
                    >
                  </div>
                </div>
              </section>

              <section class="price-setup-section">
                <AppButton
                  class="price-setup-section-toggle"
                  variant="ghost"
                  size="compact"
                  :disabled="pending"
                  :aria-expanded="modesOpen"
                  :aria-controls="modesId"
                  @click="modesOpen = !modesOpen"
                >
                  <component
                    :is="modesOpen ? ChevronUp : ChevronDown"
                    :size="14"
                    aria-hidden="true"
                  />
                  <span>{{ t('modelPrices.reuse.modePrices') }}</span>
                  <small>{{
                    modeCount
                      ? t('modelPrices.reuse.modeCount', { count: modeCount })
                      : 'Fast / Ultrafast'
                  }}</small>
                </AppButton>
                <div v-show="modesOpen" :id="modesId" class="price-setup-section-body">
                  <section
                    v-for="(schedule, name) in draft.modeSchedules"
                    :key="name"
                    class="price-setup-mode"
                  >
                    <header>
                      <strong>{{ modeLabel(String(name)) }}</strong>
                      <AppButton
                        variant="ghost"
                        size="compact"
                        :disabled="pending"
                        @click="delete draft.modeSchedules[name]"
                        >{{ t('modelPrices.reuse.removeMode') }}</AppButton
                      >
                    </header>
                    <ModelPriceMatrix
                      v-if="name !== 'fast'"
                      :draft="schedule"
                      :errors="errors.modeSchedules[name] ?? { base: {}, tiers: {} }"
                      :pending="pending"
                      failure=""
                      @update:draft="draft.modeSchedules[name] = $event"
                      @add-tier="schedule.tiers.push(createEmptyTierDraft())"
                      @remove-tier="
                        schedule.tiers = schedule.tiers.filter((tier) => tier.key !== $event)
                      "
                    />
                    <ModelPriceSlotsEditor
                      v-else
                      v-model:draft="schedule.base"
                      :id-prefix="'reuse-' + name"
                      :errors="errors.modeSchedules[name]?.base ?? {}"
                      :pending="pending"
                    />
                  </section>
                  <div class="price-setup-add">
                    <AppButton
                      v-for="name in optionalModes.filter((value) => !draft.modeSchedules[value])"
                      :key="name"
                      variant="link"
                      size="xs"
                      :disabled="pending"
                      @click="addMode(name)"
                      ><Plus :size="14" aria-hidden="true" />{{
                        t('modelPrices.reuse.modeNames.' + name)
                      }}</AppButton
                    >
                  </div>
                </div>
              </section>
            </form>
            <template v-else-if="selected">
              <p class="price-setup-hint">{{ t('modelPrices.matrix.unit') }}</p>
              <ModelPriceDetails :price="selected.price" />
            </template>
          </div>
          <p v-if="mode === 'copy'" class="price-setup-replacement">
            {{ t('modelPrices.reuse.replaceHint') }}
          </p>
        </section>
      </div>
    </template>
    <template #footer>
      <div class="price-setup-footer">
        <div class="price-setup-impact" aria-live="polite">
          <span>{{ t('modelPrices.reuse.selected', { count: ids.length }) }}</span>
          <small v-if="ids.length">{{
            t(
              'modelPrices.reuse.' +
                (affected.length > ids.length ? 'scopeDependents' : 'scopeGroups'),
              { count: affected.length - ids.length, groups: groupCount },
            )
          }}</small>
        </div>
        <div class="price-setup-actions">
          <AppButton variant="secondary" :disabled="pending" @click="close">{{
            t('common.cancel')
          }}</AppButton>
          <AppButton
            :type="mode === 'copy' ? 'submit' : 'button'"
            :form="mode === 'copy' ? formId : undefined"
            :busy="pending"
            :disabled="!selectionValid || (mode === 'link' && !canLink)"
            @click="mode === 'link' && save()"
            >{{
              ids.length
                ? t('modelPrices.reuse.apply', { count: ids.length })
                : t('modelPrices.matrix.save')
            }}</AppButton
          >
        </div>
      </div>
    </template>
  </AppDialog>
  <AppConfirmDialog
    appearance="ledger"
    tone="danger"
    :open="unpricedConfirm"
    :title="t('modelPrices.matrix.unpricedConfirm.title')"
    :description="
      t('modelPrices.matrix.unpricedConfirm.description', {
        model: t('modelPrices.reuse.selected', { count: ids.length }),
      })
    "
    :close-label="t('modelPrices.matrix.unpricedConfirm.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('modelPrices.matrix.unpricedConfirm.confirm')"
    :pending="pending"
    @update:open="unpricedConfirm = $event"
    @confirm="save(true)"
  >
    <InlineFeedback appearance="ledger" tone="warning">{{
      t('modelPrices.matrix.unpricedConfirm.warning')
    }}</InlineFeedback>
  </AppConfirmDialog>
</template>

<style scoped>
.price-setup-loading {
  padding: var(--space-4);
}
.price-setup-workspace {
  height: 100%;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
}
.price-setup-selection {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  border-right: 1px solid var(--color-border-subtle);
}
.price-setup-selection-header {
  display: flex;
  flex: none;
  min-height: calc(var(--control-sm) + var(--space-4));
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-4) 0;
}
.price-setup-selection-header h3 {
  flex: none;
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
}
.price-setup-selection-summary {
  display: none;
  min-width: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.price-setup-toggle {
  display: none;
}
.price-setup-selection-list {
  flex: 1;
  min-height: 0;
  padding: var(--space-4) var(--space-4) 0;
}
.price-setup-settings {
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--space-4);
}
.price-setup-controls,
.price-setup-prices,
.price-setup-form,
.price-setup-reference {
  display: grid;
  gap: var(--space-3);
}
.price-setup-prices {
  margin-top: var(--space-4);
}
.price-setup-hint {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.price-source-option {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
}
.price-source-option > :first-child {
  flex: 1;
  min-width: 140px;
}
.price-setup-mode {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.price-setup-mode:first-child {
  border-top: 0;
  padding-top: 0;
}
.price-setup-mode header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.price-setup-add {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.price-setup-method h3 {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
}
.price-setup-base-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.price-setup-base-header h4 {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
}
.price-setup-base-header > span {
  margin-left: auto;
  font-size: var(--text-label-xs);
  color: var(--color-text-muted);
}
.price-setup-section {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-1);
}
.price-setup-section-body {
  display: grid;
  gap: var(--space-3);
  padding-top: var(--space-2);
}
.price-setup-section-toggle {
  width: 100%;
  justify-content: flex-start;
  padding-inline: 0;
}
.price-setup-section-toggle small {
  margin-left: auto;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 400;
}
.price-source-identity {
  display: flex;
  align-items: baseline;
  min-width: 0;
  gap: var(--space-2);
}
.price-source-identity > :first-child {
  flex: 1;
  min-width: 0;
}
.price-source-identity small {
  min-width: 0;
  max-width: 40%;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
.price-setup-method {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.price-setup-replacement {
  margin: var(--space-3) 0 0;
  font-size: var(--text-label-xs);
  color: var(--color-text-muted);
}
.price-setup-footer {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.price-setup-impact {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  gap: var(--space-1) var(--space-3);
  font-size: var(--text-sm);
}
.price-setup-impact small {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
.price-setup-actions {
  display: flex;
  flex: none;
  gap: var(--space-2);
}
@media (max-width: 1150px) {
  .price-setup-workspace {
    display: block;
    overflow-y: auto;
    overscroll-behavior: contain;
  }
  .price-setup-selection {
    border-right: 0;
    border-bottom: 1px solid var(--color-border-subtle);
  }
  .price-setup-selection-header {
    padding-bottom: var(--space-3);
  }
  .price-setup-selection-header h3 {
    display: none;
  }
  .price-setup-selection-summary {
    display: block;
  }
  .price-setup-toggle {
    display: inline-flex;
    flex: none;
  }
  .price-setup-selection-list {
    flex: none;
    height: min(360px, 50dvh);
    min-height: 280px;
  }
  .price-setup-selection.is-collapsed .price-setup-selection-list {
    display: none;
  }
  .price-setup-settings {
    overflow: visible;
  }
}
@media (max-width: 760px) {
  .price-setup-footer {
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .price-setup-impact,
  .price-setup-actions {
    width: 100%;
  }
  .price-setup-actions > :last-child {
    flex: 1;
  }
}
</style>
