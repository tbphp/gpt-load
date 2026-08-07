<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceBranchDto, ModelUpstreamDto } from '@/app/resources/models'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import ModelPriceMatrix from '@/features/model-prices/ModelPriceMatrix.vue'
import { useModelPriceEditor } from '@/features/model-prices/use-model-price-editor'

import ModelSpecSheet from './ModelSpecSheet.vue'

const props = defineProps<{
  upstream: ModelUpstreamDto
}>()
const { t } = useI18n()

/** 只包含本上游模型的价格；作用域切换不跨上游。 */
const branches = computed<ModelPriceBranchDto[]>(() => props.upstream.prices)
const selectedPriceId = ref<number | null>(branches.value[0]?.price.id ?? null)

watch(branches, (list) => {
  if (list.some((branch) => branch.price.id === selectedPriceId.value)) return
  selectedPriceId.value = list[0]?.price.id ?? null
})

const selectedBranch = computed<ModelPriceBranchDto>(() => {
  const found = branches.value.find((branch) => branch.price.id === selectedPriceId.value)
  return found ?? (branches.value[0] as ModelPriceBranchDto)
})

const scopeOptions = computed(() =>
  branches.value.map((branch) => ({
    value: String(branch.price.id),
    label: t('models.inspector.scopeOption', {
      label: branch.price.scope.label,
      kind: t(`modelPrices.scope.${branch.price.scope.kind}`),
    }),
  })),
)

const priceRow = computed(() => selectedBranch.value.price)
const editor = useModelPriceEditor(priceRow)

async function selectScope(value: string): Promise<void> {
  const id = Number(value)
  if (id === selectedPriceId.value) return
  if (!(await editor.confirmDiscardSwitch())) return
  selectedPriceId.value = id
}

async function confirmDiscardSwitch(): Promise<boolean> {
  return editor.confirmDiscardSwitch()
}

defineExpose({ confirmDiscardSwitch })
</script>

<template>
  <section class="model-upstream">
    <ModelSpecSheet v-if="upstream.catalog_summary" :reference="upstream.catalog_summary" />
    <p v-else class="model-upstream__no-catalog">{{ t('models.inspector.noCatalog') }}</p>

    <div class="model-upstream__scope">
      <span class="model-upstream__eyebrow">{{ t('modelPrices.matrix.scopeLabel') }}</span>
      <SegmentedControl
        size="compact"
        appearance="drawer"
        scrollable
        :label="t('modelPrices.matrix.scopeLabel')"
        :model-value="String(selectedPriceId)"
        :options="scopeOptions"
        @update:model-value="selectScope"
      />
    </div>

    <ModelPriceMatrix
      v-model:draft="editor.draft.value"
      v-model:unpriced-confirm-open="editor.unpricedConfirmOpen.value"
      :branch="selectedBranch"
      :errors="editor.errors.value"
      :pending="editor.pending.value"
      :changed="editor.changed.value"
      :can-save="editor.canSave.value"
      :all-null="editor.allNull.value"
      :failure="editor.failure.value"
      @add-tier="editor.addTier"
      @remove-tier="editor.removeTier"
      @save="editor.requestSave"
      @cancel="editor.cancel"
      @confirm-unpriced="editor.confirmUnpricedSave"
      @reset-completed="editor.cancel"
    />
  </section>
</template>

<style scoped>
/* 上游标识由外层 tab 栏承担，此处只负责该上游的规格与价格。 */
.model-upstream {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
  padding-top: var(--space-4);
}

.model-upstream__eyebrow {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.04em;
}

.model-upstream__no-catalog {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-upstream__scope {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  padding-top: var(--space-1);
}
</style>
