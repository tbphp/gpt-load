<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { WorkspaceID } from '@modern/app/navigation'
import PageHeader from '@modern/components/PageHeader.vue'

defineProps<{ workspaceId: WorkspaceID; titleKey?: string }>()

const { t } = useI18n()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t(titleKey ?? `pages.${workspaceId}.title`)" />
  </div>
</template>
