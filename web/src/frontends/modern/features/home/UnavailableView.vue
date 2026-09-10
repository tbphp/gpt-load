<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import PageHeader from '@modern/components/PageHeader.vue'

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('notFound.title')" :description="t('notFound.description')" />
    <RouterLink class="modern-button" :to="{ name: 'modern-home' }"
      ><ArrowLeft :size="16" aria-hidden="true" />{{ t('notFound.backHome') }}</RouterLink
    >
  </div>
</template>
