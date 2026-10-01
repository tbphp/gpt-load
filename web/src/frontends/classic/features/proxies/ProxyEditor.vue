<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { proxyListKey, saveProxy, type ProxyItem } from '@shared/proxies/api'
import { isValidProxyURL } from '@/app/resources/proxy'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'

const props = defineProps<{ proxy?: ProxyItem }>()
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
    ((!props.proxy || url.value.trim()) && !isValidProxyURL(url.value.trim()))
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
  <AppDrawer
    :open="true"
    :title="t(proxy ? 'proxies.edit' : 'proxies.new')"
    :description="t('proxies.addressHelp')"
    :close-label="t('proxies.cancel')"
    :dismissible="!pending"
    @update:open="!$event && emit('close')"
  >
    <form class="proxy-editor" @submit.prevent="save">
      <FormField id="proxy-name" :label="t('proxies.name')"
        ><AppTextInput
          id="proxy-name"
          v-model="name"
          :label="t('proxies.name')"
          :disabled="pending"
          required
          maxlength="255"
      /></FormField>
      <FormField id="proxy-address" :label="t('proxies.address')"
        ><AppTextInput
          id="proxy-address"
          v-model="url"
          :label="t('proxies.address')"
          :placeholder="proxy ? t('proxies.keepAddress') : 'http://127.0.0.1:8080'"
          :disabled="pending"
          autocomplete="off"
          :spellcheck="false"
      /></FormField>
      <p>{{ t('proxies.addressHelp') }}</p>
      <p v-if="proxy">{{ proxy.display_url }}</p>
      <p v-if="error" role="alert" class="proxy-editor-error">{{ error }}</p>
      <div class="proxy-editor-actions">
        <AppButton variant="secondary" :disabled="pending" @click="emit('close')">{{
          t('proxies.cancel')
        }}</AppButton
        ><AppButton
          type="submit"
          :busy="pending"
          :disabled="
            !name.trim() || ((!proxy || Boolean(url.trim())) && !isValidProxyURL(url.trim()))
          "
          >{{ t('proxies.save') }}</AppButton
        >
      </div>
    </form>
  </AppDrawer>
</template>

<style scoped>
.proxy-editor {
  display: grid;
  gap: var(--space-4);
}
.proxy-editor p {
  margin: 0;
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
}
.proxy-editor .proxy-editor-error {
  color: var(--color-danger);
}
.proxy-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
