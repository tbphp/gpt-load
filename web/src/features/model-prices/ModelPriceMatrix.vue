<script setup lang="ts">
import { Plus, X } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceBranchDto } from '@/app/resources/models'
import { groupDetailLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatInteger } from '@/lib/format'

import { hasWiderPriceImpact } from '../models/model-presenter'
import {
  deriveOneHourCacheWrite,
  modelPriceFields,
  type ModelPriceField,
  type ModelPriceFormErrors,
  type ModelPriceDraft,
} from './model-price-form'
import ModelPriceResetDialog from './ModelPriceResetDialog.vue'

const props = defineProps<{
  branch: ModelPriceBranchDto
  errors: ModelPriceFormErrors
  pending: boolean
  changed: boolean
  canSave: boolean
  allNull: boolean
  failure: string
}>()
const emit = defineEmits<{
  'add-tier': []
  'remove-tier': [key: string]
  save: []
  cancel: []
  'confirm-unpriced': []
  'reset-completed': []
}>()
const draft = defineModel<ModelPriceDraft>('draft', { required: true })
const unpricedConfirmOpen = defineModel<boolean>('unpricedConfirmOpen', { required: true })
const { locale, t } = useI18n()

const showActions = computed(() => props.changed || props.canSave)
const tiered = computed(() => draft.value.tiers.length > 0)

function baseFieldError(field: ModelPriceField): string | undefined {
  const code = props.errors.base[field]
  return code ? t(`modelPrices.matrix.errors.${code}`) : undefined
}

function tierThresholdError(key: string): string | undefined {
  const code = props.errors.tiers[key]?.threshold
  return code ? t(`modelPrices.matrix.errors.threshold_${code}`) : undefined
}

function tierSlotError(key: string, field: ModelPriceField): string | undefined {
  const code = props.errors.tiers[key]?.slots?.[field]
  return code ? t(`modelPrices.matrix.errors.${code}`) : undefined
}

/** 输入框组共享外框，逐格挂错误文案会撑破布局；改为按行汇总去重后展示。 */
function dedupe(messages: (string | undefined)[]): string[] {
  return [...new Set(messages.filter((message): message is string => Boolean(message)))]
}

const baseErrors = computed(() => dedupe(modelPriceFields.map((field) => baseFieldError(field))))

function tierErrors(key: string): string[] {
  const messages = [
    tierThresholdError(key),
    ...modelPriceFields.map((field) => tierSlotError(key, field)),
  ]
  if (props.errors.tiers[key]?.emptyTier === true) {
    messages.push(t('modelPrices.matrix.errors.tier_empty'))
  }
  return dedupe(messages)
}

const baseOneHour = computed(() => {
  if (draft.value.tiers.length > 0) return null
  return deriveOneHourCacheWrite(draft.value.base.cache_write)
})

const oneHourFootnoteParts = computed(() => {
  if (draft.value.tiers.length === 0) return []
  const parts: string[] = []
  const base = deriveOneHourCacheWrite(draft.value.base.cache_write)
  if (base) parts.push(t('modelPrices.matrix.oneHourBasePart', { value: base }))
  for (const tier of draft.value.tiers) {
    const value = deriveOneHourCacheWrite(tier.slots.cache_write)
    const threshold = tier.threshold.trim()
    if (value && threshold !== '' && !Number.isNaN(Number(threshold))) {
      parts.push(
        t('modelPrices.matrix.oneHourTierPart', {
          threshold: formatInteger(Number(threshold), locale.value),
          value,
        }),
      )
    }
  }
  return parts
})

function methodLabel(): string {
  const { method, matched_provider_id: provider } = props.branch.price
  if (method === null) return t('modelPrices.method.pending')
  if (method === 'auto_matched') return t('modelPrices.method.auto_matched', { provider })
  return t(`modelPrices.method.${method}`)
}

function addTier(): void {
  emit('add-tier')
}
</script>

