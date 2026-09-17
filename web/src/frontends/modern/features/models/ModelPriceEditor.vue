<script setup lang="ts">
import { Info, Plus, Trash2 } from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { priceFields, saveModelPrice, type ModelPrice } from '@modern/api/models'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { useMessages } from '@modern/app/messages'
import {
  AppButton,
  AppChannelIcon,
  AppConfirmDialog,
  AppCopyValue,
  AppFormSection,
  AppIcon,
  AppIconButton,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import {
  draftSlots,
  newPriceTier,
  priceDraft,
  priceDraftErrors,
  priceDraftRequest,
} from './model-price-draft'

const props = defineProps<{ price: ModelPrice; modelCount: number; groupCount: number }>()
const emit = defineEmits<{
  saved: [price: ModelPrice]
  cancel: []
  dirty: [value: boolean]
  pending: [value: boolean]
}>()
const { t, n } = useI18n()
const client = useApiClient()
const messages = useMessages()
const draft = ref(priceDraft(props.price))
const baseline = ref(JSON.stringify(draft.value))
const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value)
const pending = ref(false)
const submitted = ref(false)
const unpriced = ref(false)
const errors = computed(() => priceDraftErrors(draft.value))
const request = computed(() => priceDraftRequest(draft.value))
const ownershipIntent = computed(
  () => request.value.confirm_unpriced && props.price.method !== 'user_marked_unpriced',
)
const canSave = computed(() => dirty.value || ownershipIntent.value)
const error = (key: string) =>
  submitted.value && errors.value[key] ? t('modelManager.' + errors.value[key]) : undefined
// standard 固定存在；这两档后端一视同仁，前端也对称提供。
const optionalModes = ['fast', 'ultrafast'] as const
const modeLabel = (mode: string) =>
  ['standard', 'fast', 'ultrafast'].includes(mode) ? t('modelManager.' + mode) : mode
const controller = new AbortController()
const body = ref<HTMLElement>()
useLoadingActivity(pending)

