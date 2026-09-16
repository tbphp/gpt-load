<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterView, useRoute } from 'vue-router'
import { TooltipProvider } from 'reka-ui'
import { tooltipDelay } from './components/ui/overlay'
import { provideMessages } from './app/messages'
import { clipboardRevision } from './components/ui/clipboard'
import { useScrollbarActivity } from './components/ui/use-scrollbar-activity'
import AppMessageHost from './components/ui/AppMessageHost.vue'

import { usePageTitle } from './app/use-page-title'
import AuthGate from './features/auth/AuthGate.vue'
import AppLayout from './layouts/AppLayout.vue'
import PublicLayout from './layouts/PublicLayout.vue'

const { locale } = useI18n()
const messages = provideMessages()
useScrollbarActivity()
const route = useRoute()
watch(
  () => route.fullPath,
  () => {
    clipboardRevision.value++
  },
  { flush: 'sync' },
)
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
  <TooltipProvider :delay-duration="tooltipDelay" :skip-delay-duration="tooltipDelay">
    <AppMessageHost :message="messages.current.value" @close="messages.close" />
    <RouterView v-slot="{ Component, route: currentRoute }">
      <AuthGate v-if="currentRoute.meta.requiresAuth">
        <AppLayout><component :is="Component" /></AppLayout>
      </AuthGate>
      <PublicLayout v-else><component :is="Component" /></PublicLayout>
    </RouterView>
  </TooltipProvider>
</template>
