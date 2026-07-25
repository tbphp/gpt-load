<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'

import { useImportRecovery } from './import-recovery'
import NewGroupImport from './NewGroupImport.vue'

const route = useRoute()
const router = useRouter()
const recovery = useImportRecovery()
const { t } = useI18n()
const recoveredDraft = recovery.consume()
const rawMode = computed(() =>
  typeof route.query.mode === 'string' ? route.query.mode : undefined,
)

if (rawMode.value !== undefined && rawMode.value !== 'new' && rawMode.value !== 'existing') {
  void router.replace({ name: 'import', query: { mode: 'new' } })
}

const showNew = computed(() => recoveredDraft !== null || rawMode.value !== 'existing')
</script>

<template>
  <div class="import-page">
    <PageHeader :title="t('import.title')" :description="t('import.description')" />
    <NewGroupImport v-if="showNew" :initial-draft="recoveredDraft" />
    <InlineFeedback v-else tone="info">{{ t('import.existingDeferred') }}</InlineFeedback>
  </div>
</template>

<style scoped>
.import-page {
  display: grid;
  gap: var(--space-6);
}
</style>