watch(dirty, (value) => emit('dirty', value), { immediate: true })
watch(pending, (value) => emit('pending', value), { flush: 'sync' })
watch(
  () => props.price,
  (price) => {
    if (dirty.value || pending.value) return
    draft.value = priceDraft(price)
    baseline.value = JSON.stringify(draft.value)
  },
)
onScopeDispose(() => {
  controller.abort()
  emit('dirty', false)
  emit('pending', false)
})
async function save(confirmed = false): Promise<void> {
  if (pending.value || !canSave.value) return
  submitted.value = true
  if (Object.keys(errors.value).length) {
    await nextTick()
    body.value?.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus()
    return
  }
  if (request.value.confirm_unpriced && !confirmed) {
    unpriced.value = true
    return
  }
  pending.value = true
  try {
    const price = await saveModelPrice(client, props.price.id, request.value, controller.signal)
    if (controller.signal.aborted) return
    baseline.value = JSON.stringify(draft.value)
    emit('dirty', false)
    unpriced.value = false
    messages.show({ tone: 'success', text: t('modelManager.saveSuccess') })
    emit('saved', price)
  } catch (cause) {
    if (!controller.signal.aborted)
      messages.show({
        tone: 'danger',
        text: cause instanceof ApiError ? cause.message : t('modelManager.saveFailed'),
      })
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <form class="modern-model-price-editor" novalidate @submit.prevent="save()">
    <div ref="body" class="modern-model-price-editor-body">
      <div class="modern-model-price-editor-identity">
        <AppChannelIcon
          :icon="price.channel.icon"
          :mark="price.channel.mark"
          :name="price.channel.name"
          :tooltip="false"
          size="md"
          surface
        />
        <div>
          <span>{{ price.channel.name || t('logs.deleted') }}</span>
          <h3><AppCopyValue :value="price.model" /></h3>
        </div>
      </div>
      <p class="modern-model-price-impact">
        {{ t('modelManager.priceImpact', { models: n(modelCount), groups: n(groupCount) }) }}
      </p>
      <div class="modern-model-price-editor-hint">
        <span>{{ t('modelManager.unit') }}</span>
        <AppTooltip :label="t('modelManager.emptySlotsHint')">
          <span tabindex="0" class="modern-model-price-help"
            ><AppIcon :icon="Info" size="sm"
          /></span>
        </AppTooltip>
      </div>
      <AppFormSection
        v-for="schedule in draft"
        :key="schedule.mode"
        :title="modeLabel(schedule.mode)"
        compact
      >
        <template #actions>
          <!-- 阶梯价只在标准档提供：Fast 后端直接驳回，Ultrafast 统一不做。 -->
          <span v-if="schedule.mode !== 'standard'" class="modern-model-price-note">
            {{ t('modelManager.noTierMode') }}
          </span>
          <AppButton
            v-else
            variant="text"
            size="xs"
            :icon="Plus"
            :disabled="pending"
            @click="schedule.tiers.push(newPriceTier())"
          >
            {{ t('modelManager.addTier') }}
          </AppButton>
          <AppIconButton
            v-if="schedule.mode !== 'standard'"
            :icon="Trash2"
            :label="t('modelManager.removeMode')"
            :tooltip="true"
            size="xs"
            :disabled="pending"
            @click="draft = draft.filter((item) => item !== schedule)"
          />
        </template>
        <div class="modern-model-price-inputs">
          <AppTextField
            v-for="field in priceFields"
            :key="field"
            v-model="schedule.prices[field]"
            :label="t('modelManager.slots.' + field)"
            :error="error(schedule.mode + '.' + field)"
            :disabled="pending"
            :placeholder="t('modelManager.emptySlot')"
            inputmode="decimal"
            size="sm"
          />
        </div>
        <p v-if="error(schedule.mode)" class="modern-model-price-error">
          {{ error(schedule.mode) }}
        </p>
        <div v-for="tier in schedule.tiers" :key="tier.key" class="modern-model-price-tier-editor">
          <header>
            <AppTextField
              v-model="tier.threshold"
              :label="t('modelManager.threshold')"
              :error="error(tier.key)"
              :disabled="pending"
              size="sm"
              inputmode="numeric"
            />
            <AppIconButton
              :icon="Trash2"
              :label="t('modelManager.removeTier')"
              :tooltip="true"
              size="xs"
              :disabled="pending"
              @click="schedule.tiers = schedule.tiers.filter((item) => item.key !== tier.key)"
            />
          </header>
          <div class="modern-model-price-inputs">
            <AppTextField
              v-for="field in priceFields"
              :key="field"
              v-model="tier.prices[field]"
              :label="t('modelManager.slots.' + field)"
              :error="error(tier.key + '.' + field)"
              :disabled="pending"
              :placeholder="t('modelManager.emptySlot')"
              size="sm"
              inputmode="decimal"
            />
          </div>
          <p v-if="error(tier.key + '.prices')" class="modern-model-price-error">
            {{ error(tier.key + '.prices') }}
          </p>
        </div>
      </AppFormSection>
      <AppButton
        v-for="mode in optionalModes.filter((item) => !draft.some((row) => row.mode === item))"
        :key="mode"
        variant="ghost"
        size="sm"
        :icon="Plus"
        :disabled="pending"
        @click="draft.push({ mode, prices: draftSlots(), tiers: [] })"
      >
        {{ t('modelManager.addMode') }} · {{ t('modelManager.' + mode) }}
      </AppButton>
    </div>
    <footer class="modern-model-price-editor-footer">
      <AppButton size="sm" :disabled="pending" @click="$emit('cancel')">{{
        t('modelManager.cancel')
      }}</AppButton>
      <AppButton
        size="sm"
        type="submit"
        variant="primary"
        :disabled="!canSave"
        :loading="pending"
        >{{ t('modelManager.save') }}</AppButton
      >
    </footer>
    <AppConfirmDialog
      :open="unpriced"
      :title="t('modelManager.unpricedTitle')"
      :description="t('modelManager.unpricedDescription')"
      :confirm-label="t('modelManager.confirmUnpriced')"
      :pending="pending"
      @confirm="save(true)"
      @cancel="unpriced = false"
    />
  </form>
</template>

<style scoped>
.modern-model-price-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-model-price-editor {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-model-price-editor-body {
  display: grid;
  align-content: start;
  gap: var(--modern-space-4);
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-5);
}
.modern-model-price-impact,
.modern-model-price-editor-hint {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-editor-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-model-price-editor-identity > div {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-model-price-editor-identity > div > span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-editor-identity h3 {
  min-width: 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-model-price-editor-hint {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-model-price-help {
  display: inline-flex;
  border-radius: var(--modern-radius-small);
}
.modern-model-price-inputs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-model-price-tier-editor {
  display: grid;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
}
.modern-model-price-tier-editor header {
  display: flex;
  align-items: flex-end;
  gap: var(--modern-space-3);
}
.modern-model-price-tier-editor header > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-model-price-error {
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-editor-footer {
  display: flex;
  flex: none;
  justify-content: flex-end;
  gap: var(--modern-space-2);
  padding: var(--modern-space-4) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
</style>
