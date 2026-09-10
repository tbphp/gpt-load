<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import PageHeader from '@modern/components/PageHeader.vue'
import FrontendPicker from './FrontendPicker.vue'

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('pages.settings.title')" />
    <FrontendPicker />
  </div>
</template>
