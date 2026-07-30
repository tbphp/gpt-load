<script setup lang="ts">
import { ArrowLeft, Plus, Tags } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { lazySurface } from '@/app/async-surface'
import { modelPriceQueryOptions, type ModelPriceRuleDto } from '@/app/resources/model-prices'
import AppButton from '@/components/ui/AppButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import ModelPriceCollection from './ModelPriceCollection.vue'

const ModelPriceDrawer = lazySurface(() => import('./ModelPriceDrawer.vue'))

const client = useApiClient()
const { t } = useI18n()
const drawerOpen = ref(false)
const selected = ref<ModelPriceRuleDto | null>(null)
let restoreFocus: HTMLElement | null = null

const pricesQuery = useQuery(modelPriceQueryOptions(client))

function addOverride(): void {
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function editRule(rule: ModelPriceRuleDto, trigger: HTMLElement): void {
  selected.value = rule
  restoreFocus = trigger
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
    selected.value = null
    const target = restoreFocus
    restoreFocus = null
    await nextTick()
    target?.focus()
  }
}
</script>

<template>
  <section class="model-prices" aria-labelledby="model-prices-title">
    <RouterLink class="model-prices__back" to="/settings">
      <ArrowLeft :size="16" aria-hidden="true" />{{ t('modelPrices.back') }}
    </RouterLink>

    <PageHeader
      id="model-prices-title"
      :title="t('modelPrices.title')"
      :description="t('modelPrices.description')"
    >
      <template #actions>
        <AppButton data-test="model-price-add" @click="addOverride">
          <Plus :size="16" aria-hidden="true" />{{ t('modelPrices.add') }}
        </AppButton>
      </template>
    </PageHeader>

    <ModelPriceDrawer
      v-if="drawerOpen"
      :open="drawerOpen"
      :rule="selected"
      @update:open="setDrawerOpen"
    />

    <SurfaceCard class="model-prices__notice">
      <Tags :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('modelPrices.priceUnit') }}</strong>
        <p>{{ t('modelPrices.historyNote') }}</p>
        <p>{{ t('modelPrices.modelIdentityNote') }}</p>
        <p>{{ t('modelPrices.precedenceNote') }}</p>
        <p>{{ t('modelPrices.wholeRuleNote') }}</p>
      </div>
    </SurfaceCard>

    <QueryFeedback
      v-if="pricesQuery.isPending.value"
      state="loading"
      :message="t('modelPrices.loading')"
    />
    <QueryFeedback
      v-else-if="pricesQuery.isError.value && !pricesQuery.data.value"
      state="error"
      :message="t('modelPrices.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="pricesQuery.refetch()"
    />
    <template v-else-if="pricesQuery.data.value">
      <QueryFeedback
        v-if="pricesQuery.isError.value"
        state="stale"
        :message="t('modelPrices.stale')"
        :retry-label="t('common.retry')"
        @retry="pricesQuery.refetch()"
      />

      <section
        class="model-prices__section"
        data-test="model-price-rule-section"
        data-source="user"
        aria-labelledby="override-prices-title"
      >
        <div class="model-prices__section-heading">
          <div>
            <h2 id="override-prices-title">{{ t('modelPrices.overrides.title') }}</h2>
            <p>{{ t('modelPrices.overrides.description') }}</p>
          </div>
          <StatusBadge>{{ t('modelPrices.source.user') }}</StatusBadge>
        </div>
        <EmptyState
          v-if="pricesQuery.data.value.overrides.length === 0"
          :title="t('modelPrices.overrides.empty')"
          :description="t('modelPrices.overrides.emptyDescription')"
        />
        <ModelPriceCollection
          v-else
          :rules="pricesQuery.data.value.overrides"
          source="user"
          @edit="editRule"
        />
      </section>

      <section
        class="model-prices__section"
        data-test="model-price-rule-section"
        data-source="builtin"
        aria-labelledby="builtin-prices-title"
      >
        <div class="model-prices__section-heading">
          <div>
            <h2 id="builtin-prices-title">{{ t('modelPrices.builtin.title') }}</h2>
            <p>{{ t('modelPrices.builtin.description') }}</p>
          </div>
          <StatusBadge>{{ t('modelPrices.source.builtin') }}</StatusBadge>
        </div>
        <EmptyState
          v-if="pricesQuery.data.value.builtin.length === 0"
          :title="t('modelPrices.builtin.empty')"
          :description="t('modelPrices.builtin.emptyDescription')"
        />
        <ModelPriceCollection
          v-else
          :rules="pricesQuery.data.value.builtin"
          source="builtin"
          @edit="editRule"
        />
      </section>
    </template>
  </section>
</template>

<style scoped>
.model-prices {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}
.model-prices__back {
  display: inline-flex;
  align-items: center;
}
.model-prices__back {
  width: fit-content;
  min-height: 44px;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-weight: 650;
}
.model-prices__back:hover {
  color: var(--color-action);
}
.model-prices__notice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-4);
}
.model-prices__notice p,
.model-prices__section-heading p {
  margin: 0;
  color: var(--color-text-muted);
}
.model-prices__notice p + p {
  margin-top: var(--space-1);
}
.model-prices__section {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.model-prices__section-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.model-prices__section-heading h2 {
  margin: 0;
  font-size: 1.125rem;
}
@media (max-width: 640px) {
  .model-prices__section-heading {
    align-items: stretch;
    flex-direction: column;
  }
  .model-prices__section-heading > :last-child {
    width: fit-content;
  }
}
</style>
