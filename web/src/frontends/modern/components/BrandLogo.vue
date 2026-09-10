<script setup lang="ts">
import icon from '@modern/assets/brand/icon.svg'
import logoDark from '@modern/assets/brand/logo-dark.svg'
import logoLight from '@modern/assets/brand/logo-light.svg'

defineProps<{ compact?: boolean }>()
</script>

<template>
  <img v-if="compact" class="modern-brand-icon" :src="icon" alt="GPT-Load" width="32" height="32" />
  <span v-else class="modern-brand-logo">
    <img class="modern-brand-logo__light" :src="logoLight" alt="GPT-Load" width="192" height="48" />
    <img class="modern-brand-logo__dark" :src="logoDark" alt="GPT-Load" width="192" height="48" />
  </span>
</template>
