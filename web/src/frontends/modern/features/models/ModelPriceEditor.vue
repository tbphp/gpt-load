<script setup lang="ts">
import { ChevronDown, ChevronRight, Plus, Trash2 } from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { priceFields, type ModelPrice, type ModelPriceUpdate } from '@modern/api/models'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { ApiError } from '@shared/http/errors'
import { useMessages } from '@modern/app/messages'
import {
  AppButton,
  AppConfirmDialog,
  AppFormSection,
  AppIcon,
  AppIconButton,
  AppTextField,
} from '@modern/components/ui'
import {
  draftSlots,
  newPriceTier,
  priceDraft,
  priceDraftErrors,
  priceDraftRequest,
} from './model-price-draft'
import ModelPriceTierEditor from './ModelPriceTierEditor.vue'

const props = defineProps<{
  price?: ModelPrice
  disabled: boolean
  targetCount: number
  submit: (request: ModelPriceUpdate) => Promise<ModelPrice>
}>()
const emit = defineEmits<{
  saved: [price: ModelPrice]
  dirty: [value: boolean]
  pending: [value: boolean]
}>()
const { t, n } = useI18n()
const messages = useMessages()
const draft = ref(priceDraft(props.price))
const baseline = ref(JSON.stringify(draft.value))
const standard = computed(() => draft.value.find((schedule) => schedule.mode === 'standard')!)
const schedules = computed(() => draft.value.filter((schedule) => schedule.mode !== 'standard'))
const tiersOpen = ref(standard.value.tiers.length > 0)
const tiersId = useId()
const modeCount = computed(() => schedules.value.length)
const modesOpen = ref(modeCount.value > 0)
const modesId = useId()
const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value)
const pending = ref(false)
const submitted = ref(false)
const unpriced = ref(false)
const errors = computed(() => priceDraftErrors(draft.value))
const request = computed(() => priceDraftRequest(draft.value))
const error = (key: string) =>
  submitted.value && errors.value[key] ? t('modelManager.' + errors.value[key]) : undefined
// standard 固定存在；Fast 和 Ultrafast 均可独立配置价格。
const optionalModes = ['fast', 'ultrafast'] as const
const modeLabel = (mode: string) =>
  ['standard', 'fast', 'ultrafast'].includes(mode) ? t('modelManager.' + mode) : mode
const controller = new AbortController()
const body = ref<HTMLElement>()
useLoadingActivity(pending)

