<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import PageHeader from '@modern/components/PageHeader.vue'
const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('unavailableTitle')" :description="t('homeDescription')" />
    <RouterLink class="modern-button" :to="{ name: 'modern-home' }"
      ><ArrowLeft :size="16" aria-hidden="true" />{{ t('workspace.backHome') }}</RouterLink
    >
  </div>
</template>
