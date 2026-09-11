<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterView } from 'vue-router'

import { usePageTitle } from './app/use-page-title'
import AuthGate from './features/auth/AuthGate.vue'
import AppLayout from './layouts/AppLayout.vue'
import PublicLayout from './layouts/PublicLayout.vue'

const { locale } = useI18n()
const { title } = usePageTitle()
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
