<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import SkeletonBlock from './SkeletonBlock.vue'

const { t } = useI18n()
</script>

<template>
  <div
    class="async-surface-loading"
    data-test="async-surface-loading"
    role="status"
    :aria-label="t('common.asyncLoading')"
  >
    <SkeletonBlock height="48px" />
    <SkeletonBlock height="120px" />
  </div>
</template>

<style scoped>
.async-surface-loading {
  display: grid;
  gap: var(--space-3);
}
</style>
