<script setup lang="ts">
import { computed, onScopeDispose } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppCopyValue, AppOverflowText } from '@modern/components/ui'
import { revealCredential } from '@modern/api/credential-actions'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{
  name: string
  groupId: number | null
  credentialId: number | null
  deleted: boolean
  connectionType?: 'api_key' | 'subscription'
}>()
const { t } = useI18n()
const client = useApiClient()
const controller = new AbortController()
onScopeDispose(() => controller.abort())
const display = computed(
  () =>
    props.name ||
    (props.credentialId ? t(props.deleted ? 'logs.deleted' : 'logs.unavailableCredential') : '—'),
)
const canCopy = computed(() =>
  Boolean(
    !props.deleted &&
    props.name &&
    (props.connectionType === 'subscription' ||
      (props.connectionType === 'api_key' && props.groupId && props.credentialId)),
  ),
)
function resolveSecret(): Promise<string> {
  if (!props.groupId || !props.credentialId) throw new Error('CREDENTIAL_NOT_AVAILABLE')
  return revealCredential(client, props.groupId, props.credentialId, controller.signal)
}
</script>

<template>
  <AppCopyValue
    v-if="canCopy"
    :value="name"
    :label="t('logs.copyCredential')"
    :resolve-value="connectionType === 'api_key' ? resolveSecret : undefined"
  />
  <AppOverflowText v-else :text="display" :class="{ 'modern-log-credential-deleted': deleted }" />
</template>

<style scoped>
.modern-log-credential-deleted {
  color: var(--modern-muted);
}
</style>
