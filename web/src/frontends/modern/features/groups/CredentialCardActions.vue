<script setup lang="ts">
import { Download, Eye, KeyRound, RotateCcw, Stethoscope, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppActionMenu, AppIconButton } from '@modern/components/ui'
const props = defineProps<{ row: CredentialRow; subscription?: boolean; disabled?: boolean }>()
defineEmits<{ action: [value: string] }>()
const { t } = useI18n()
const actions = computed(() => [
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
    : [{ id: 'test', label: t('credentialCards.test'), icon: Stethoscope }]),
  ...(props.row.state === 'cooldown' ||
  props.row.state === 'blacklisted' ||
  props.row.modelCooldowns.length
    ? [{ id: 'restore', label: t('credentialCards.restore'), icon: RotateCcw }]
    : []),
  { id: 'delete', label: t('groupDetail.deleteCredential'), icon: Trash2, danger: true },
])
</script>
<template>
  <div class="modern-credential-card-action-menu">
    <AppIconButton
      :icon="Eye"
      :label="t('credentialCards.viewDetails')"
      size="xxs"
      :disabled="disabled"
      @click="$emit('action', 'details')"
    />
    <AppActionMenu
      size="xxs"
      :label="t('credentialCards.more')"
      :items="actions"
      :disabled="disabled"
      @select="$emit('action', $event)"
    />
  </div>
</template>

<style scoped>
.modern-credential-card-action-menu {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--modern-space-0-5);
}
</style>
