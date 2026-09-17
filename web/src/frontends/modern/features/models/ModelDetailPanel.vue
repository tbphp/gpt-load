<script setup lang="ts">
import { Boxes, PencilLine, RotateCcw } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { DialogRoot } from 'reka-ui'
import {
  getModelContext,
  getModelSource,
  modelContextKey,
  modelSourceKey,
  resetModelPrice,
  type ModelFilters,
  type ModelPrice,
} from '@modern/api/models'
import { useApiClient } from '@shared/http/client-context'
import { useMessages, useMessageSource } from '@modern/app/messages'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppOverflowText,
  AppProtocolTag,
  AppSearchSelect,
} from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { priceStatus } from './models-display'
import { useLoadingActivity } from '@modern/components/ui/loading'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import ModelPricingDetails from './ModelPricingDetails.vue'
import './model-group-chip.css'
import ModelPriceEditor from './ModelPriceEditor.vue'

const props = defineProps<{
  model: string
  source: number
  groups: ModelFilters['groups']
  admin: boolean
}>()
const emit = defineEmits<{ close: []; select: [source: number]; changed: [] }>()
const { t, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const messages = useMessages()
const controller = new AbortController()
const context = useQuery(
  computed(() => ({
    queryKey: modelContextKey(props.model, props.groups),
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getModelContext(client, props.model, props.groups, signal),
  })),
)
const modelData = computed(() => context.data.value)
const chosen = computed(() =>
  modelData.value?.sources.find((source) => source.price.id === props.source),
)
const detail = useQuery(
  computed(() => ({
    queryKey: modelSourceKey(props.source),
    queryFn: ({ signal }: { signal: AbortSignal }) => getModelSource(client, props.source, signal),
    enabled: props.admin && Boolean(chosen.value),
  })),
)
const price = computed(() => (props.admin ? detail.data.value?.price : chosen.value?.price))
const catalog = computed(() => (props.admin ? detail.data.value?.catalog : chosen.value?.catalog))
const associations = computed(() => detail.data.value?.associations ?? [])
const groups = computed(() => {
  const rows = props.admin
    ? associations.value.filter((item) => item.model === props.model).map((item) => item.group)
    : []
  return [...new Map(rows.map((group) => [group.id, group])).values()]
})
const relatedModels = computed(() => [...new Set(associations.value.map((item) => item.model))])
const sharedProtocols = computed(() => {
  const first = groups.value[0]?.protocols ?? []
  const signature = [...first].sort().join('\n')
  return groups.value.every((group) => [...group.protocols].sort().join('\n') === signature)
    ? first
    : null
})
const options = computed(() =>
  (modelData.value?.sources ?? []).map((source) => ({
    value: String(source.price.id),
    label: (source.price.channel.name || t('logs.deleted')) + ' · ' + source.model,
  })),
)
const sourceMap = computed(
  () => new Map(modelData.value?.sources.map((source) => [String(source.price.id), source])),
)
const editing = ref(false)
const dirty = ref(false)
const saving = ref(false)
const resetting = ref(false)
const resetOpen = ref(false)
const resetError = ref('')
const discardOpen = ref(false)
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const body = ref<HTMLElement>()
const capabilities = computed(() =>
  Object.entries(catalog.value?.capabilities ?? {})
    .filter(([, value]) => value === true)
    .map(([key]) => key),
)
const knownModalities = ['audio', 'embedding', 'image', 'pdf', 'text', 'video']
const modalityText = (values: string[]) =>
  values
    .map((value) => (knownModalities.includes(value) ? t('modelManager.modality.' + value) : value))
    .join(' / ')
const busy = computed(() => saving.value || resetting.value)
useLoadingActivity(resetting)
useMessageSource(() =>
  (context.isError.value && context.data.value) ||
  (props.source && detail.isError.value && detail.data.value)
    ? { tone: 'warning', text: t('modelManager.stale') }
    : undefined,
)
onScopeDispose(() => controller.abort())
// 没有「全部来源」这一层了，未指定来源时直接落到第一个。
watch(
  [modelData, () => props.source],
  ([data, current]) => {
    if (!current && data?.sources.length) emit('select', data.sources[0]!.price.id)
  },
  { immediate: true },
)
watch(
  () => props.source,
  () => {
    editing.value = false
    resetOpen.value = false
    resetError.value = ''
    void nextTick(() => body.value?.scrollTo({ top: 0 }))
  },
)
async function close(): Promise<void> {
  if (!busy.value && (await guard.value?.confirm())) emit('close')
}
async function select(id: number): Promise<void> {
  if (busy.value || id === props.source) return
  if (!(await guard.value?.confirm())) return
  editing.value = false
  emit('select', id)
}
function cancelEdit(): void {
  if (dirty.value) discardOpen.value = true
  else editing.value = false
}
function discardEdit(): void {
  discardOpen.value = false
  editing.value = false
  dirty.value = false
}
function cancelReset(): void {
  resetOpen.value = false
  resetError.value = ''
}
function saved(updated: ModelPrice): void {
  if (detail.data.value)
    cache.setQueryData(modelSourceKey(updated.id), { ...detail.data.value, price: updated })
  dirty.value = false
  editing.value = false
  emit('changed')
}
async function reset(): Promise<void> {
  if (!price.value || busy.value) return
  resetting.value = true
  resetError.value = ''
  try {
    const updated = await resetModelPrice(client, price.value.id, controller.signal)
    if (controller.signal.aborted) return
    saved(updated)
    resetOpen.value = false
    messages.show({ tone: 'success', text: t('modelManager.resetSuccess') })
  } catch {
    if (!controller.signal.aborted) resetError.value = t('modelManager.resetFailed')
  } finally {
    resetting.value = false
  }
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      placement="editor"
      :title="model"
      :description="t('modelManager.detailDescription')"
      @escape-key-down="
        (event: Event) => {
          if (busy) event.preventDefault()
        }
      "
      @interact-outside="
        (event: Event) => {
          if (busy) event.preventDefault()
        }
      "
    >
      <AppDialogHeader
        class="modern-model-detail-header"
        :title="editing ? t('modelManager.edit') : model"
        :description="editing ? model : undefined"
        :close-label="t('ui.close')"
        :close-disabled="busy"
        @close="close"
      />
      <ModelPriceEditor
        v-if="editing && price"
        :key="price.id"
        :price="price"
        :model-count="detail.data.value?.modelCount ?? 0"
        :group-count="detail.data.value?.groupCount ?? 0"
        @dirty="dirty = $event"
        @pending="saving = $event"
        @saved="saved"
        @cancel="cancelEdit"
      />
      <template v-else>
        <div ref="body" class="modern-model-detail-body">
          <AppCollectionState
            v-if="!modelData && context.isPending.value"
            :title="t('modelManager.loading')"
            loading
          />
          <AppCollectionState
            v-else-if="!modelData && context.isError.value"
            :title="t('modelManager.failed')"
            error
          >
            <AppButton @click="context.refetch()">{{ t('ui.retry') }}</AppButton>
          </AppCollectionState>
          <AppCollectionState
            v-else-if="!modelData"
            :icon="Boxes"
            :title="t('modelManager.missing')"
          />
          <template v-else>
            <div class="modern-model-detail-navigation">
              <AppSearchSelect
                v-if="modelData.sources.length > 1"
                :model-value="chosen ? String(source) : ''"
                :options="options"
                :label="t('modelManager.chooseSource')"
                :placeholder="t('modelManager.chooseSource')"
                label-hidden
                size="sm"
                class="modern-model-detail-source-select"
                @update:model-value="select(Number($event))"
              >
                <template #option="{ option }">
                  <AppChannelIcon
                    v-if="sourceMap.has(option.value)"
                    :icon="sourceMap.get(option.value)!.price.channel.icon"
                    :mark="sourceMap.get(option.value)!.price.channel.mark"
                    :name="sourceMap.get(option.value)!.price.channel.name"
                    :tooltip="false"
                    size="sm"
                  />
                  <span>{{ option.label }}</span>
                </template>
              </AppSearchSelect>
              <AppCopyValue
                v-if="chosen && modelData.sources.length > 1"
                :value="chosen.model"
                display=""
                :label="t('modelManager.copyUpstream')"
              />
              <div
                v-if="chosen && modelData.sources.length === 1"
                class="modern-model-detail-identity"
              >
                <AppChannelIcon
                  :icon="chosen.price.channel.icon"
                  :mark="chosen.price.channel.mark"
                  :name="chosen.price.channel.name"
                  :tooltip="false"
                  size="md"
                  surface
                />
                <div>
                  <strong>{{ chosen.price.channel.name || t('logs.deleted') }}</strong>
                  <AppCopyValue :value="chosen.model" :label="t('modelManager.copyUpstream')" />
                </div>
              </div>
            </div>
            <AppCollectionState
              v-if="!chosen && !source"
              :title="t('modelManager.loading')"
              loading
            />
            <AppCollectionState
              v-else-if="!chosen"
              :icon="Boxes"
              :title="t('modelManager.sourceMissing')"
            />
            <AppCollectionState
              v-else-if="admin && detail.isPending.value"
              :title="t('modelManager.loading')"
              loading
            />
            <AppCollectionState v-else-if="!price" :title="t('modelManager.failed')" error>
              <AppButton @click="detail.refetch()">{{ t('ui.retry') }}</AppButton>
            </AppCollectionState>
            <template v-else>
              <AppFormSection :title="t('modelManager.pricing')" compact>
                <template #actions>
                  <AppBadge
                    :tone="price.status === 'pending' ? 'warning' : 'neutral'"
                    size="xs"
                    variant="plain"
                    >{{ t('modelManager.priceMethods.' + priceStatus(price)) }}</AppBadge
                  >
                </template>
                <ModelPricingDetails :price="price" />
              </AppFormSection>
              <AppFormSection v-if="catalog" :title="t('modelManager.catalog')" compact>
                <div class="modern-model-catalog-origin">
                  <span>{{
                    t(
                      catalog.source === 'actual_provider'
                        ? 'modelManager.actualProvider'
                        : 'modelManager.referenceProvider',
                    )
                  }}</span>
                  <strong>{{ catalog.provider }} · {{ catalog.name }}</strong>
                </div>
                <p v-if="catalog.description" class="modern-model-catalog-description">
                  {{ catalog.description }}
                </p>
                <dl class="modern-model-catalog-facts">
                  <template v-for="(value, key) in catalog.limits" :key="key">
                    <div v-if="value !== null">
                      <dt>{{ t('modelManager.limits.' + key) }}</dt>
                      <dd>{{ formatCompactNumber(value, locale) }}</dd>
                    </div>
                  </template>
                  <div v-if="catalog.family">
                    <dt>{{ t('modelManager.family') }}</dt>
                    <dd>{{ catalog.family }}</dd>
                  </div>
                  <div v-if="catalog.releaseDate">
                    <dt>{{ t('modelManager.released') }}</dt>
                    <dd>{{ catalog.releaseDate }}</dd>
                  </div>
                  <div v-if="catalog.knowledge">
                    <dt>{{ t('modelManager.knowledge') }}</dt>
                    <dd>{{ catalog.knowledge }}</dd>
                  </div>
                </dl>
                <p
                  v-if="catalog.modalities.input.length || catalog.modalities.output.length"
                  class="modern-model-modalities"
                >
                  <span>{{ t('modelManager.modalities') }}</span>
                  {{ modalityText(catalog.modalities.input) || '—' }} →
                  {{ modalityText(catalog.modalities.output) || '—' }}
                </p>
                <div v-if="capabilities.length || catalog.status" class="modern-model-detail-tags">
                  <AppBadge
                    v-for="capability in capabilities"
                    :key="capability"
                    size="xs"
                    tone="neutral"
                  >
                    {{ t('modelManager.capability.' + capability) }}
                  </AppBadge>
                  <AppBadge v-if="catalog.status" tone="neutral" size="xs">
                    {{
                      ['alpha', 'beta', 'deprecated'].includes(catalog.status)
                        ? t('modelManager.catalogStatus.' + catalog.status)
                        : catalog.status
                    }}
                  </AppBadge>
                </div>
              </AppFormSection>
              <AppFormSection
                v-if="admin && groups.length"
                :title="t('modelManager.relatedGroups')"
                compact
              >
                <div
                  class="modern-model-detail-groups"
                  :class="{ 'has-shared-protocols': sharedProtocols !== null }"
                >
                  <div v-for="group in groups" :key="group.id" class="modern-model-detail-group">
                    <RouterLink
                      :to="{ name: 'modern-group-detail', params: { id: String(group.id) } }"
                      class="modern-model-group-chip"
                      ><AppOverflowText :text="group.name || t('logs.deleted')"
                    /></RouterLink>
                    <AppBadge v-if="!group.enabled" size="xs" variant="plain">{{
                      t('modelManager.paused')
                    }}</AppBadge>
                    <div v-if="sharedProtocols === null" class="modern-model-detail-tags">
                      <AppProtocolTag
                        v-for="protocol in group.protocols"
                        :key="protocol"
                        :protocol="protocol"
                      />
                    </div>
                  </div>
                </div>
                <div v-if="sharedProtocols?.length" class="modern-model-detail-tags">
                  <AppProtocolTag
                    v-for="protocol in sharedProtocols"
                    :key="protocol"
                    :protocol="protocol"
                  />
                </div>
              </AppFormSection>
              <AppFormSection
                v-if="admin && relatedModels.length > 1"
                :title="t('modelManager.relatedModels')"
                compact
              >
                <div class="modern-model-detail-tags">
                  <AppBadge v-for="item in relatedModels" :key="item" size="xs" tone="neutral">
                    <AppOverflowText :text="item" />
                  </AppBadge>
                </div>
              </AppFormSection>
            </template>
          </template>
        </div>
        <footer class="modern-model-detail-footer">
          <AppButton
            v-if="admin && chosen && price?.canReset"
            variant="text"
            size="sm"
            :icon="RotateCcw"
            :disabled="busy"
            @click="resetOpen = true"
          >
            {{ t('modelManager.reset') }}
          </AppButton>
          <div>
            <AppButton size="sm" :disabled="busy" @click="close">{{ t('ui.close') }}</AppButton>
            <AppButton
              v-if="admin && chosen && price"
              size="sm"
              variant="primary"
              :icon="PencilLine"
              :disabled="busy"
              @click="editing = true"
            >
              {{ t('modelManager.edit') }}
            </AppButton>
          </div>
        </footer>
      </template>
    </AppDialogContent>
  </DialogRoot>
  <AppDraftGuard
    ref="guard"
    :dirty="dirty"
    :pending="busy"
    :query-scope="['model', 'source', 'group_status']"
  />
  <AppConfirmDialog
    :open="discardOpen"
    :title="t('groups.edit.unsaved')"
    :confirm-label="t('groups.edit.discard')"
    :cancel-label="t('groups.edit.keepEditing')"
    tone="danger"
    @confirm="discardEdit"
    @cancel="discardOpen = false"
  />
  <AppConfirmDialog
    :open="resetOpen"
    :title="t('modelManager.resetTitle')"
    :description="t('modelManager.resetDescription')"
    :subject="price?.model"
    :confirm-label="t('modelManager.reset')"
    :pending="resetting"
    :error="resetError"
    @confirm="reset"
    @cancel="cancelReset"
  />
</template>

<style scoped>
.modern-model-detail-header {
  overflow-wrap: anywhere;
}
.modern-model-detail-body {
  display: grid;
  align-content: start;
  flex: 1;
  min-height: 0;
  gap: var(--modern-space-4);
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-4);
}
.modern-model-detail-navigation {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-model-detail-source-select {
  flex: 1;
  min-width: 0;
}
.modern-model-detail-identity {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-model-detail-identity > div {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-detail-identity strong {
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  overflow-wrap: anywhere;
}
.modern-model-detail-tags {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1-5);
}
.modern-model-catalog-origin {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-1) var(--modern-space-2);
  font-size: var(--modern-font-size-small);
}
.modern-model-catalog-origin span,
.modern-model-catalog-description,
.modern-model-modalities {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-catalog-origin strong {
  font-weight: var(--modern-weight-medium);
}
.modern-model-catalog-facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  margin: 0;
  font-size: var(--modern-font-size-secondary);
}
/* 标签列定宽右对齐：auto 会让每行按各自内容算宽，行与行对不齐。 */
.modern-model-catalog-facts > div {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  align-items: baseline;
  gap: var(--modern-space-2);
}
.modern-model-catalog-facts dt {
  overflow-wrap: break-word;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  text-align: right;
}
.modern-model-catalog-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.modern-model-modalities span {
  margin-right: var(--modern-space-2);
}
.modern-model-detail-group {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  padding-block: var(--modern-space-1);
}
.modern-model-detail-groups:not(.has-shared-protocols)
  .modern-model-detail-group
  + .modern-model-detail-group {
  margin-top: var(--modern-space-2);
  padding-top: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-detail-groups.has-shared-protocols {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-1) var(--modern-space-2);
}
.modern-model-detail-group > :first-child {
  min-width: 0;
  max-width: 100%;
}
/* 详情里分组是独立段落，比表格行里的标签放大一档。 */
.modern-model-detail-group .modern-model-group-chip {
  --modern-model-chip-height: var(--modern-badge-sm);
}
.modern-model-detail-footer {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3) var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-detail-footer > div {
  display: flex;
  gap: var(--modern-space-2);
  margin-left: auto;
}
@media (max-width: 760px) {
  .modern-model-catalog-facts {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
