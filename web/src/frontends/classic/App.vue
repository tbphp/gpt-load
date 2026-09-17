<script setup lang="ts">
import { RouterView } from 'vue-router'

import AppShell from '@/app/AppShell.vue'
import AuthGate from '@/app/AuthGate.vue'
import RouteAnnouncer from '@/app/RouteAnnouncer.vue'
import PublicShell from '@/components/layout/PublicShell.vue'
import AppToastViewport from '@/components/ui/AppToastViewport.vue'
import UnsavedChangesDialog from '@/components/ui/UnsavedChangesDialog.vue'
</script>

<template>
  <RouteAnnouncer />
  <AppToastViewport />
  <UnsavedChangesDialog />
  <RouterView v-slot="{ Component, route }">
    <AuthGate v-if="route.meta.requiresAuth">
      <AppShell>
        <component :is="Component" />
      </AppShell>
    </AuthGate>
    <PublicShell v-else>
      <component :is="Component" />
    </PublicShell>
  </RouterView>
</template>
