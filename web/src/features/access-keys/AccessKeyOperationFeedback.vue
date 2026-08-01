<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import InlineFeedback from '@/components/ui/InlineFeedback.vue'

defineProps<{
  failed: boolean
  editNotApplied: boolean
  mutationFeedbackKey: string
  scopeFeedbackKey: string
  showScopeFeedback: boolean
}>()
const { t } = useI18n()
</script>

<template>
  <InlineFeedback v-if="failed" tone="danger">{{
    t(editNotApplied ? 'accessKeys.drawer.editNotApplied' : 'accessKeys.drawer.saveFailed')
  }}</InlineFeedback>
  <InlineFeedback v-if="mutationFeedbackKey" tone="warning">{{
    t(mutationFeedbackKey)
  }}</InlineFeedback>
  <InlineFeedback v-if="scopeFeedbackKey && showScopeFeedback" tone="warning">{{
    t(scopeFeedbackKey)
  }}</InlineFeedback>
</template>
