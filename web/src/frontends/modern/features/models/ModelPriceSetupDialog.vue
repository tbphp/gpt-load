<script setup lang="ts">
import { computed, onScopeDispose, ref } from 'vue'
import { DialogRoot } from 'reka-ui'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { ApiError, InvalidResponseError } from '@shared/http/errors'
import { useMessages } from '@modern/app/messages'
import {
  batchModelPrices,
  getPriceSources,
  type ModelPrice,
  type ModelPriceSource,
  type ModelPriceUpdate,
  type ModelSource,
} from '@modern/api/models'
import {
  AppButton,
  AppDialogContent,
  AppDialogHeader,
  AppNotice,
  AppSearchSelect,
  AppSegmentedControl,
  AppTag,
} from '@modern/components/ui'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import ModelPriceEditor from './ModelPriceEditor.vue'
import ModelPricingDetails from './ModelPricingDetails.vue'
import { modelStateKeys } from './models-state'

const props = defineProps<{ sources: ModelSource[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const messages = useMessages()
const mode = ref<'copy' | 'link'>('copy')
const selected = ref<ModelPriceSource>()
const candidates = new Map<string, ModelPriceSource>()
const hasMore = ref(false)
const edited = ref(false)
const editorPending = ref(false)
const linking = ref(false)
const pending = computed(() => editorPending.value || linking.value)
const dirty = computed(() => Boolean(selected.value) || edited.value)
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const controller = new AbortController()
const ids = computed(() => [...new Set(props.sources.map((source) => source.price.id))])
const groups = computed(
  () =>
    new Set(props.sources.flatMap((source) => source.affectedGroups.map((group) => group.id))).size,
)
const modes = computed(() => [
  { value: 'copy', label: t('modelManager.reuse.copy') },
  { value: 'link', label: t('modelManager.reuse.link') },
])
const label = (source: ModelPriceSource) => `${source.providerName} · ${source.model}`
const selectedOption = computed(() =>
  selected.value ? { value: selected.value.key, label: label(selected.value) } : undefined,
)
const preview = computed<ModelPrice | undefined>(() => {
  const target = props.sources[0]?.price
  const source = selected.value
  return target && source
    ? {
        ...target,
        prices: source.prices,
        context_tiers: source.context_tiers,
        mode_schedules: source.mode_schedules,
        method: 'user_set',
        catalogReference: null,
      }
    : undefined
})
async function options(query: string, signal: AbortSignal) {
  const result = await getPriceSources(
    client,
    mode.value,
    Array.from(query.trim()).slice(0, 200).join(''),
    signal,
  )
  if (signal.aborted) return []
  hasMore.value = result.hasMore
  for (const source of result.items) candidates.set(source.key, source)
  return result.items.map((source) => ({
    value: source.key,
    label: label(source),
    group: t('modelManager.reuse.' + source.kind),
  }))
}
async function select(key: string): Promise<void> {
  if (
    pending.value ||
    key === selected.value?.key ||
    (edited.value && !(await guard.value?.confirm(false)))
  )
    return
  selected.value = candidates.get(key)
  edited.value = false
}
async function changeMode(value: string): Promise<void> {
  if (
    pending.value ||
    value === mode.value ||
    (value !== 'copy' && value !== 'link') ||
    (edited.value && !(await guard.value?.confirm(false)))
  )
    return
  mode.value = value
  if (value === 'link' && selected.value?.kind === 'local') selected.value = undefined
  edited.value = false
}
async function close(): Promise<void> {
  if (!pending.value && (await guard.value?.confirm())) emit('close')
}
async function submitCopy(price: ModelPriceUpdate): Promise<ModelPrice> {
  const items = await batchModelPrices(
    client,
    { ids: ids.value, mode: 'copy', price },
    controller.signal,
  )
  const first = items.find((item) => item.id === ids.value[0])
  if (!first) throw new InvalidResponseError()
  return first
}
function saved(): void {
  edited.value = false
  selected.value = undefined
  emit('saved')
}
async function link(): Promise<void> {
  const source = selected.value
  if (!source || source.kind !== 'catalog' || pending.value) return
  linking.value = true
  try {
    await batchModelPrices(
      client,
      {
        ids: ids.value,
        mode: 'link',
        reference: { provider_id: source.providerId, model_id: source.model },
      },
      controller.signal,
    )
    if (!controller.signal.aborted) {
      messages.show({ tone: 'success', text: t('modelManager.saveSuccess') })
      saved()
    }
  } catch (cause) {
    if (!controller.signal.aborted)
      messages.show({
        tone: 'danger',
        text: cause instanceof ApiError ? cause.message : t('modelManager.saveFailed'),
      })
  } finally {
    linking.value = false
  }
}
onScopeDispose(() => controller.abort())
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      placement="editor"
      :title="t('modelManager.reuse.title')"
      :description="t('modelManager.reuse.replace', { count: ids.length })"
      @escape-key-down="pending && $event.preventDefault()"
      @interact-outside="pending && $event.preventDefault()"
    >
      <AppDialogHeader
        :title="t('modelManager.reuse.title')"
        :close-label="t('modelManager.reuse.close')"
        :close-disabled="pending"
        @close="close"
      />
      <div class="modern-price-setup-controls">
        <AppSegmentedControl
          :model-value="mode"
          :options="modes"
          :label="t('modelManager.reuse.title')"
          :disabled="pending"
          @update:model-value="changeMode"
        />
        <AppSearchSelect
          :key="mode"
          :model-value="selected?.key ?? ''"
          :selected-option="selectedOption"
          :label="t('modelManager.reuse.source')"
          :placeholder="t('modelManager.reuse.search')"
          :load-options="options"
          :disabled="pending"
          @update:model-value="select"
        />
        <p v-if="hasMore" class="modern-price-setup-hint">{{ t('modelManager.reuse.more') }}</p>
        <AppNotice :tone="mode === 'copy' ? 'warning' : 'info'">{{
          t('modelManager.reuse.' + (mode === 'copy' ? 'replace' : 'follow'), { count: ids.length })
        }}</AppNotice>
        <div class="modern-price-setup-targets">
          <AppTag
            v-for="source in sources"
            :key="source.price.id"
            :text="source.price.channel.name + ' · ' + source.model"
            size="xs"
          />
        </div>
        <p class="modern-price-setup-hint">
          {{ t('modelManager.reuse.impact', { count: groups }) }}
        </p>
      </div>
      <ModelPriceEditor
        v-if="preview && mode === 'copy'"
        :key="selected!.key"
        :price="preview"
        :model-count="ids.length"
        :group-count="groups"
        :submit="submitCopy"
        force-save
        @saved="saved"
        @cancel="close"
        @dirty="edited = $event"
        @pending="editorPending = $event"
      />
      <template v-else>
        <div v-if="preview" class="modern-price-setup-preview">
          <ModelPricingDetails :price="preview" />
        </div>
        <footer class="modern-price-setup-footer">
          <AppButton :disabled="pending" @click="close">{{ t('modelManager.cancel') }}</AppButton>
          <AppButton variant="primary" :disabled="!selected" :loading="linking" @click="link">{{
            t('modelManager.save')
          }}</AppButton>
        </footer>
      </template>
      <AppDraftGuard ref="guard" :dirty="dirty" :pending="pending" :query-scope="modelStateKeys" />
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-price-setup-controls {
  display: grid;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-price-setup-targets {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  max-height: 100px;
  overflow-y: auto;
}
.modern-price-setup-hint {
  margin: 0;
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-price-setup-preview {
  overflow: auto;
  padding: var(--modern-space-5);
}
.modern-price-setup-footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
  padding: var(--modern-space-4) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
</style>