watch(dirty, (value) => emit('dirty', value), { immediate: true })
watch(pending, (value) => emit('pending', value), { flush: 'sync' })
onScopeDispose(() => {
  controller.abort()
  emit('dirty', false)
  emit('pending', false)
})
async function save(confirmed = false): Promise<void> {
  if (pending.value || props.disabled) return
  submitted.value = true
  if (Object.keys(errors.value).length) {
    tiersOpen.value = true
    modesOpen.value = true
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
    const price = await props.submit(request.value)
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
      <AppFormSection
        :title="t('modelManager.basePrices')"
        :hint="t('modelManager.emptySlotsHint')"
        compact
      >
        <template #actions
          ><span class="modern-model-price-unit">{{ t('modelManager.unit') }}</span></template
        >
        <div class="modern-model-price-inputs">
          <AppTextField
            v-for="field in priceFields"
            :key="field"
            v-model="standard.prices[field]"
            :label="t('modelManager.slots.' + field)"
            :error="error('standard.' + field)"
            :disabled="pending"
            :placeholder="t('modelManager.emptySlot')"
            inputmode="decimal"
            size="sm"
          />
        </div>
      </AppFormSection>

      <section class="modern-model-price-section">
        <AppButton
          class="modern-model-price-section-toggle"
          variant="ghost"
          size="sm"
          :disabled="pending"
          :aria-expanded="tiersOpen"
          :aria-controls="tiersId"
          @click="tiersOpen = !tiersOpen"
        >
          <AppIcon :icon="tiersOpen ? ChevronDown : ChevronRight" size="xs" />
          <span>{{ t('modelManager.reuse.tierPrices') }}</span>
          <small v-if="standard.tiers.length">{{
            t('modelManager.tierCount', { count: standard.tiers.length })
          }}</small>
        </AppButton>
        <div v-show="tiersOpen" :id="tiersId" class="modern-model-price-section-body">
          <ModelPriceTierEditor
            v-for="(tier, index) in standard.tiers"
            :key="tier.key"
            :tier="tier"
            :pending="pending"
            :error="error"
            @update:tier="standard.tiers[index] = $event"
            @remove="standard.tiers = standard.tiers.filter((item) => item.key !== tier.key)"
          />
          <div class="modern-model-price-add">
            <AppButton
              variant="text"
              size="xs"
              :icon="Plus"
              :disabled="pending"
              @click="standard.tiers.push(newPriceTier())"
              >{{ t('modelManager.addTier') }}</AppButton
            >
          </div>
        </div>
      </section>

      <section class="modern-model-price-section">
        <AppButton
          class="modern-model-price-section-toggle"
          variant="ghost"
          size="sm"
          :disabled="pending"
          :aria-expanded="modesOpen"
          :aria-controls="modesId"
          @click="modesOpen = !modesOpen"
        >
          <AppIcon :icon="modesOpen ? ChevronDown : ChevronRight" size="xs" />
          <span>{{ t('modelManager.reuse.modePrices') }}</span>
          <small>{{
            modeCount ? t('modelManager.reuse.modeCount', { count: modeCount }) : 'Fast / Ultrafast'
          }}</small>
        </AppButton>
        <div v-show="modesOpen" :id="modesId" class="modern-model-price-section-body">
          <AppFormSection
            v-for="schedule in schedules"
            :key="schedule.mode"
            :title="modeLabel(schedule.mode)"
            compact
          >
            <template #actions>
              <AppButton
                v-if="schedule.mode !== 'fast'"
                variant="text"
                size="xs"
                :icon="Plus"
                :disabled="pending"
                @click="schedule.tiers.push(newPriceTier())"
                >{{ t('modelManager.addTier') }}</AppButton
              >
              <AppIconButton
                :icon="Trash2"
                :label="t('modelManager.removeMode')"
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
            <ModelPriceTierEditor
              v-for="(tier, index) in schedule.tiers"
              :key="tier.key"
              :tier="tier"
              :pending="pending"
              :error="error"
              @update:tier="schedule.tiers[index] = $event"
              @remove="schedule.tiers = schedule.tiers.filter((item) => item.key !== tier.key)"
            />
          </AppFormSection>
          <div
            v-if="optionalModes.some((mode) => !draft.some((row) => row.mode === mode))"
            class="modern-model-price-add"
          >
            <AppButton
              v-for="mode in optionalModes.filter(
                (item) => !draft.some((row) => row.mode === item),
              )"
              :key="mode"
              variant="text"
              size="xs"
              :icon="Plus"
              :disabled="pending"
              @click="draft.push({ mode, prices: draftSlots(), tiers: [] })"
              >{{ t('modelManager.' + mode) }}</AppButton
            >
          </div>
        </div>
      </section>
    </div>
    <AppConfirmDialog
      :open="unpriced"
      :title="t('modelManager.unpricedTitle', { count: n(targetCount) })"
      :description="t('modelManager.unpricedDescription')"
      :confirm-label="t('modelManager.confirmUnpriced')"
      :pending="pending"
      @confirm="save(true)"
      @cancel="unpriced = false"
    />
  </form>
</template>

<style scoped>
.modern-model-price-editor {
  container: price-editor / inline-size;
}
.modern-model-price-editor-body {
  display: grid;
  align-content: start;
  gap: var(--modern-space-3);
}
.modern-model-price-section {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-1);
}
.modern-model-price-section-toggle {
  width: 100%;
  justify-content: flex-start;
  padding-inline: 0;
  font-weight: var(--modern-weight-medium);
}
.modern-model-price-section-toggle small {
  margin-left: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-model-price-section-body {
  display: grid;
  gap: var(--modern-space-3);
  padding-top: var(--modern-space-2);
}
.modern-model-price-unit {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-add {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
}
.modern-model-price-inputs {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-2);
}
.modern-model-price-error {
  margin: 0;
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}
@container price-editor (max-width: 400px) {
  .modern-model-price-inputs {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
