<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterView, useRoute } from 'vue-router'

import { findNavigationItem } from './app/navigation'
import AuthGate from './features/auth/AuthGate.vue'
import AppLayout from './layouts/AppLayout.vue'
import PublicLayout from './layouts/PublicLayout.vue'

const route = useRoute()
const { t, locale } = useI18n()
const title = computed(() => {
  const item = findNavigationItem(route.meta.primaryNav ?? route.name)
  return t(
    typeof route.meta.titleKey === 'string'
      ? route.meta.titleKey
      : item
        ? `pages.${item.id}.title`
        : 'notFound.title',
  )
})
watch(
  [title, locale],
  () => {
    document.title = `${title.value} · GPT-Load`
  },
  { immediate: true },
)
</script>

<template>
  <RouterView v-slot="{ Component, route: currentRoute }">
    <AuthGate v-if="currentRoute.meta.requiresAuth">
      <AppLayout><component :is="Component" /></AppLayout>
    </AuthGate>
    <PublicLayout v-else><component :is="Component" /></PublicLayout>
  </RouterView>
</template>
