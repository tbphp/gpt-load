<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import PageHeader from '@/components/ui/PageHeader.vue'

const { t } = useI18n()
</script>

<template>
  <section class="not-found" aria-labelledby="not-found-title">
    <PageHeader
      id="not-found-title"
      :title="t('notFound.title')"
      :description="t('notFound.description')"
    />
    <RouterLink class="button-link" to="/">{{ t('notFound.backHome') }}</RouterLink>
  </section>
</template>

<style scoped>
.not-found {
  display: grid;
  max-width: 680px;
  gap: var(--space-5);
}
.not-found .button-link {
  width: fit-content;
}
</style>
