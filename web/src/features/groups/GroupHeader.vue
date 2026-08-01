<script setup lang="ts">
import { ArrowLeft, Upload } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { GroupSummaryDto } from '@/app/resources/groups'
import { groupsLocation, importLocation } from '@/app/route-locations'
import CopyChip from '@/components/ui/CopyChip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

defineProps<{ group: GroupSummaryDto }>()
const { t } = useI18n()
</script>

<template>
  <header class="group-header">
    <RouterLink class="group-header__back" :to="groupsLocation()">
      <ArrowLeft :size="16" aria-hidden="true" />{{ t('group.backToGroups') }}
    </RouterLink>
    <div class="group-header__topline">
      <div class="group-header__title">
        <h1 id="group-detail-title">{{ group.name }}</h1>
        <StatusBadge :status="group.service_status">
          {{ t(`groups.collection.status.${group.service_status}`) }}
        </StatusBadge>
      </div>
      <RouterLink
        class="button-link group-header__import"
        :to="importLocation({ mode: 'existing', group_id: group.id })"
      >
        <Upload :size="16" aria-hidden="true" />{{ t('group.importKeys') }}
      </RouterLink>
    </div>
    <div class="group-header__details">
      <CopyChip
        :value="group.upstream_url"
        :label="t('group.copyUpstreamUrl', { url: group.upstream_url })"
        :success-label="t('group.copySuccess')"
        :failure-label="t('group.copyFailure')"
      />
      <div class="group-header__protocols" :aria-label="t('group.protocolsLabel')">
        <span v-for="protocol in group.protocols" :key="protocol" class="meta-tag">{{
          protocol
        }}</span>
      </div>
    </div>
  </header>
</template>

<style scoped>
.group-header {
  display: grid;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}
.group-header__back {
  display: inline-flex;
  width: fit-content;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-weight: 650;
}
.group-header__topline {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.group-header__title {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.group-header h1 {
  max-width: none;
  margin: 0;
  font-size: clamp(1.65rem, 3vw, 2.2rem);
}
.group-header__import {
  flex: 0 0 auto;
  gap: var(--space-2);
}
.group-header__details {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}
.group-header__protocols {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}
@media (max-width: 620px) {
  .group-header__topline {
    align-items: stretch;
    flex-direction: column;
  }
  .group-header__import {
    width: 100%;
  }
}
</style>