<template>
  <div class="model-price-matrix">
    <InlineFeedback v-if="failure" appearance="ledger" tone="danger">{{ failure }}</InlineFeedback>

    <div class="model-price-matrix__head">
      <h3>{{ t('modelPrices.matrix.heading') }}</h3>
      <span class="model-price-matrix__unit">{{ t('modelPrices.matrix.unit') }}</span>
    </div>

    <div class="model-price-matrix__grid" :class="{ 'model-price-matrix__grid--tiered': tiered }">
      <!-- 纯视觉列标签；每个输入的可访问名称由 AppTextInput 自带的 sr-only label 承担。 -->
      <div class="model-price-matrix__row model-price-matrix__row--header" aria-hidden="true">
        <span v-if="tiered">{{ t('modelPrices.matrix.thresholdColumn') }}</span>
        <div class="model-price-matrix__header-cells">
          <span v-for="field in modelPriceFields" :key="field">
            {{ t(`modelPrices.fields.${field}`) }}
          </span>
        </div>
        <span v-if="tiered"></span>
      </div>

      <div class="model-price-matrix__row">
        <span v-if="tiered" class="model-price-matrix__tier-label">
          {{ t('modelPrices.matrix.baseRow') }}
        </span>
        <!-- 一横排为一个输入框组：共享外框，内部竖线分隔，无间隙。 -->
        <div class="model-price-group" role="group" :aria-label="t('modelPrices.matrix.baseRow')">
          <AppTextInput
            v-for="field in modelPriceFields"
            :id="`model-price-base-${field}`"
            :key="field"
            v-model="draft.base[field]"
            :label="t(`modelPrices.fields.${field}`)"
            appearance="surface"
            size="compact"
            monospace
            inputmode="decimal"
            :disabled="pending"
            :invalid="Boolean(baseFieldError(field))"
          />
        </div>
        <span v-if="tiered" aria-hidden="true"></span>
      </div>
      <p v-if="baseErrors.length > 0" class="model-price-matrix__error" role="alert">
        {{ baseErrors.join(' · ') }}
      </p>

      <template v-for="tier in draft.tiers" :key="tier.key">
        <div class="model-price-matrix__row">
          <div class="model-price-group model-price-group--threshold">
            <AppTextInput
              :id="`model-price-tier-${tier.key}-threshold`"
              v-model="tier.threshold"
              :label="t('modelPrices.matrix.thresholdColumn')"
              appearance="surface"
              size="compact"
              monospace
              inputmode="numeric"
              :disabled="pending"
              :invalid="Boolean(tierThresholdError(tier.key))"
            />
          </div>
          <div
            class="model-price-group"
            role="group"
            :aria-label="tier.threshold.trim() || t('modelPrices.matrix.thresholdColumn')"
          >
            <AppTextInput
              v-for="field in modelPriceFields"
              :id="`model-price-tier-${tier.key}-${field}`"
              :key="field"
              v-model="tier.slots[field]"
              :label="t(`modelPrices.fields.${field}`)"
              appearance="surface"
              size="compact"
              monospace
              inputmode="decimal"
              :disabled="pending"
              :invalid="Boolean(tierSlotError(tier.key, field))"
            />
          </div>
          <IconButton
            class="model-price-matrix__remove"
            variant="ghost"
            size="xs"
            :disabled="pending"
            :label="t('modelPrices.matrix.removeTier')"
            @click="emit('remove-tier', tier.key)"
          >
            <X :size="14" aria-hidden="true" />
          </IconButton>
        </div>
        <p v-if="tierErrors(tier.key).length > 0" class="model-price-matrix__error" role="alert">
          {{ tierErrors(tier.key).join(' · ') }}
        </p>
      </template>
    </div>

    <div class="model-price-matrix__notes">
      <AppButton
        class="model-price-matrix__add"
        variant="link"
        size="inline"
        :disabled="pending"
        @click="addTier"
      >
        <Plus :size="14" aria-hidden="true" />{{ t('modelPrices.matrix.addTier') }}
      </AppButton>
      <p v-if="baseOneHour" class="model-price-matrix__note">
        {{ t('modelPrices.matrix.oneHourInline', { value: baseOneHour }) }}
      </p>
      <p v-if="oneHourFootnoteParts.length > 0" class="model-price-matrix__note">
        {{ t('modelPrices.matrix.oneHourNote', { parts: oneHourFootnoteParts.join(' · ') }) }}
      </p>
      <p v-if="tiered" class="model-price-matrix__note">
        {{ t('modelPrices.matrix.replaceNote') }}
      </p>
    </div>

    <div class="model-price-matrix__status">
      <StatusBadge
        size="compact"
        :tone="branch.price.pricing_status === 'configured' ? 'success' : 'warning'"
      >
        {{ t(`modelPrices.status.${branch.price.pricing_status}`) }}
      </StatusBadge>
      <span>{{ methodLabel() }}</span>
      <span class="model-price-matrix__groups">
        <span class="model-price-matrix__eyebrow">{{ t('models.detail.routeGroups') }}</span>
        <RouterLink
          v-for="group in branch.route_groups"
          :key="group.id"
          :to="groupDetailLocation(group.id)"
        >
          {{ group.name }}<span v-if="!group.enabled">{{ t('models.detail.groupDisabled') }}</span>
        </RouterLink>
      </span>
      <span v-if="branch.price.partial" class="model-price-matrix__status-warning">
        {{ t('modelPrices.facts.partial') }}
      </span>
      <span v-if="hasWiderPriceImpact(branch)" class="model-price-matrix__status-warning">
        {{ t('models.detail.globalImpact', { count: branch.affected_groups.length }) }}
      </span>
      <ModelPriceResetDialog
        v-if="branch.price.can_reset"
        :row="branch.price"
        action="reset"
        @completed="emit('reset-completed')"
      />
    </div>

    <div v-if="showActions" class="model-price-matrix__actions">
      <AppButton variant="secondary" size="compact" :disabled="pending" @click="emit('cancel')">
        {{ t('common.cancel') }}
      </AppButton>
      <AppButton size="compact" :busy="pending" :disabled="!canSave" @click="emit('save')">
        {{ t('modelPrices.matrix.save') }}
      </AppButton>
    </div>
  </div>

  <AppConfirmDialog
    appearance="ledger"
    tone="danger"
    :open="unpricedConfirmOpen"
    :title="t('modelPrices.matrix.unpricedConfirm.title')"
    :description="
      t('modelPrices.matrix.unpricedConfirm.description', { model: branch.price.model_id })
    "
    :close-label="t('modelPrices.matrix.unpricedConfirm.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('modelPrices.matrix.unpricedConfirm.confirm')"
    :pending="pending"
    @update:open="unpricedConfirmOpen = $event"
    @confirm="emit('confirm-unpriced')"
  >
    <InlineFeedback appearance="ledger" tone="warning">
      {{ t('modelPrices.matrix.unpricedConfirm.warning') }}
    </InlineFeedback>
  </AppConfirmDialog>
