<script setup lang="ts">
import { RouterView } from 'vue-router'

import AuthGate from '@/app/AuthGate.vue'
</script>

<template>
  <RouterView v-slot="{ Component, route }">
    <AuthGate v-if="route.meta.requiresAuth">
      <component :is="Component" />
    </AuthGate>
    <component :is="Component" v-else />
  </RouterView>
</template>
