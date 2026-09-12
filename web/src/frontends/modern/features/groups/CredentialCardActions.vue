<script setup lang="ts">
import { Download, KeyRound, RotateCcw, SlidersHorizontal, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppActionMenu } from '@modern/components/ui'
const props = defineProps<{ row: CredentialRow; subscription?: boolean; disabled?: boolean }>()
defineEmits<{ action: [value: string] }>()
const { t } = useI18n()
const actions = computed(() => [
  { id: 'details', label: t('credentialCards.diagnosticsAndSettings'), icon: SlidersHorizontal },
  ...(props.subscription
    ? [
        {
          id: 'refresh',
          label: t('credentialCards.refreshToken'),
          icon: KeyRound,
          disabled: !props.row.enabled,
        },
        { id: 'download', label: t('credentialCards.export'), icon: Download },
      ]
    : []),
  ...(props.row.state === 'cooldown' ||
  props.row.state === 'blacklisted' ||
  props.row.modelCooldowns.length
    ? [{ id: 'restore', label: t('credentialCards.restore'), icon: RotateCcw }]
    : []),
  { id: 'delete', label: t('groupDetail.deleteCredential'), icon: Trash2, danger: true },
])
</script>
<template>
  <AppActionMenu
    :label="t('credentialCards.more')"
    :items="actions"
    :disabled="disabled"
    @select="$emit('action', $event)"
  />
</template>
