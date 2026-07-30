<script setup lang="ts">
import { ArrowLeft, Upload } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { GroupDetailDto } from '@/app/resources/groups'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { normalizeUpstreamHost } from './upstream-host'

defineProps<{ group: GroupDetailDto }>()
const { t } = useI18n()
</script>

<template>
  <header class="group-header">
    <RouterLink class="group-header__back" to="/">
      <ArrowLeft :size="16" aria-hidden="true" />{{ t('shell.backHome') }}
    </RouterLink>
    <div class="group-header__main">
      <div class="group-header__identity">
        <h1>{{ group.name }}</h1>
        <p>{{ normalizeUpstreamHost(group.upstream_url) }}</p>
      </div>
      <div class="group-header__meta">
        <span v-for="protocol in group.protocols" :key="protocol" class="meta-tag">
          {{ t(`common.protocols.${protocol}`) }}
        </span>
        <StatusBadge :tone="group.enabled ? 'success' : 'neutral'">
          {{ group.enabled ? t('group.enabled') : t('group.disabled') }}
        </StatusBadge>
      </div>
      <RouterLink
        class="button-link group-header__import"
        data-test="group-import-link"
        :to="`/import?mode=existing&group_id=${group.id}`"
      >
        <Upload :size="16" aria-hidden="true" />{{ t('group.importKeys') }}
      </RouterLink>
    </div>
  </header>
</template>

<style scoped>
.group-header {
  display: grid;
  gap: var(--space-3);
}
.group-header__back {
  display: inline-flex;
  width: fit-content;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-weight: 650;
}
.group-header__main {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-4);
}
.group-header__identity {
  min-width: 0;
}
.group-header h1 {
  max-width: none;
  font-size: clamp(1.5rem, 3vw, 2rem);
}
.group-header__identity p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  overflow-wrap: anywhere;
}
.group-header__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.group-header__import {
  flex: 0 0 auto;
  gap: var(--space-2);
  margin-left: auto;
}
@media (max-width: 720px) {
  .group-header__main {
    align-items: flex-start;
    flex-direction: column;
  }
  .group-header__import {
    width: 100%;
    margin-left: 0;
  }
}
</style>
