<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import PageHeader from '@modern/components/PageHeader.vue'
import AppButton from '@modern/components/ui/AppButton.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('notFound.title')" :description="t('notFound.description')" />
    <AppButton as-child>
      <RouterLink :to="{ name: 'modern-home' }">
        <AppIcon :icon="ArrowLeft" size="sm" />{{ t('notFound.backHome') }}
      </RouterLink>
    </AppButton>
  </div>
</template>
