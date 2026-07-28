<script setup lang="ts">
import { ArrowRight, Upload } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupSummary, HealthGroupDto } from '@/api/control/types'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import { isGroupServiceable, normalizeUpstreamHost } from './home-model'

const props = defineProps<{ group: GroupSummary; health?: HealthGroupDto }>()
const { t } = useI18n()
const serviceable = computed(() => isGroupServiceable(props.group, props.health?.counts))
const serviceTone = computed(() => {
  if (serviceable.value === undefined) return 'neutral'
  return serviceable.value ? 'success' : 'danger'
})
const serviceLabel = computed(() => {
  if (serviceable.value === undefined) return t('home.unknown')
  return serviceable.value ? t('home.serviceable') : t('home.unavailable')
})
const serviceReason = computed(() => {
  if (!props.group.enabled) return t('home.groupDisabledReason')
  if (props.group.models.length === 0) return t('home.noModelsReason')
  if (!props.health) return t('home.healthUnknownReason')
  if (props.health.counts.available === 0) return t('home.noAvailableKeysReason')
  return ''
})
</script>

<template>
  <SurfaceCard class="group-card" :data-group-id="group.id">
    <header class="group-card__header">
      <div>
        <h3>{{ group.name }}</h3>
        <p>{{ normalizeUpstreamHost(group.upstream_url) }}</p>
      </div>
      <StatusBadge :tone="serviceTone">{{ serviceLabel }}</StatusBadge>
    </header>

    <div class="group-card__tags">
      <StatusBadge :tone="group.enabled ? 'success' : 'neutral'">
        {{ group.enabled ? t('home.enabled') : t('home.disabled') }}
      </StatusBadge>
      <span v-for="protocol in group.protocols" :key="protocol" class="meta-tag">{{
        t(`common.protocols.${protocol}`)
      }}</span>
    </div>

    <p v-if="serviceReason" data-test="group-service-reason" class="group-card__reason">
      {{ serviceReason }}
    </p>

    <div class="group-card__facts">
      <span>{{ t('home.models', { count: group.models.length }) }}</span>
      <span>{{ t('home.keys', { count: group.key_count }) }}</span>
    </div>
    <div v-if="health" class="group-card__counts">
      <span>{{ t('home.keyAvailable', { count: health.counts.available }) }}</span>
      <span>{{ t('home.keyCooldown', { count: health.counts.cooldown }) }}</span>
      <span>{{ t('home.keyBlacklisted', { count: health.counts.blacklisted }) }}</span>
      <span>{{ t('home.keyDisabled', { count: health.counts.disabled }) }}</span>
    </div>

    <footer class="group-card__actions">
      <RouterLink :to="`/groups/${group.id}?tab=keys`">
        {{ t('home.details') }}<ArrowRight :size="16" aria-hidden="true" />
      </RouterLink>
      <RouterLink :to="`/import?mode=existing&group_id=${group.id}`">
        <Upload :size="16" aria-hidden="true" />{{ t('home.importToGroup') }}
      </RouterLink>
    </footer>
  </SurfaceCard>
</template>

<style scoped>
.group-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-5);
}
.group-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.group-card h3 {
  margin: 0;
  font-size: 1.0625rem;
  overflow-wrap: anywhere;
}
.group-card__header p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
}
.group-card__tags,
.group-card__facts,
.group-card__counts,
.group-card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.group-card__facts {
  color: var(--color-text-muted);
}
.group-card__reason {
  margin: 0;
  color: var(--color-text-muted);
}
.group-card__facts span + span::before {
  content: '·';
  margin-right: var(--space-2);
}
.group-card__counts {
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.group-card__actions {
  margin-top: auto;
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
}
.group-card__actions a {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-primary);
  font-weight: 650;
}
.group-card__actions a + a {
  margin-left: auto;
}
</style>
