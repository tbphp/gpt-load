<script setup lang="ts">
import { ListChecks, Plus, Search, Trash2 } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelCandidate } from '@modern/api/model-discovery'
import { AppButton, AppIconButton, AppPagination, AppTextField } from '@modern/components/ui'
import { useLoadingFeedback } from '@modern/components/ui/loading'
import ModelSelectionDialog from '../models/ModelSelectionDialog.vue'
import ModelSourceBadges from '../models/ModelSourceBadges.vue'
import ModelPriceBadge from '../models/ModelPriceBadge.vue'
import { modelErrors, type GroupDraftModel } from './group-create-rules'

const props = defineProps<{
  candidates: readonly ModelCandidate[]
  connectionRevision: number
  discoverySupported: boolean
  canDiscover: boolean
  loading: boolean
  discoveryError?: string
  disabled?: boolean
  attempted?: boolean
}>()
const models = defineModel<GroupDraftModel[]>({ required: true })
const emit = defineEmits<{ discover: []; cancelDiscovery: [] }>()
const { t, n } = useI18n()
const choosing = ref(false)
const loaded = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = ref(10)
const fields = new Map<number, { focus(): void; $el?: HTMLElement }>()
const filtering = ref(false)
const feedback = useLoadingFeedback(filtering)
let trigger: HTMLElement | undefined
let nextKey = 1
const errors = computed(() => modelErrors(models.value))
const candidatesByID = computed(
  () => new Map(props.candidates.map((candidate) => [candidate.id, candidate])),
)
const filtered = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return models.value.filter((model) =>
    terms.every((term) => [model.id, model.alias].join(' ').toLocaleLowerCase().includes(term)),
  )
})
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
watch([search, pageSize], () => {
  page.value = 1
})
watch([search, page, pageSize], async () => {
  filtering.value = true
  await nextTick()
  filtering.value = false
})
watch(
  () => filtered.value.length,
  (length) => {
    page.value = Math.min(page.value, Math.max(1, Math.ceil(length / pageSize.value)))
  },
)
watch(
  () => props.loading,
  (loading, previous) => {
    if (choosing.value && previous && !loading && !props.discoveryError) loaded.value = true
  },
)
watch(
  () => props.connectionRevision,
  () => {
    loaded.value = false
    choosing.value = false
  },
)
function fieldRef(key: number, element: unknown): void {
  if (element && typeof element === 'object' && 'focus' in element)
    fields.set(key, element as { focus(): void; $el?: HTMLElement })
  else fields.delete(key)
}
function nextModel(id: string, origin: GroupDraftModel['origin']): GroupDraftModel {
  return { key: nextKey++, id, alias: '', origin }
}
function reserveKeys(): void {
  for (const model of models.value) nextKey = Math.max(nextKey, model.key + 1)
}
async function focusModel(model: GroupDraftModel): Promise<void> {
  search.value = ''
  await nextTick()
  page.value =
    Math.floor(models.value.findIndex((item) => item.key === model.key) / pageSize.value) + 1
  await nextTick()
  const field = fields.get(model.key)
  field?.focus()
  field?.$el?.scrollIntoView({ block: 'nearest' })
}
async function addManual(): Promise<void> {
  if (props.disabled) return
  reserveKeys()
  const model = nextModel('', 'manual')
  models.value = [...models.value, model]
  await focusModel(model)
}
function openSelection(event: MouseEvent): void {
  if (props.disabled || !props.canDiscover) return
  trigger = event.currentTarget as HTMLElement
  choosing.value = true
  if (!loaded.value) emit('discover')
}
async function closeSelection(): Promise<void> {
  choosing.value = false
  emit('cancelDiscovery')
  await nextTick()
  if (trigger?.isConnected) trigger.focus({ preventScroll: true })
}
function addCandidates(candidates: ModelCandidate[]): void {
  if (props.disabled) return
  reserveKeys()
  const existing = new Set(models.value.map((model) => model.id.trim()))
  models.value = [
    ...models.value,
    ...candidates
      .filter((candidate) => !existing.has(candidate.id))
      .map((candidate) => nextModel(candidate.id, 'discovery')),
  ]
  void closeSelection()
}
function update(key: number, field: 'id' | 'alias', value: string): void {
  models.value = models.value.map((model) =>
    model.key === key
      ? { ...model, [field]: value, ...(field === 'id' ? { origin: 'manual' as const } : {}) }
      : model,
  )
}
defineExpose({
  focusFirstInvalid: async () => {
    const model = models.value.find((item) => errors.value.has(item.key))
    if (model) await focusModel(model)
  },
})
</script>