</template>

<style scoped>
.model-price-matrix {
  display: grid;
  min-width: 0;
  gap: var(--space-3-25);
}

.model-price-matrix__head {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
}

.model-price-matrix__head h3 {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 650;
}

.model-price-matrix__unit {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-matrix__grid {
  display: grid;
  min-width: 0;
  justify-content: start;
  gap: var(--space-1-75) var(--space-2);
}

/*
 * 每行外层列结构统一为 [阈值/行标签] [价格组] [删除位]，
 * 表头复用同一套列定义，内部再等分 4 格，确保标签与组内单元格对齐。
 */
.model-price-matrix__row {
  display: grid;
  min-width: 0;
  grid-template-columns: 352px;
  align-items: center;
  gap: var(--space-2);
}

.model-price-matrix__grid--tiered .model-price-matrix__row {
  grid-template-columns: 104px 352px 28px;
}

.model-price-matrix__header-cells {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  /* 对齐 AppTextInput 的 padding-left(--space-3) 与组边框 1px。 */
  padding-left: calc(var(--space-3) + 1px);
}

.model-price-matrix__row--header {
  align-items: end;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-group {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}

.model-price-group--threshold {
  grid-template-columns: minmax(0, 1fr);
}

/* 组内单元格去掉自身边框，仅保留竖线分隔，形成一体化输入框组。 */
.model-price-group :deep(.app-text-input) {
  border: 0;
  border-left: 1px solid var(--color-border-subtle);
  border-radius: 0;
  background: none;
}

.model-price-group :deep(.app-text-input:first-child) {
  border-left: 0;
}

/* 边框被移除后，用内描边表达 focus 与非法态，避免影响相邻单元格布局。 */
.model-price-group :deep(.app-text-input:focus-within) {
  outline: 2px solid var(--color-focus);
  outline-offset: -2px;
}

.model-price-group :deep(.app-text-input--invalid) {
  background: var(--color-danger-bg);
}

.model-price-matrix__error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

.model-price-matrix__tier-label {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.model-price-matrix__remove {
  color: var(--color-text-faint);
}

.model-price-matrix__remove:hover:not(:disabled) {
  color: var(--color-danger);
}

.model-price-matrix__notes {
  display: grid;
  justify-items: start;
  gap: var(--space-1);
}

.model-price-matrix__note {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-matrix__status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2-5);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3-25);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.model-price-matrix__status-warning {
  color: var(--color-warning);
}

.model-price-matrix__groups {
  display: inline-flex;
  min-width: 0;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-1-75);
}

.model-price-matrix__eyebrow {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.04em;
}

.model-price-matrix__groups a {
  color: var(--color-action);
  font-weight: 560;
}

.model-price-matrix__groups a span {
  margin-left: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
}

.model-price-matrix__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

@media (max-width: 720px) {
  .model-price-matrix__row,
  .model-price-matrix__grid--tiered .model-price-matrix__row {
    grid-template-columns: minmax(0, 1fr);
  }

  .model-price-matrix__grid--tiered .model-price-matrix__row {
    grid-template-columns: minmax(0, 1fr) 28px;
  }

  .model-price-matrix__grid--tiered
    .model-price-matrix__row
    > .model-price-group:not(:first-child) {
    grid-column: 1 / -1;
  }
}
</style>
