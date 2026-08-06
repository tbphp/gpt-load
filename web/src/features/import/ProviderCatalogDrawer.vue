<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProviderSuggestion } from '@/app/resources/providers'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = defineProps<{
  open: boolean
  recent: readonly ProviderSuggestion[]
  suggestions: readonly ProviderSuggestion[]
  search: string
  loading: boolean
  error: boolean
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:search': [value: string]
  retry: []
  selectSuggestion: [provider: ProviderSuggestion]
  selectRecent: [provider: ProviderSuggestion]
  custom: []
}>()
const { t } = useI18n()

const normalizedSearch = computed(() => props.search.trim().toLocaleLowerCase())
const recentMatches = computed(() => {
  const query = normalizedSearch.value
  if (!query) return props.recent
  return props.recent.filter(
    (provider) =>
      provider.provider_id.toLocaleLowerCase().includes(query) ||
      provider.name.toLocaleLowerCase().includes(query) ||
      (provider.api_url?.toLocaleLowerCase().includes(query) ?? false),
  )
})
const recentProviderIDs = computed(
  () => new Set(recentMatches.value.map(({ provider_id }) => provider_id)),
)
const curatedMatches = computed(() =>
  props.suggestions.filter(
    ({ provider_id, source }) => source === 'curated' && !recentProviderIDs.value.has(provider_id),
  ),
)
const catalogMatches = computed(() =>
  props.suggestions.filter(
    ({ provider_id, source }) => source === 'catalog' && !recentProviderIDs.value.has(provider_id),
  ),
)
const hasAnyResults = computed(
  () =>
    recentMatches.value.length > 0 ||
    curatedMatches.value.length > 0 ||
    catalogMatches.value.length > 0,
)

function providerMeta(provider: ProviderSuggestion): string {
  const protocolsText = provider.protocols.length
    ? provider.protocols.join(' · ')
    : t('import.presets.protocolsUnavailable')
  const urlText = provider.api_url ? hostOf(provider.api_url) : t('import.presets.urlRequired')
  return `${protocolsText} · ${urlText}`
}

function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}
</script>

<template>
  <AppDrawer
    appearance="ledger"
    :open="open"
    :title="t('import.presets.catalog')"
    :description="t('import.presets.description')"
    :close-label="t('import.presets.collapse')"
    @update:open="emit('update:open', $event)"
  >
    <template #filters>
      <AppSearchInput
        :model-value="search"
        class="provider-catalog-drawer__search"
        :label="t('import.presets.search')"
        :placeholder="t('import.presets.search')"
        :clear-label="t('import.presets.clearSearch')"
        @update:model-value="emit('update:search', $event)"
      />
    </template>

    <div class="provider-catalog-drawer">
      <section v-if="recentMatches.length" class="provider-catalog-drawer__group">
        <h3>{{ t('import.presets.recent') }}</h3>
        <div class="provider-catalog-drawer__options">
          <button
            v-for="provider in recentMatches"
            :key="`recent-${provider.provider_id}`"
            class="provider-catalog-drawer__option"
            type="button"
            @click="emit('selectRecent', provider)"
          >
            <span class="provider-catalog-drawer__mark">{{ provider.mark || '···' }}</span>
            <span>
              <strong>{{ provider.name }}</strong>
              <small>{{ providerMeta(provider) }}</small>
            </span>
            <span aria-hidden="true">→</span>
          </button>
        </div>
      </section>

      <InlineFeedback v-if="loading" class="provider-catalog-drawer__state">
        {{ t('import.presets.loading') }}
      </InlineFeedback>
      <InlineFeedback v-else-if="error" class="provider-catalog-drawer__state" tone="danger">
        {{ t('import.presets.loadFailed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="emit('retry')">
            {{ t('common.retry') }}
          </AppButton>
        </template>
      </InlineFeedback>
      <template v-else>
        <section v-if="curatedMatches.length" class="provider-catalog-drawer__group">
          <h3>{{ t('import.presets.recommended') }}</h3>
          <div class="provider-catalog-drawer__options">
            <button
              v-for="provider in curatedMatches"
              :key="provider.provider_id"
              class="provider-catalog-drawer__option"
              type="button"
              @click="emit('selectSuggestion', provider)"
            >
              <span class="provider-catalog-drawer__mark">{{ provider.mark || '···' }}</span>
              <span>
                <strong>{{ provider.name }}</strong>
                <small>{{ providerMeta(provider) }}</small>
              </span>
              <span aria-hidden="true">→</span>
            </button>
          </div>
        </section>

        <section v-if="catalogMatches.length" class="provider-catalog-drawer__group">
          <h3>{{ t('import.presets.catalogMore') }}</h3>
          <div class="provider-catalog-drawer__options">
            <button
              v-for="provider in catalogMatches"
              :key="provider.provider_id"
              class="provider-catalog-drawer__option"
              type="button"
              @click="emit('selectSuggestion', provider)"
            >
              <span class="provider-catalog-drawer__mark">{{ provider.mark || '···' }}</span>
              <span>
                <strong>{{ provider.name }}</strong>
                <small>{{ providerMeta(provider) }}</small>
              </span>
              <span aria-hidden="true">→</span>
            </button>
          </div>
        </section>

        <InlineFeedback v-if="!hasAnyResults" class="provider-catalog-drawer__state" tone="warning">
          {{ t('import.presets.noMatches') }}
          <template #action>
            <AppButton variant="link" size="inline" @click="emit('custom')">
              {{ t('import.presets.custom.name') }}
            </AppButton>
          </template>
        </InlineFeedback>
      </template>
    </div>
  </AppDrawer>
</template>

<style scoped>
.provider-catalog-drawer {
  min-height: 100%;
}

.provider-catalog-drawer__search {
  width: auto;
  min-width: 0;
  flex: 1;
}

.provider-catalog-drawer__state {
  margin-top: var(--space-3);
}

.provider-catalog-drawer__group:first-of-type {
  margin-top: var(--space-5);
}

.provider-catalog-drawer__group + .provider-catalog-drawer__group {
  margin-top: var(--space-5);
}

.provider-catalog-drawer__group h3 {
  margin: 0 0 var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
  letter-spacing: 0.04em;
}

.provider-catalog-drawer__options {
  display: grid;
  gap: var(--space-2);
}

.provider-catalog-drawer__option {
  display: grid;
  min-width: 0;
  min-height: var(--touch-target);
  grid-template-columns: 28px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 9px 10px;
  text-align: left;
  cursor: pointer;
}

.provider-catalog-drawer__option:hover {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.provider-catalog-drawer__option strong,
.provider-catalog-drawer__option small {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-catalog-drawer__option strong {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
}

.provider-catalog-drawer__option small {
  margin-top: 1px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.provider-catalog-drawer__mark {
  display: grid;
  width: 26px;
  height: 26px;
  place-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 700;
}
</style>