<template>
  <section class="modern-create-models" :aria-label="t('groupCreate.models')">
    <div class="modern-create-model-heading">
      <h3>
        {{ t('groupCreate.models') }} <span>{{ n(models.length) }}</span>
      </h3>
      <div class="modern-create-model-tools">
        <AppButton
          v-if="discoverySupported"
          :icon="ListChecks"
          variant="brand"
          size="sm"
          :disabled="disabled || !canDiscover"
          @click="openSelection"
          >{{ t('modelSelection.title') }}</AppButton
        >
        <AppButton :icon="Plus" variant="ghost" size="xs" :disabled="disabled" @click="addManual">{{
          t('groupCreate.manualModel')
        }}</AppButton>
      </div>
    </div>
    <AppTextField
      v-if="models.length > 5 || search"
      v-model="search"
      :label="t('modelSelection.searchSelected')"
      label-hidden
      :placeholder="t('modelSelection.searchSelected')"
      :icon="Search"
      :loading="feedback"
      type="search"
    />
    <p v-if="!models.length" class="modern-create-model-empty">
      {{ t('groupCreate.modelsOptional') }}
    </p>
    <p v-else-if="!rows.length" class="modern-create-model-empty">
      {{ t('modelSelection.empty') }}
    </p>
    <div v-else class="modern-create-model-list">
      <div class="modern-create-model-labels" aria-hidden="true">
        <span>{{ t('groupCreate.modelID') }}</span
        ><span>{{ t('groupCreate.alias') }}</span>
      </div>
      <div class="modern-create-model-rows">
        <div v-for="model in rows" :key="model.key" class="modern-create-model-row">
          <AppTextField
            :ref="(element) => fieldRef(model.key, element)"
            :model-value="model.id"
            :label="t('groupCreate.modelID')"
            label-hidden
            :disabled="disabled"
            :error="
              attempted && errors.get(model.key) === 'id'
                ? t('groupCreate.modelIDRequired')
                : undefined
            "
            autocomplete="off"
            spellcheck="false"
            size="sm"
            @update:model-value="update(model.key, 'id', $event)"
          />
          <AppTextField
            :model-value="model.alias"
            :label="t('groupCreate.alias')"
            label-hidden
            :placeholder="t('groupCreate.aliasOptional')"
            :disabled="disabled"
            :error="
              attempted && errors.get(model.key) === 'duplicate'
                ? t('groupCreate.modelConflict')
                : undefined
            "
            autocomplete="off"
            spellcheck="false"
            size="sm"
            @update:model-value="update(model.key, 'alias', $event)"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('groupCreate.removeModel', { name: model.id || t('groupCreate.modelID') })"
            size="sm"
            :disabled="disabled"
            @click="models = models.filter((item) => item.key !== model.key)"
          />
          <div class="modern-create-model-evidence">
            <ModelSourceBadges
              :sources="candidatesByID.get(model.id.trim())?.sources"
              :manual="model.origin === 'manual'"
            />
            <ModelPriceBadge :candidate="candidatesByID.get(model.id.trim())" />
          </div>
        </div>
      </div>
    </div>
    <AppPagination
      v-if="models.length > 10"
      v-model:page="page"
      v-model:page-size="pageSize"
      mode="total"
      :total="filtered.length"
      :page-sizes="[10, 20, 50]"
      :disabled="disabled"
      :pending="feedback"
    />
  </section>
  <ModelSelectionDialog
    v-if="choosing"
    :candidates="candidates"
    :current-ids="models.map((model) => model.id)"
    :loading="loading"
    :error="discoveryError"
    @close="closeSelection"
    @refresh="emit('discover')"
    @confirm="addCandidates"
  />
</template>

<style scoped>
.modern-create-models {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-create-model-heading,
.modern-create-model-tools {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-create-model-heading {
  justify-content: space-between;
  flex-wrap: wrap;
}
.modern-create-model-heading h3 {
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-create-model-heading h3 span {
  color: var(--modern-muted);
  margin-left: var(--modern-space-1);
  font-weight: var(--modern-weight-regular);
}
.modern-create-model-list {
  min-width: 0;
}
.modern-create-model-labels,
.modern-create-model-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) var(--modern-control-sm);
  gap: var(--modern-space-2);
  align-items: start;
}
.modern-create-model-labels {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  padding: 0 var(--modern-space-1) var(--modern-space-2);
}
.modern-create-model-rows {
  display: grid;
  padding: var(--modern-space-1);
  gap: var(--modern-space-3);
}
.modern-create-model-evidence {
  grid-column: 1 / -1;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-create-model-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
@media (max-width: 760px) {
  .modern-create-model-labels,
  .modern-create-model-row {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) var(--modern-touch-target);
  }
}
</style>
