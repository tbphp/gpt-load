<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import PageHeader from '@modern/components/PageHeader.vue'

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('pages.home.title')" />
  </div>
</template>
