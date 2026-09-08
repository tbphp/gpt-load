<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import type { AccessKeyDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { revealAccessKey } from '@/app/resources/access-keys'
import { loginLocation } from '@/app/route-locations'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import { formatLocalInstant } from '@/lib/format'

const props = defineProps<{ accessKey: Pick<AccessKeyDto, 'id' | 'name' | 'expires_at_ms'> }>()
const emit = defineEmits<{ close: [] }>()
const { locale, t } = useI18n()
const router = useRouter()
const client = useApiClient()
const controllers = useAbortControllerPool()
const secret = ref('')
const loading = ref(true)
const failed = ref(false)
const origin = window.location.origin
const login = new URL(router.resolve(loginLocation()).href, origin).href
const handoffText = computed(() =>
  t('accessKeys.distribution.handoffText', {
    name: props.accessKey.name,
    login,
    baseUrl: origin,
    key: secret.value,
    expires:
      props.accessKey.expires_at_ms === null
        ? t('accessKeys.distribution.neverExpires')
        : formatLocalInstant(props.accessKey.expires_at_ms, locale.value),
  }),
)

async function loadKey(): Promise<void> {
  loading.value = true
  failed.value = false
  const controller = controllers.create()
  try {
    secret.value = (await revealAccessKey(client, props.accessKey.id, controller.signal)).key
  } catch (error: unknown) {
    if (!(error instanceof RequestCancelledError)) failed.value = true
  } finally {
    controllers.release(controller)
    loading.value = false
  }
}
onMounted(loadKey)
</script>

<template>
  <AppDialog
    :open="true"
    :title="t('accessKeys.distribution.created', { name: accessKey.name })"
    :close-label="t('common.close')"
    @update:open="!$event && emit('close')"
  >
    <template #body>
      <p v-if="loading" class="access-key-handoff__loading" role="status">
        {{ t('accessKeys.distribution.handoffLoading') }}
      </p>
      <QueryFeedback
        v-else-if="failed"
        state="error"
        :message="t('accessKeys.distribution.handoffFailed')"
        :retry-label="t('common.retry')"
        @retry="loadKey"
      />
      <div v-else class="access-key-handoff__body">
        <dl class="access-key-handoff__fields">
          <div>
            <dt>API Key</dt>
            <dd>
              <code>{{ secret }}</code>
              <CopyChip
                layout="icon"
                :value="secret"
                :label="t('accessKeys.copy')"
                :success-label="t('common.copied')"
                :failure-label="t('common.copyFailed')"
              />
            </dd>
          </div>
        </dl>
        <section class="access-key-handoff__instructions">
          <header>
            <label for="access-key-handoff-text">{{
              t('accessKeys.distribution.instructions')
            }}</label>
            <CopyChip
              layout="icon"
              :value="handoffText"
              :label="t('accessKeys.distribution.handoff')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
            />
          </header>
          <textarea id="access-key-handoff-text" :value="handoffText" rows="8" readonly />
        </section>
      </div>
    </template>
    <template #footer>
      <AppButton variant="secondary" size="compact" @click="emit('close')">{{
        t('common.close')
      }}</AppButton>
    </template>
  </AppDialog>
</template>

<style scoped>
.access-key-handoff__body {
  display: grid;
  gap: 18px;
}
.access-key-handoff__fields {
  display: grid;
  gap: 12px;
  margin: 0;
}
.access-key-handoff__fields dt,
.access-key-handoff__instructions label {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
.access-key-handoff__fields dd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin: 4px 0 0;
}
.access-key-handoff__fields code {
  min-width: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
.access-key-handoff__fields :deep(.copy-chip-wrap) {
  flex-shrink: 0;
}
.access-key-handoff__instructions {
  display: grid;
  gap: 6px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 12px;
}
.access-key-handoff__instructions header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.access-key-handoff__instructions textarea {
  box-sizing: border-box;
  width: 100%;
  resize: vertical;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 10px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: 1.6;
}
.access-key-handoff__loading {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
</style>
