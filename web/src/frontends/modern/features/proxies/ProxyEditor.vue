<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { proxyListKey, revealProxy, saveProxy, type ProxyItem } from '@shared/proxies/api'
import { validProxyURL } from '@modern/app/proxy'
import {
  AppButton,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppNotice,
  AppTextField,
} from '@modern/components/ui'

const props = defineProps<{ proxy?: ProxyItem }>()
const emit = defineEmits<{ close: []; saved: [proxy: ProxyItem] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const name = ref(props.proxy?.name ?? '')
const url = ref('')
const pending = ref(false)
const error = ref('')
const loading = ref(Boolean(props.proxy))
const loadFailed = ref(false)
const loadController = new AbortController()
const valid = computed(() => !loading.value && !loadFailed.value && validProxyURL(url.value.trim()))
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
  <DialogRoot :open="true" @update:open="!$event && !pending && emit('close')">
    <AppDialogContent
      :title="t(proxy ? 'proxies.edit' : 'proxies.new')"
      :description="t('proxies.addressHelp')"
      placement="editor"
    >
      <AppDialogHeader
        :close-label="t('ui.close')"
        :title="t(proxy ? 'proxies.edit' : 'proxies.new')"
        :close-disabled="pending"
        @close="!pending && emit('close')"
      />
      <form class="modern-proxy-editor" @submit.prevent="save">
        <div class="modern-proxy-editor-body">
          <AppCollectionState v-if="loading" :title="t('ui.loading')" loading />
          <AppCollectionState v-else-if="loadFailed" :title="t('proxies.loadFailed')" error>
            <AppButton size="sm" @click="load">{{ t('ui.retry') }}</AppButton>
          </AppCollectionState>
          <template v-else>
            <AppTextField
              v-model="name"
              :label="t('proxies.nameOptional')"
              :placeholder="t('proxies.namePlaceholder')"
              :disabled="pending"
              size="sm"
              maxlength="255"
              autocomplete="off"
            />
            <AppTextField
              v-model="url"
              :label="t('proxies.address')"
              placeholder="http://127.0.0.1:8080"
              :description="t('proxies.addressHelp')"
              :disabled="pending"
              type="text"
              required
              size="sm"
              autocomplete="off"
              spellcheck="false"
            />
            <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
          </template>
        </div>
        <footer class="modern-proxy-editor-footer">
          <AppButton size="sm" :disabled="pending" variant="ghost" @click="emit('close')">{{
            t('proxies.cancel')
          }}</AppButton>
          <AppButton
            type="submit"
            variant="primary"
            size="sm"
            :loading="pending"
            :disabled="!valid"
            >{{ t('proxies.save') }}</AppButton
          >
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-proxy-editor {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-proxy-editor-body {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
}
.modern-proxy-editor-footer {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: flex-end;
  gap: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-3) var(--modern-space-5);
}
</style>
