<script setup lang="ts">
import { Plus, X } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import { formatInteger } from '@/lib/format'

import {
  modelPriceFields,
  tierDisplayOrder,
  type ModelPriceField,
  type ModelPriceScheduleErrors,
  type ModelPriceScheduleDraft,
} from './model-price-form'

const props = defineProps<{
  modelId: string
  errors: ModelPriceScheduleErrors
  pending: boolean
  failure: string
}>()
const emit = defineEmits<{
  'add-tier': []
  'remove-tier': [key: string]
  'confirm-unpriced': []
}>()
const draft = defineModel<ModelPriceScheduleDraft>('draft', { required: true })
const unpricedConfirmOpen = defineModel<boolean>('unpricedConfirmOpen', { required: true })
const { locale, t } = useI18n()

const tiered = computed(() => draft.value.tiers.length > 0)

/**
 * 新增档位固定追加到末尾，用户填入的阈值可能比上面已有档位更小，
 * 导致编辑区行序与下方按阈值排序的派生说明相互矛盾。失焦时才重排，
 * 避免每次按键都重排导致正在编辑的行跳动。
 */
function reorderTiers(): void {
  draft.value.tiers = [...draft.value.tiers].sort(
    (left, right) => tierDisplayOrder(left.threshold) - tierDisplayOrder(right.threshold),
  )
}

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

interface TierRule {
  key: string
  description: string
}

function thresholdDescription(value: string): string {
  const threshold = tierDisplayOrder(value)
  if (Number.isFinite(threshold)) return formatInteger(threshold, locale.value)
  return value.trim() || t('modelPrices.matrix.thresholdNotSet')
}

const tierRules = computed<TierRule[]>(() => {
  return draft.value.tiers.map((tier, index) => ({
    key: tier.key,
    description: t('modelPrices.matrix.tierRule', {
      threshold: thresholdDescription(tier.threshold),
      tier: index + 1,
    }),
  }))
})
</script>

<template>
  <div class="model-price-matrix">
    <InlineFeedback v-if="failure" appearance="ledger" tone="danger">{{ failure }}</InlineFeedback>

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
        <div v-if="tiered" class="model-price-group model-price-group--threshold">
          <AppTextInput
            id="model-price-default"
            :model-value="t('modelPrices.matrix.baseRow')"
            :label="t('modelPrices.matrix.baseRow')"
            appearance="surface"
            size="compact"
            readonly
          />
        </div>
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
        <!-- 增删都落在同一列：无档位时基础行就是最后一行，由它承载加号。 -->
        <div class="model-price-matrix__actions">
          <IconButton
            v-if="!tiered"
            class="model-price-matrix__add"
            variant="ghost"
            size="xs"
            :disabled="pending"
            :label="t('modelPrices.matrix.addTier')"
            @click="emit('add-tier')"
          >
            <Plus :size="14" aria-hidden="true" />
          </IconButton>
        </div>
      </div>
      <p v-if="baseErrors.length > 0" class="model-price-matrix__error" role="alert">
        {{ baseErrors.join(' · ') }}
      </p>

      <template v-for="(tier, index) in draft.tiers" :key="tier.key">
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
              @blur="reorderTiers"
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
          <div class="model-price-matrix__actions">
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
            <IconButton
              v-if="index === draft.tiers.length - 1"
              class="model-price-matrix__add"
              variant="ghost"
              size="xs"
              :disabled="pending"
              :label="t('modelPrices.matrix.addTier')"
              @click="emit('add-tier')"
            >
              <Plus :size="14" aria-hidden="true" />
            </IconButton>
          </div>
        </div>
        <p v-if="tierErrors(tier.key).length > 0" class="model-price-matrix__error" role="alert">
          {{ tierErrors(tier.key).join(' · ') }}
        </p>
      </template>
    </div>

    <div class="model-price-matrix__rules">
      <p class="model-price-matrix__cache-rule">{{ t('modelPrices.matrix.oneHourRule') }}</p>
      <ul v-if="tierRules.length > 0" class="model-price-matrix__tier-rules">
        <li v-for="rule in tierRules" :key="rule.key">{{ rule.description }}</li>
      </ul>
    </div>
  </div>

  <AppConfirmDialog
    appearance="ledger"
    tone="danger"
    :open="unpricedConfirmOpen"
    :title="t('modelPrices.matrix.unpricedConfirm.title')"
    :description="t('modelPrices.matrix.unpricedConfirm.description', { model: modelId })"
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
  gap: var(--space-2-5);
}

