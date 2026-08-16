<script setup lang="ts">
import { Zap } from '@lucide/vue'
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
import {
  type ModelPriceSlotDraft,
  type ModelPriceSlotErrors,
} from '@/features/model-prices/model-price-form'
import ModelPriceSlotsEditor from '@/features/model-prices/ModelPriceSlotsEditor.vue'
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
  channel_id: '',
  channel_name: '',
  channel_mark: '',
  channel_icon: '',
  model_id: '',
  prices: { input: null, output: null, cache_read: null, cache_write: null },
  mode_schedules: {},
  pricing_status: 'pending',
  method: null,
  matched_provider_id: null,
  match_source: null,
  referenced: false,
  reference_count: 0,
  reference_group_count: 0,
  context_tiers: [],
  updated_at_ms: 0,
  can_reset: false,
  can_delete: false,
}
const price = computed(() => detail.value?.price ?? placeholderPrice)
const editor = useModelPriceEditor(toRef(price))
const emptyFastDraft: ModelPriceSlotDraft = {
  input: '',
  output: '',
  cache_read: '',
  cache_write: '',
}
const emptyFastErrors: ModelPriceSlotErrors = {}
const hasFastSchedule = computed(() => Boolean(price.value.mode_schedules.fast))
const fastDraft = computed({
  get: () => editor.draft.value.modeSchedules.fast?.base ?? emptyFastDraft,
  set: (value) => {
    const schedule = editor.draft.value.modeSchedules.fast
    if (schedule) schedule.base = value
  },
})
const fastErrors = computed(() => editor.errors.value.modeSchedules.fast?.base ?? emptyFastErrors)
const resetting = ref(false)

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
        <ModelPriceStatusBadge
          :price="price"
          :provider-name="detail.catalog_reference?.provider_name"
        />
        <span v-if="price.updated_at_ms > 0" class="upstream-drawer__faint">
          {{ t('models.drawer.updatedAt') }}
          <AppDateTime :instant="price.updated_at_ms" :locale="locale" />
        </span>
      </div>

      <dl class="upstream-drawer__identity">
        <div>
          <dt>{{ t('models.drawer.pricingChannel') }}</dt>
          <dd>
            <span class="upstream-drawer__channel-name">{{ price.channel_name }}</span>
            <code class="upstream-drawer__channel-id">{{ price.channel_id }}</code>
          </dd>
        </div>
        <div>
          <dt>{{ t('models.drawer.upstreamModel') }}</dt>
          <dd>
            <code>{{ detail.model_id }}</code>
          </dd>
        </div>
      </dl>

      <InlineFeedback v-if="price.reference_count > 1" appearance="ledger" tone="warning">
        {{
          t('models.drawer.sharedImpact', {
            references: price.reference_count,
            clients: detail.client_model_count,
            groups: detail.group_count,
          })
        }}
      </InlineFeedback>

      <section class="upstream-drawer__section">
        <h3>{{ t('models.drawer.specs') }}</h3>
        <ModelSpecSheet v-if="detail.catalog_reference" :reference="detail.catalog_reference" />
        <p v-else class="upstream-drawer__faint">{{ t('models.inspector.noCatalog') }}</p>
      </section>

      <section class="upstream-drawer__section">
        <h3>
          {{ t('models.drawer.prices') }}
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
        <div v-if="hasFastSchedule" class="upstream-drawer__mode-heading">
          <h3>
            <span>
              <Zap :size="13" aria-hidden="true" />
              {{ t('models.drawer.fastPrices') }}
            </span>
            <span class="upstream-drawer__eyebrow">{{ t('modelPrices.matrix.unit') }}</span>
          </h3>
        </div>
        <ModelPriceSlotsEditor
          v-if="hasFastSchedule"
          v-model:draft="fastDraft"
          id-prefix="model-price-fast"
          :errors="fastErrors"
          :pending="editor.pending.value"
        />
      </section>

      <section class="upstream-drawer__section">
        <h3>{{ t('models.drawer.relationships') }}</h3>
        <p class="upstream-drawer__faint">
          {{ t('models.drawer.relationshipsHelp', { count: price.reference_count }) }}
        </p>
        <ul class="upstream-drawer__associations">
          <li
            v-for="association in detail.associations"
            :key="`${association.client_model}:${association.group.id}`"
          >
            <span
              class="upstream-drawer__mapping"
              :aria-label="
                association.alias_applied
                  ? t('models.drawer.aliasMapping', {
                      client: association.client_model,
                      upstream: detail.model_id,
                    })
                  : t('models.drawer.directMapping', { model: association.client_model })
              "
            >
              <code>{{ association.client_model }}</code>
              <template v-if="association.alias_applied">
                <span aria-hidden="true">→</span>
                <code>{{ detail.model_id }}</code>
              </template>
            </span>
            <span class="upstream-drawer__group">
              <span class="upstream-drawer__eyebrow">{{ t('models.drawer.group') }}</span>
              <RouterLink :to="groupDetailLocation(association.group.id)">
                {{ association.group.name }}
              </RouterLink>
              <span v-if="!association.group.enabled" class="upstream-drawer__tag">
                {{ t('models.detail.groupDisabled') }}
              </span>
            </span>
          </li>
        </ul>
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

/* 概要条只承载价格状态和时间；计价身份在下方独立展开，避免混淆渠道名与 ID。 */
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

.upstream-drawer__identity {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
}

.upstream-drawer__identity > div {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
  border-left: 2px solid var(--color-border-control);
  padding-left: var(--space-2);
}

.upstream-drawer__identity dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.upstream-drawer__identity dd {
  display: flex;
  min-width: 0;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}

.upstream-drawer__identity code {
  color: var(--color-text);
  font-size: var(--text-label-xs);
  overflow-wrap: anywhere;
}

.upstream-drawer__channel-name {
  color: var(--color-text);
  font-size: var(--text-body);
}

.upstream-drawer__identity .upstream-drawer__channel-id {
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  padding: 1px 5px;
  color: var(--color-text-muted);
  line-height: var(--line-compact);
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

.upstream-drawer__mode-heading,
.upstream-drawer__mode-heading h3,
.upstream-drawer__mode-heading h3 > span:first-child {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.upstream-drawer__mode-heading {
  justify-content: space-between;
  flex-wrap: wrap;
}

.upstream-drawer__mode-heading h3 {
  margin: 0;
}

.upstream-drawer__mode-heading svg {
  color: var(--color-text-faint);
}

.upstream-drawer__associations {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.upstream-drawer__associations li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(120px, auto);
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: var(--space-2);
}

.upstream-drawer__mapping,
.upstream-drawer__group {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.upstream-drawer__mapping {
  color: var(--color-text-muted);
}

.upstream-drawer__mapping code {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.upstream-drawer__group {
  justify-content: flex-end;
}

.upstream-drawer__group a {
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

@media (max-width: 620px) {
  .upstream-drawer__identity {
    grid-template-columns: minmax(0, 1fr);
  }

  .upstream-drawer__associations li {
    grid-template-columns: minmax(0, 1fr);
  }

  .upstream-drawer__group {
    justify-content: flex-start;
  }
}
</style>
