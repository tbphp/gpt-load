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

<style scoped>
.modern-brand-logo {
  display: block;
  width: var(--modern-logo-width);
  max-width: 100%;
}
.modern-brand-logo img {
  display: block;
  width: 100%;
  height: auto;
}
.modern-brand-logo .modern-brand-logo__dark {
  display: none;
}
.modern-brand-icon {
  display: block;
  border-radius: var(--modern-radius-control);
}
:global(:root[data-theme='dark'] .modern-brand-logo__light) {
  display: none;
}
:global(:root[data-theme='dark'] .modern-brand-logo .modern-brand-logo__dark) {
  display: block;
}
@media (prefers-color-scheme: dark) {
  :global(:root:not([data-theme='light']) .modern-brand-logo__light) {
    display: none;
  }
  :global(:root:not([data-theme='light']) .modern-brand-logo .modern-brand-logo__dark) {
    display: block;
  }
}
</style>
