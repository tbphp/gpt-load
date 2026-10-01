<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { proxyListKey, saveProxy, type ProxyItem } from '@shared/proxies/api'
import { validProxyURL } from '@modern/app/proxy'
import {
  AppButton,
  AppDialogContent,
  AppDialogHeader,
  AppNotice,
  AppTextField,
} from '@modern/components/ui'

const props = defineProps<{ proxy?: ProxyItem; compact?: boolean }>()
const emit = defineEmits<{ close: []; saved: [proxy: ProxyItem] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const name = ref(props.proxy?.name ?? '')
const url = ref('')
const pending = ref(false)
const error = ref('')
async function save() {
  if (
    pending.value ||
    !name.value.trim() ||
    ((!props.proxy || url.value.trim()) && !validProxyURL(url.value.trim()))
  )
    return
  pending.value = true
  error.value = ''
  try {
    const proxy = await saveProxy(
      client,
      { name: name.value.trim(), url: url.value.trim() },
      props.proxy?.id,
    )
    url.value = ''
    await cache.invalidateQueries({ queryKey: proxyListKey })
    emit('saved', proxy)
  } catch (cause) {
    error.value = t(
      cause instanceof ApiError && cause.code === 'DUPLICATE_RESOURCE'
        ? 'proxies.duplicate'
        : 'proxies.operationFailed',
    )
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && !pending && emit('close')">
    <AppDialogContent
      :title="t(proxy ? 'proxies.edit' : 'proxies.new')"
      :description="t('proxies.addressHelp')"
      :placement="compact ? 'dialog' : 'editor'"
      size="sheet"
    >
      <AppDialogHeader
        :close-label="t('proxies.cancel')"
        :title="t(proxy ? 'proxies.edit' : 'proxies.new')"
        @close="!pending && emit('close')"
      />
      <form class="modern-proxy-editor" @submit.prevent="save">
        <AppTextField
          v-model="name"
          :label="t('proxies.name')"
          :disabled="pending"
          required
          maxlength="255"
        />
        <AppTextField
          v-model="url"
          :label="t('proxies.address')"
          :placeholder="proxy ? t('proxies.keepAddress') : 'http://127.0.0.1:8080'"
          :disabled="pending"
          autocomplete="off"
          spellcheck="false"
        />
        <p>{{ t('proxies.addressHelp') }}</p>
        <p v-if="proxy">{{ proxy.display_url }}</p>
        <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
        <div class="modern-proxy-editor-actions">
          <AppButton :disabled="pending" variant="ghost" @click="emit('close')">{{
            t('proxies.cancel')
          }}</AppButton>
          <AppButton
            type="submit"
            :loading="pending"
            :disabled="
              !name.trim() || ((!proxy || Boolean(url.trim())) && !validProxyURL(url.trim()))
            "
            >{{ t('proxies.save') }}</AppButton
          >
        </div>
      </form>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-proxy-editor {
  display: grid;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
  overflow-y: auto;
}
.modern-proxy-editor p {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
.modern-proxy-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
}
</style>
