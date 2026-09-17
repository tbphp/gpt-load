<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import SkeletonSurface from './SkeletonSurface.vue'

const { t } = useI18n()
</script>

<template>
  <SkeletonSurface variant="panel" min-height="420px" :label="t('common.asyncLoading')" />
</template>
