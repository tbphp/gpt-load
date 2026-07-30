<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { homeLocation } from '@/app/route-locations'

const route = useRoute()
const { t } = useI18n()
const title = computed(() => t(String(route.meta.titleKey ?? 'common.appName')))
</script>

<template>
  <section class="surface-card placeholder-card" :aria-labelledby="`${String(route.name)}-title`">
    <p class="eyebrow">M3</p>
    <h1 :id="`${String(route.name)}-title`">{{ title }}</h1>
    <p class="lead">{{ t('shell.placeholderDescription') }}</p>
    <div class="meta-row">
      <RouterLink class="button-link" :to="homeLocation()">
        {{ t('shell.backHome') }}
      </RouterLink>
    </div>
  </section>
</template>

<style scoped>
.placeholder-card h1 {
  max-width: none;
}
</style>
