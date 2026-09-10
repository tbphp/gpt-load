<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { navigationItems, type WorkspaceID } from '@modern/app/navigation'
import PageHeader from '@modern/components/PageHeader.vue'
const props = defineProps<{ workspaceId: WorkspaceID }>()
const { t } = useI18n()
const workspace = computed(() => navigationItems.find((item) => item.id === props.workspaceId))
</script>

<template>
  <div class="modern-page">
    <PageHeader
      :title="t(`pages.${workspaceId}.title`)"
      :description="t(`pages.${workspaceId}.description`)"
    />
    <section
      class="modern-panel modern-workspace-pending"
      :aria-label="t('workspace.pendingTitle')"
    >
      <span class="modern-workspace-pending__icon"
        ><component :is="workspace?.icon" :size="28" :stroke-width="1.5" aria-hidden="true"
      /></span>
      <h2>{{ t('workspace.pendingTitle') }}</h2>
      <p>{{ t('workspace.pendingDescription') }}</p>
      <RouterLink class="modern-button" :to="{ name: 'modern-home' }"
        ><ArrowLeft :size="16" aria-hidden="true" />{{ t('workspace.backHome') }}</RouterLink
      >
    </section>
  </div>
</template>
