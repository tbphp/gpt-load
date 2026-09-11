<script setup lang="ts">
import type { WorkspaceID } from '@modern/app/navigation'

defineProps<{ workspaceId: WorkspaceID }>()
</script>

<template>
  <div class="modern-page" :data-workspace="workspaceId"></div>
</template>
