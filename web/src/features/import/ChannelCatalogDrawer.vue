<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useStableLoading } from '@/app/loading-state'
import type { ChannelDto } from '@/app/resources/channels'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

const props = defineProps<{
  open: boolean
  recent: readonly ChannelDto[]
  channels: readonly ChannelDto[]
  search: string
  loading: boolean
  error: boolean
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:search': [value: string]
  retry: []
  select: [channel: ChannelDto]
}>()
const { t } = useI18n()

const normalizedSearch = computed(() => props.search.trim().toLocaleLowerCase())
const recentMatches = computed(() => {
  const query = normalizedSearch.value
  if (!query) return props.recent
  return props.recent.filter(
    (channel) =>
      channel.channel_id.toLocaleLowerCase().includes(query) ||
      channel.name.toLocaleLowerCase().includes(query) ||
      channel.description.toLocaleLowerCase().includes(query),
  )
})
const recentChannelIDs = computed(
  () => new Set(recentMatches.value.map(({ channel_id }) => channel_id)),
)
const channelMatches = computed(() =>
  props.channels.filter(({ channel_id }) => !recentChannelIDs.value.has(channel_id)),
)
const hasAnyResults = computed(
  () => recentMatches.value.length > 0 || channelMatches.value.length > 0,
)
const initialLoadingActive = computed(() => props.loading && !hasAnyResults.value)
const initialLoadingVisible = useStableLoading(initialLoadingActive)
const refreshing = computed(() => props.loading && hasAnyResults.value)

function channelMeta(channel: ChannelDto): string {
  if (channel.param_fields.some(({ key }) => key === 'base_url')) {
    return t('import.presets.baseUrlRequired')
  }
  return t('import.presets.channelReady')
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
        class="channel-catalog-drawer__search"
        :label="t('import.presets.search')"
        :placeholder="t('import.presets.search')"
        :clear-label="t('import.presets.clearSearch')"
        @update:model-value="emit('update:search', $event)"
      />
    </template>

    <div class="channel-catalog-drawer">
      <section v-if="recentMatches.length" class="channel-catalog-drawer__group">
        <h3>{{ t('import.presets.recent') }}</h3>
        <div class="channel-catalog-drawer__options">
          <button
            v-for="channel in recentMatches"
            :key="`recent-${channel.channel_id}`"
            class="channel-catalog-drawer__option"
            type="button"
            @click="emit('select', channel)"
          >
            <span class="channel-catalog-drawer__mark">{{ channel.mark || '···' }}</span>
            <span>
              <OverflowTooltip as="strong" :content="channel.name" :focusable="false">
                {{ channel.name }}
              </OverflowTooltip>
              <OverflowTooltip as="small" :content="channelMeta(channel)" :focusable="false">
                {{ channelMeta(channel) }}
              </OverflowTooltip>
            </span>
            <ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
      </section>

      <AsyncRefreshIndicator :active="refreshing" :label="t('import.presets.loading')" />

      <SkeletonSurface
        v-if="initialLoadingActive || initialLoadingVisible"
        variant="collection"
        :rows="5"
        :columns="3"
        row-height="64px"
        mobile-row-height="76px"
        min-height="358px"
        :show-pagination="false"
        :concealed="!initialLoadingVisible"
        :label="t('import.presets.loading')"
      />
      <InlineFeedback v-else-if="error" class="channel-catalog-drawer__state" tone="danger">
        {{ t('import.presets.loadFailed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="emit('retry')">
            {{ t('common.retry') }}
          </AppButton>
        </template>
      </InlineFeedback>
      <template v-else>
        <section v-if="channelMatches.length" class="channel-catalog-drawer__group">
          <h3>{{ t('import.presets.recommended') }}</h3>
          <div class="channel-catalog-drawer__options">
            <button
              v-for="channel in channelMatches"
              :key="channel.channel_id"
              class="channel-catalog-drawer__option"
              type="button"
              @click="emit('select', channel)"
            >
              <span class="channel-catalog-drawer__mark">{{ channel.mark || '···' }}</span>
              <span>
                <OverflowTooltip as="strong" :content="channel.name" :focusable="false">
                  {{ channel.name }}
                </OverflowTooltip>
                <OverflowTooltip as="small" :content="channelMeta(channel)" :focusable="false">
                  {{ channelMeta(channel) }}
                </OverflowTooltip>
              </span>
              <ChevronRight :size="16" aria-hidden="true" />
            </button>
          </div>
        </section>

        <InlineFeedback v-if="!hasAnyResults" class="channel-catalog-drawer__state" tone="warning">
          {{ t('import.presets.noMatches') }}
        </InlineFeedback>
      </template>
    </div>
  </AppDrawer>
</template>

<style scoped>
.channel-catalog-drawer {
  min-height: 100%;
}

.channel-catalog-drawer__search {
  width: auto;
  min-width: 0;
  flex: 1;
}

.channel-catalog-drawer__state {
  margin-top: var(--space-3);
}

.channel-catalog-drawer__group:first-of-type {
  margin-top: var(--space-5);
}

.channel-catalog-drawer__group + .channel-catalog-drawer__group {
  margin-top: var(--space-5);
}

.channel-catalog-drawer__group h3 {
  margin: 0 0 var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
  letter-spacing: 0.04em;
}

.channel-catalog-drawer__options {
  display: grid;
  gap: var(--space-2);
}

.channel-catalog-drawer__option {
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

.channel-catalog-drawer__option:hover {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.channel-catalog-drawer__option strong,
.channel-catalog-drawer__option small {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-catalog-drawer__option strong {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
}

.channel-catalog-drawer__option small {
  margin-top: 1px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.channel-catalog-drawer__mark {
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
