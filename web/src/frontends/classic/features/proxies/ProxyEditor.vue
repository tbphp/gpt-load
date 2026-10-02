<script setup lang="ts">
import { Save } from '@lucide/vue'
import { computed, onMounted, onScopeDispose, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { proxyListKey, revealProxy, saveProxy, type ProxyItem } from '@shared/proxies/api'
import { isValidProxyURL } from '@/app/resources/proxy'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = defineProps<{ proxy?: ProxyItem }>()
const emit = defineEmits<{ close: []; saved: [proxy: ProxyItem] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const formID = useId()
const name = ref(props.proxy?.name ?? '')
const url = ref('')
const pending = ref(false)
const error = ref('')
const loading = ref(Boolean(props.proxy))
const loadFailed = ref(false)
const loadController = new AbortController()
const valid = computed(
  () => !loading.value && !loadFailed.value && isValidProxyURL(url.value.trim()),
)
async function load() {
  if (!props.proxy) return
  loading.value = true
  loadFailed.value = false
  try {
    const proxy = await revealProxy(client, props.proxy.id, loadController.signal)
    if (loadController.signal.aborted) return
    name.value = proxy.name
    url.value = proxy.url
  } catch {
    if (!loadController.signal.aborted) loadFailed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  if (props.proxy) void load()
})
onScopeDispose(() => {
  loadController.abort()
  url.value = ''
})
async function save() {
  if (pending.value || !valid.value) return
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
    :close-label="t('common.close')"
    :dismissible="!pending"
    show-description
    @update:open="!$event && emit('close')"
  >
    <form :id="formID" class="proxy-editor" @submit.prevent="save">
      <InlineFeedback v-if="loading">{{ t('common.asyncLoading') }}</InlineFeedback>
      <InlineFeedback v-else-if="loadFailed" tone="danger">
        {{ t('proxies.loadFailed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="load">{{ t('common.retry') }}</AppButton>
        </template>
      </InlineFeedback>
      <template v-else>
        <FormField :id="`${formID}-name`" :label="t('proxies.nameOptional')">
          <AppTextInput
            :id="`${formID}-name`"
            v-model="name"
            :label="t('proxies.nameOptional')"
            :placeholder="t('proxies.namePlaceholder')"
            :disabled="pending"
            maxlength="255"
            autocomplete="off"
          />
        </FormField>
        <FormField :id="`${formID}-address`" :label="t('proxies.address')" required>
          <AppTextInput
            :id="`${formID}-address`"
            v-model="url"
            :label="t('proxies.address')"
            placeholder="http://127.0.0.1:8080"
            :disabled="pending"
            type="text"
            required
            autocomplete="off"
            :spellcheck="false"
          />
        </FormField>
        <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
      </template>
    </form>
    <template #footer>
      <AppButton variant="secondary" size="compact" :disabled="pending" @click="emit('close')">
        {{ t('proxies.cancel') }}
      </AppButton>
      <AppButton type="submit" :form="formID" size="compact" :busy="pending" :disabled="!valid">
        <Save :size="16" aria-hidden="true" />{{ t('proxies.save') }}
      </AppButton>
    </template>
  </AppDrawer>
</template>

<style scoped>
.proxy-editor {
  display: grid;
  gap: var(--space-4);
}
</style>
