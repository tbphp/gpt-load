<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { AppButton, AppIcon } from '@modern/components/ui'

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <p class="modern-unavailable-description">{{ t('notFound.description') }}</p>
    <AppButton as-child>
      <RouterLink :to="{ name: 'modern-home' }">
        <AppIcon :icon="ArrowLeft" size="sm" />{{ t('notFound.backHome') }}
      </RouterLink>
    </AppButton>
  </div>
</template>

<style scoped>
.modern-unavailable-description {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
</style>
