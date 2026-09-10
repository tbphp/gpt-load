<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

const { t } = useI18n()
</script>

<template>
  <section class="modern-page" aria-labelledby="modern-unavailable-title">
    <h1 id="modern-unavailable-title">{{ t('unavailableTitle') }}</h1>
    <p class="modern-description">{{ t('homeDescription') }}</p>
    <RouterLink class="modern-button" :to="{ name: 'modern-settings' }">
      {{ t('interfaceSettings') }}
    </RouterLink>
  </section>
</template>
