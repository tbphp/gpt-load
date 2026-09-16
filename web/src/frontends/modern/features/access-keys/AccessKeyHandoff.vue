<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  getAccessKey,
  revealAccessKey,
  type AccessFilters,
  type AccessKey,
} from '@modern/api/access-keys'
import {
  AppButton,
  AppCollectionState,
  AppCopyValue,
  AppDialogContent,
  AppDialogHeader,
  AppTextArea,
} from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { accessTime } from './access-key-display'
const props = defineProps<{ id: number; hint: AccessFilters }>()
defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const client = useApiClient()
const router = useRouter()
const key = ref('')
const row = ref<AccessKey>()
const loading = ref(true)
const failed = ref(false)
let controller: AbortController | undefined
const endpoint = window.location.origin
const login = new URL(router.resolve({ name: 'modern-login' }).href, endpoint).href
const instructions = computed(() =>
  row.value && key.value
    ? t('accessKeys.handoffText', {
        name: row.value.name,
        key: key.value,
        endpoint,
        login,
        expires:
          row.value.expires_at_ms === null
            ? t('accessKeys.never')
            : accessTime(row.value.expires_at_ms, locale.value),
      })
    : '',
)
async function load(): Promise<void> {
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  failed.value = false
  key.value = ''
  try {
    const [metadata, secret] = await Promise.all([
      getAccessKey(client, props.id, request.signal, props.hint),
      revealAccessKey(client, props.id, request.signal),
    ])
    if (request.signal.aborted) return
    if (!metadata) {
      failed.value = true
      return
    }
    row.value = metadata
    key.value = secret
  } catch {
    if (!request.signal.aborted) failed.value = true
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}
onMounted(load)
onScopeDispose(() => {
  controller?.abort()
  key.value = ''
})
useLoadingActivity(loading)
</script>
<template>
  <DialogRoot
    :open="true"
    @update:open="
      (open) => {
        if (!open) $emit('close')
      }
    "
  >
    <AppDialogContent
      :title="t('accessKeys.handoffTitle')"
      :description="row?.name ?? t('accessKeys.handoff')"
    >
      <AppDialogHeader
        :title="t('accessKeys.handoffTitle')"
        :description="row?.name"
        :close-label="t('ui.close')"
        @close="$emit('close')"
      />
      <div class="modern-access-handoff-body">
        <AppCollectionState v-if="loading" :title="t('collection.loading')" loading />
        <AppCollectionState v-else-if="failed" :title="t('accessKeys.loadFailed')" error
          ><AppButton @click="load">{{ t('ui.retry') }}</AppButton></AppCollectionState
        >
        <template v-else>
          <dl class="modern-access-handoff-fields">
            <div>
              <dt>{{ t('accessKeys.key') }}</dt>
              <dd><AppCopyValue :value="key" :label="t('accessKeys.copy')" wrap /></dd>
            </div>
            <div>
              <dt>{{ t('accessKeys.endpoint') }}</dt>
              <dd><AppCopyValue :value="endpoint" :label="t('ui.copy.action')" /></dd>
            </div>
            <div>
              <dt>{{ t('accessKeys.login') }}</dt>
              <dd><AppCopyValue :value="login" :label="t('ui.copy.action')" /></dd>
            </div>
          </dl>
          <AppCopyValue
            :value="instructions"
            :display="t('accessKeys.handoff')"
            :label="t('accessKeys.copyHandoff')"
          />
          <AppTextArea
            :model-value="instructions"
            :label="t('accessKeys.handoff')"
            label-hidden
            readonly
            :rows="7"
            spellcheck="false"
          />
        </template>
      </div>
      <footer class="modern-access-handoff-footer">
        <AppButton size="sm" @click="$emit('close')">{{ t('ui.close') }}</AppButton>
      </footer>
    </AppDialogContent>
  </DialogRoot>
</template>
<style scoped>
.modern-access-handoff-body {
  display: grid;
  gap: var(--modern-space-3);
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-5);
}
.modern-access-handoff-fields {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
}
.modern-access-handoff-fields dt {
  margin-bottom: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-handoff-fields dd {
  margin: 0;
  min-width: 0;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
}
.modern-access-handoff-footer {
  display: flex;
  justify-content: flex-end;
  flex: none;
  padding: var(--modern-space-3) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
</style>
