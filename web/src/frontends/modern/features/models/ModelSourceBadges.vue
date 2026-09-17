<script setup lang="ts">
import { BookOpen, PencilLine, Radio } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppBadge } from '@modern/components/ui'
import type { ModelSource } from '@modern/api/model-discovery'

defineProps<{ sources?: readonly ModelSource[]; manual?: boolean }>()
const { t } = useI18n()
</script>

<template>
  <span class="modern-model-sources">
    <AppBadge v-if="sources?.includes('live')" :icon="Radio" tone="brand" size="xs">{{
      t('modelSelection.sources.live')
    }}</AppBadge>
    <AppBadge v-if="sources?.includes('catalog')" :icon="BookOpen" size="xs">{{
      t('modelSelection.sources.catalog')
    }}</AppBadge>
    <AppBadge v-if="!sources?.length" :icon="manual ? PencilLine : undefined" size="xs">{{
      t(manual ? 'modelSelection.sources.manual' : 'modelSelection.sources.unknown')
    }}</AppBadge>
  </span>
</template>

<style scoped>
.modern-model-sources {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--modern-space-1);
}
</style>
