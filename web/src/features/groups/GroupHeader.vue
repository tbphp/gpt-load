<script setup lang="ts">
import { ArrowLeft, Globe, SlidersHorizontal } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupSummaryDto } from '@/app/resources/groups'
import { useApiClient } from '@/api/client-context'
import { channelsQueryOptions } from '@/app/resources/channels'
import { groupsLocation } from '@/app/route-locations'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{ group: GroupSummaryDto }>()
const { t } = useI18n()
const client = useApiClient()
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const channel = computed(() =>
  channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === props.group.channel_id),
)
const channelName = computed(() => channel.value?.name.trim() || props.group.channel_id)
const customUpstreamUrl = computed(
  () => props.group.params.base_url?.trim() || props.group.params.endpoint?.trim(),
)
const upstreamUrls = computed(() => {
  if (customUpstreamUrl.value) return [customUpstreamUrl.value]
  if (channel.value?.default_base_urls.length) return channel.value.default_base_urls
  return channel.value?.default_base_url ? [channel.value.default_base_url] : []
})
const upstreamUrlSource = computed(() =>
  t(customUpstreamUrl.value ? 'group.upstreamUrlCustom' : 'group.upstreamUrlOfficialDefault'),
)
</script>

<template>
  <header class="group-header">
    <RouterLink class="group-header__back" :to="groupsLocation()">
      <ArrowLeft :size="16" aria-hidden="true" />{{ t('group.backToGroups') }}
    </RouterLink>
    <div class="group-header__body">
      <div class="group-header__topline">
        <div class="group-header__title">
          <h1 id="group-detail-title">{{ group.name }}</h1>
          <StatusBadge :status="group.service_status" size="compact">
            {{ t(`groups.collection.status.${group.service_status}`) }}
          </StatusBadge>
          <span
            v-if="group.service_status === 'unavailable' && group.service_status_reason"
            class="group-header__status-reason"
          >
            {{ t(`groups.collection.statusReason.${group.service_status_reason}`) }}
          </span>
        </div>
      </div>
      <div class="group-header__details">
        <span class="group-header__id">#{{ group.id }}</span>
        <span v-if="group.price_multiplier !== '1'" class="meta-tag">
          {{ t('common.priceMultiplier.value', { value: group.price_multiplier }) }}
        </span>
        <span class="meta-tag">
          <ChannelIcon
            v-if="channel"
            class="group-header__channel-icon"
            :icon="channel.icon"
            :mark="channel.mark"
          />
          <span>{{ channelName }}</span>
        </span>
        <span v-for="url in upstreamUrls" :key="url" class="group-header__upstream">
          <CopyChip
            :value="url"
            :label="t('group.copyUpstreamUrl', { url })"
            :success-label="t('group.copySuccess')"
            :failure-label="t('group.copyFailure')"
            layout="trailing"
          />
          <AppTooltip :content="upstreamUrlSource">
            <span
              class="group-header__url-source"
              role="img"
              :aria-label="upstreamUrlSource"
              tabindex="0"
            >
              <SlidersHorizontal v-if="customUpstreamUrl" :size="14" aria-hidden="true" />
              <Globe v-else :size="14" aria-hidden="true" />
            </span>
          </AppTooltip>
        </span>
      </div>
    </div>
  </header>
</template>

<style scoped>
.group-header {
  display: grid;
}
.group-header__back {
  display: inline-flex;
  width: fit-content;
  min-height: 38px;
  align-items: center;
  gap: 6px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}
.group-header__back:hover {
  color: var(--color-action);
}
.group-header__body {
  min-width: 0;
  padding: var(--space-2) 0 22px;
}
.group-header__topline {
  display: flex;
  min-width: 0;
  align-items: center;
}
.group-header__title {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
.group-header__status-reason {
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.group-header h1 {
  max-width: none;
  margin: 0;
  font-size: clamp(26px, 4vw, 34px);
  font-weight: 650;
  letter-spacing: -0.025em;
  line-height: 1.15;
  overflow-wrap: anywhere;
}
.group-header__details {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px 12px;
  margin-top: 13px;
}
.group-header__id {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
.group-header .meta-tag {
  display: inline-flex;
  min-height: 24px;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 3px 7px;
  font-size: var(--text-label-xs);
}
.group-header__channel-icon {
  flex: none;
  font-size: 15px;
}
.group-header__upstream {
  display: inline-flex;
  max-width: 100%;
  min-width: 0;
  align-items: center;
  gap: 2px;
}
.group-header__url-source {
  display: inline-flex;
  flex: none;
  color: var(--color-text-faint);
}
.group-header__details :deep(.copy-chip) {
  max-width: 20rem;
  min-height: var(--control-compact);
  padding: 3px 5px;
  font-size: var(--text-sm);
}
@media (max-width: 800px) {
  .group-header h1 {
    font-size: 27px;
  }
}
@media (max-width: 520px) {
  .group-header__back {
    min-height: var(--touch-target);
  }
  .group-header__body {
    padding-top: 6px;
    padding-bottom: 18px;
  }
  .group-header__title {
    align-items: flex-start;
    flex-direction: column;
    gap: 7px;
  }
  .group-header h1 {
    font-size: 25px;
  }
  .group-header__details {
    align-items: flex-start;
    flex-direction: column;
  }
  .group-header__details :deep(.copy-chip-wrap) {
    width: 100%;
  }
  .group-header__upstream {
    width: 100%;
  }
  .group-header__details :deep(.copy-chip) {
    width: 100%;
    max-width: 100%;
  }
}
</style>
