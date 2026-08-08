<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, toRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { getUpstreamModelDetail, type UpstreamModelDetailDto } from '@/app/resources/models'
import { controlQueryKeys } from '@/app/query-keys'
import { groupDetailLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import ModelPriceMatrix from '@/features/model-prices/ModelPriceMatrix.vue'
import ModelPriceResetDialog from '@/features/model-prices/ModelPriceResetDialog.vue'
import { useModelPriceEditor } from '@/features/model-prices/use-model-price-editor'

import ModelPriceStatusBadge from './ModelPriceStatusBadge.vue'
import ModelSpecSheet from './ModelSpecSheet.vue'

const props = defineProps<{
  open: boolean
  priceId: number | null
}>()
const emit = defineEmits<{ close: [] }>()
const client = useApiClient()
const { locale, t } = useI18n()

const detailQuery = useQuery({
  queryKey: computed(() => controlQueryKeys.models.detail(props.priceId ?? 0)),
  queryFn: ({ signal }) => getUpstreamModelDetail(client, props.priceId as number, signal),
  enabled: computed(() => props.open && props.priceId !== null),
})
const initialLoading = useStableLoading(() => props.open && detailQuery.isPending.value)
const detail = computed(() => detailQuery.data.value)

/**
 * 编辑器需要一个稳定的价格引用；详情未就绪时用占位行，
 * 待数据到达后 useModelPriceEditor 内部的 watch 会按 id 重建草稿。
 */
const placeholderPrice: UpstreamModelDetailDto['price'] = {
  id: 0,
  model_id: '',
  prices: { input: null, output: null, cache_read: null, cache_write: null },
  pricing_status: 'pending',
  method: null,
  matched_provider_id: null,
  referenced: false,
  reference_count: 0,
  reference_group_count: 0,
  context_tiers: [],
  partial: false,
  updated_at_ms: 0,
  can_reset: false,
  can_delete: false,
}
const price = computed(() => detail.value?.price ?? placeholderPrice)
const editor = useModelPriceEditor(toRef(price))
const resetting = ref(false)

/** 客户端模型与分组分开列出：前者只是影响面，后者可以跳转。 */
const clientModels = computed(() => {
  return [...new Set((detail.value?.associations ?? []).map(({ client_model }) => client_model))]
})
const groups = computed(() => {
  const seen = new Map<number, UpstreamModelDetailDto['associations'][number]['group']>()
  for (const { group } of detail.value?.associations ?? []) seen.set(group.id, group)
  return [...seen.values()]
})

async function requestClose(): Promise<void> {
  if (!(await editor.confirmDiscardSwitch())) return
  editor.cancel()
  emit('close')
}

async function confirmDiscardSwitch(): Promise<boolean> {
  return editor.confirmDiscardSwitch()
}

function discardChanges(): void {
  editor.cancel()
}

function hasUnsavedChanges(): boolean {
  return editor.changed.value
}

defineExpose({ requestClose, confirmDiscardSwitch, discardChanges, hasUnsavedChanges })
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="detail?.model_id ?? t('models.drawer.title')"
    :description="
      detail
        ? t('models.drawer.impact', {
            clients: detail.client_model_count,
            groups: detail.group_count,
          })
        : t('models.drawer.description')
    "
    show-description
    :close-label="t('common.close')"
    @update:open="requestClose"
  >
    <template v-if="detail" #title-adornment>
      <CopyChip
        layout="icon"
        :value="detail.model_id"
        :label="t('models.drawer.copyModel', { model: detail.model_id })"
        :success-label="t('models.drawer.copySucceeded')"
        :failure-label="t('models.drawer.copyFailed')"
      />
    </template>

    <SkeletonSurface
      v-if="(open && detailQuery.isPending.value) || initialLoading"
      variant="detail"
      min-height="640px"
      :concealed="!initialLoading"
      :label="t('models.drawer.loading')"
    />
    <QueryFeedback
      v-else-if="detailQuery.isError.value"
      state="error"
      :message="t('models.drawer.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="detailQuery.refetch()"
    />
    <div v-else-if="detail" class="upstream-drawer">
      <div class="upstream-drawer__meta">
        <ModelPriceStatusBadge :price="price" />
        <span v-if="price.updated_at_ms > 0" class="upstream-drawer__faint">
          {{ t('models.drawer.updatedAt') }}
          <AppDateTime :instant="price.updated_at_ms" :locale="locale" />
        </span>
      </div>

      <InlineFeedback v-if="detail.client_model_count > 1" appearance="ledger" tone="warning">
        {{ t('models.drawer.sharedWarning', { count: detail.client_model_count }) }}
      </InlineFeedback>

      <section class="upstream-drawer__section">
        <h3>{{ t('models.drawer.specs') }}</h3>
        <ModelSpecSheet v-if="detail.catalog_reference" :reference="detail.catalog_reference" />
        <p v-else class="upstream-drawer__faint">{{ t('models.inspector.noCatalog') }}</p>
      </section>

      <section class="upstream-drawer__section">
        <h3>
          {{ t('modelPrices.matrix.heading') }}
          <span class="upstream-drawer__eyebrow">{{ t('modelPrices.matrix.unit') }}</span>
        </h3>
        <ModelPriceMatrix
          v-model:draft="editor.draft.value"
          v-model:unpriced-confirm-open="editor.unpricedConfirmOpen.value"
          :model-id="detail.model_id"
          :errors="editor.errors.value"
          :pending="editor.pending.value"
          :failure="editor.failure.value"
          @add-tier="editor.addTier"
          @remove-tier="editor.removeTier"
          @confirm-unpriced="editor.confirmUnpricedSave"
        />
      </section>

      <section class="upstream-drawer__section">
        <h3>{{ t('models.drawer.relationships') }}</h3>

        <div class="upstream-drawer__relation">
          <span class="upstream-drawer__eyebrow">
            {{ t('models.drawer.clientModels') }}
          </span>
          <!-- 客户端模型不可跳转：用中性 code 样式，与下面的分组链接明确区分。 -->
          <ul class="upstream-drawer__clients">
            <li v-for="entry in clientModels" :key="entry">
              <code>{{ entry }}</code>
            </li>
          </ul>
        </div>

        <div class="upstream-drawer__relation">
          <span class="upstream-drawer__eyebrow">
            {{ t('models.drawer.groups') }}
          </span>
          <ul class="upstream-drawer__groups">
            <li v-for="group in groups" :key="group.id">
              <RouterLink :to="groupDetailLocation(group.id)">{{ group.name }}</RouterLink>
              <span v-if="!group.enabled" class="upstream-drawer__tag">
                {{ t('models.detail.groupDisabled') }}
              </span>
            </li>
          </ul>
        </div>
      </section>
    </div>

    <template v-if="detail" #footer>
      <ModelPriceResetDialog
        v-if="price.can_reset"
        :row="price"
        action="reset"
        :disabled="editor.pending.value || editor.changed.value"
        @pending="resetting = $event"
        @completed="editor.cancel()"
      />
      <span v-else />

      <span class="upstream-drawer__actions">
        <AppButton
          variant="secondary"
          size="compact"
          :disabled="editor.pending.value || resetting || !editor.changed.value"
          @click="editor.cancel()"
        >
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          size="compact"
          :busy="editor.pending.value"
          :disabled="!editor.canSave.value || resetting"
          @click="editor.requestSave()"
        >
          {{ t('modelPrices.matrix.save') }}
        </AppButton>
      </span>
    </template>
  </AppDrawer>
</template>

<style scoped>
.upstream-drawer {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  padding-block: var(--space-3-5);
}

/* 概要条：状态、定价方式、最近更新时间一行收口。 */
.upstream-drawer__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2-5);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: var(--space-2) var(--space-2-5);
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}

.upstream-drawer__faint {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.upstream-drawer__section {
  display: grid;
  min-width: 0;
  gap: var(--space-2-5);
}

.upstream-drawer__section h3 {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--text-meta);
  font-weight: 650;
}

.upstream-drawer__eyebrow {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
  letter-spacing: 0.03em;
}

.upstream-drawer__relation {
  display: grid;
  min-width: 0;
  gap: var(--space-1-75);
}

.upstream-drawer__clients,
.upstream-drawer__groups {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1-75) var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.upstream-drawer__clients li,
.upstream-drawer__groups li {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-1);
}

/* 只读影响面：无边框中性底，读起来是标签而不是可点项。 */
.upstream-drawer__clients code {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 2px 7px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

/* 分组可跳转：链接色加下划线，与上面的只读标签区分开。 */
.upstream-drawer__groups a {
  border-bottom: 1px solid currentcolor;
  color: var(--color-action);
  font-size: var(--text-meta);
  font-weight: 560;
  overflow-wrap: anywhere;
}

.upstream-drawer__tag {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.upstream-drawer__actions {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
</style>
