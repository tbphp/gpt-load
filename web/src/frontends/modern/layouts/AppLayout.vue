<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

const { t } = useI18n()
</script>

<template>
  <a class="modern-skip-link" href="#modern-content">{{ t('skipToContent') }}</a>
  <div class="modern-app">
    <header class="modern-header">
      <RouterLink class="modern-brand" :to="{ name: 'modern-home' }">GPT-Load</RouterLink>
      <nav :aria-label="t('navigation')">
        <RouterLink :to="{ name: 'modern-settings' }">{{ t('settings') }}</RouterLink>
      </nav>
    </header>
    <main id="modern-content" class="modern-content" tabindex="-1">
      <slot />
    </main>
  </div>
</template>
