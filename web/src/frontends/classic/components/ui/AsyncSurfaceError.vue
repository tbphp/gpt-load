<script setup lang="ts">
import { inject } from 'vue'
import { useI18n } from 'vue-i18n'

import QueryFeedback from './QueryFeedback.vue'

const { t } = useI18n()
const reload = inject<() => void>('asyncSurfaceReload', () => window.location.reload())
</script>

<template>
  <QueryFeedback
    state="error"
    :message="t('common.asyncLoadFailed')"
    :retry-label="t('common.retry')"
    @retry="reload"
  />
</template>
