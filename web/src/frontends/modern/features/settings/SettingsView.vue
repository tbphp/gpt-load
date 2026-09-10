<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import FrontendPicker from './FrontendPicker.vue'

const { t } = useI18n()
</script>

<template>
  <section class="modern-page" aria-labelledby="modern-settings-title">
    <h1 id="modern-settings-title">{{ t('settings') }}</h1>
    <FrontendPicker />
  </section>
</template>
