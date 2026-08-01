<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useUnsavedChangesController } from '@/app/unsaved-changes'

import AppConfirmDialog from './AppConfirmDialog.vue'

const controller = useUnsavedChangesController()
const { t } = useI18n()
const open = computed(() => controller.dialogOpen.value)
</script>

<template>
  <AppConfirmDialog
    :open="open"
    :title="t('common.unsavedChangesDialog.title')"
    :description="t('common.unsavedChangesDialog.description')"
    :close-label="t('common.close')"
    :cancel-label="t('common.unsavedChangesDialog.cancel')"
    :confirm-label="t('common.unsavedChangesDialog.confirm')"
    tone="danger"
    @update:open="!$event && controller.resolveConfirmation(false)"
    @confirm="controller.resolveConfirmation(true)"
  />
</template>
