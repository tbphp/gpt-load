<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import type { WorkspaceID } from '@modern/app/navigation'
import PageHeader from '@modern/components/PageHeader.vue'

defineProps<{ workspaceId: WorkspaceID }>()

const { t } = useI18n()
const route = useRoute()
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t(route.meta.titleKey ?? `pages.${workspaceId}.title`)" />
  </div>
</template>