.model-price-matrix__grid {
  display: grid;
  min-width: 0;
  gap: var(--space-1-75) var(--space-2);
}

/*
 * 每行外层列结构统一为 [阈值/行标签] [价格组] [增删位]，
 * 表头复用同一套列定义，内部再等分 4 格，确保标签与组内单元格对齐。
 * 增删列常驻，让加号和叉号落在同一竖线上。
 */
.model-price-matrix__row {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) var(--control-xs);
  align-items: center;
  gap: var(--space-1-75);
}

.model-price-matrix__grid--tiered .model-price-matrix__row {
  grid-template-columns: 76px minmax(0, 1fr) calc(var(--control-xs) * 2);
}

.model-price-matrix__header-cells {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  /* 对齐 AppTextInput 的 padding-left 与组边框 1px。 */
  padding-left: calc(var(--space-2) + 1px);
}

/* 固定两个槽位：删除永远占第一格，新增永远占第二格，各行按钮才能纵向对齐。 */
.model-price-matrix__actions {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: var(--control-xs);
  align-items: center;
  justify-content: start;
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

.model-price-group:focus-within {
  border-color: var(--color-focus);
  outline: 0;
  box-shadow: var(--focus-ring);
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
  /* 抽屉只有 480px，四格并排时收紧内边距换取数字可见宽度。 */
  padding-left: var(--space-2);
}

.model-price-group :deep(.app-text-input:first-child) {
  border-left: 0;
}

/* 组合控件统一由外框表达焦点，子输入仅保留单元格分隔线。 */
.model-price-group :deep(.app-text-input[data-input-shell]:focus-within) {
  border-color: var(--color-border-subtle);
  outline: 0;
  box-shadow: none;
}

.model-price-group :deep(.app-text-input--invalid) {
  background: var(--color-danger-bg);
}

.model-price-matrix__error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

.model-price-matrix__remove,
.model-price-matrix__add {
  color: var(--color-text-faint);
}

.model-price-matrix__remove:hover:not(:disabled) {
  color: var(--color-danger);
}

.model-price-matrix__add:hover:not(:disabled) {
  color: var(--color-action);
}

.model-price-matrix__rules {
  display: grid;
  gap: var(--space-1);
  font-size: var(--text-label-xs);
}

.model-price-matrix__cache-rule {
  margin: 0;
  color: var(--color-text-muted);
}

.model-price-matrix__tier-rules {
  display: grid;
  gap: var(--space-0-75);
  margin: 0;
  padding: 0;
  color: var(--color-text-faint);
  list-style: none;
}

.model-price-matrix__tier-rules li {
  line-height: 1.55;
}

/* 抽屉在小屏铺满视口，四格并排放不下：阈值与增删占第一行，价格组换到第二行。 */
@media (max-width: 560px) {
  .model-price-matrix__row,
  .model-price-matrix__grid--tiered .model-price-matrix__row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .model-price-matrix__row--header {
    display: none;
  }

  .model-price-group {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .model-price-group--threshold {
    grid-template-columns: minmax(0, 1fr);
  }

  .model-price-matrix__grid--tiered
    .model-price-matrix__row
    > .model-price-group:not(.model-price-group--threshold) {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .model-price-matrix__grid--tiered .model-price-matrix__row > .model-price-group--threshold {
    grid-column: 1;
    grid-row: 1;
  }

  .model-price-matrix__grid--tiered .model-price-matrix__row > .model-price-matrix__actions {
    grid-column: 2;
    grid-row: 1;
  }
}
</style>
