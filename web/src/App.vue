<script setup lang="ts">
import { RouterView } from 'vue-router'

import AppShell from '@/app/AppShell.vue'
import AuthGate from '@/app/AuthGate.vue'
import RouteAnnouncer from '@/app/RouteAnnouncer.vue'
</script>

<template>
  <RouteAnnouncer />
  <RouterView v-slot="{ Component, route }">
    <AuthGate v-if="route.meta.requiresAuth">
      <AppShell>
        <component :is="Component" />
      </AppShell>
    </AuthGate>
    <component :is="Component" v-else />
  </RouterView>
</template>
