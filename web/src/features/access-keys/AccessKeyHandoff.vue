<script setup lang="ts">
import { X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'

import type { AccessKeyDto } from '@/api/control/types'
import { useApiClient } from '@/api/client-context'
import { revealAccessKey } from '@/app/resources/access-keys'
import { getHomeBase } from '@/app/resources/home'
import { homeLocation, loginLocation } from '@/app/route-locations'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { formatLocalInstant } from '@/lib/format'

const props = defineProps<{ accessKey: Pick<AccessKeyDto, 'id' | 'name' | 'expires_at_ms'> }>()
defineEmits<{ close: [] }>()
const { locale, t } = useI18n()
const router = useRouter()
const client = useApiClient()
const controllers = useAbortControllerPool()
const origin = window.location.origin
async function handoff(): Promise<string> {
  const controller = controllers.create()
  const key = props.accessKey
  try {
    const home = await getHomeBase(client, controller.signal)
    const result = await revealAccessKey(client, key.id, controller.signal)
    return t('accessKeys.distribution.handoffText', {
      name: key.name,
      login: new URL(router.resolve(loginLocation()).href, origin).href,
      baseUrl: origin,
      key: result.key,
      expires:
        key.expires_at_ms === null
          ? t('accessKeys.distribution.neverExpires')
          : formatLocalInstant(key.expires_at_ms, locale.value),
      contact: home.contact_info,
    })
  } finally {
    controllers.release(controller)
  }
}
</script>

<template>
  <div class="access-key-handoff" role="status">
    <strong>{{ t('accessKeys.distribution.created', { name: accessKey.name }) }}</strong>
    <div class="access-key-handoff__actions">
      <CopyChip
        :value="t('accessKeys.distribution.handoff')"
        :label="t('accessKeys.distribution.handoff')"
        :success-label="t('common.copied')"
        :failure-label="t('common.copyFailed')"
        :resolve-value="handoff"
      />
      <RouterLink :to="homeLocation({ access_key_id: String(accessKey.id) })">{{
        t('accessKeys.distribution.guide')
      }}</RouterLink>
      <IconButton size="compact" variant="ghost" :label="t('common.close')" @click="$emit('close')"
        ><X :size="14"
      /></IconButton>
    </div>
  </div>
</template>

<style scoped>
.access-key-handoff {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 0;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: var(--text-sm);
}
.access-key-handoff__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}
.access-key-handoff a {
  color: var(--color-action);
}
</style>
