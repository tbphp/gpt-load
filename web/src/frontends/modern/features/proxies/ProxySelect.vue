<script setup lang="ts">
import { ExternalLink, Plus } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { listProxies, proxyOptionLabel, type ProxyItem } from '@shared/proxies/api'
import {
  AppButton,
  AppIcon,
  AppIconButton,
  AppSearchSelect,
  type ControlSize,
} from '@modern/components/ui'
import ProxyEditor from './ProxyEditor.vue'

const props = defineProps<{
  disabled?: boolean
  savedId?: number
  savedName?: string
  savedAddress?: string
  referenceState?: string
  error?: string
  labelHidden?: boolean
  size?: ControlSize
}>()
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const client = useApiClient()
const adding = ref(false)
const select = ref<InstanceType<typeof AppSearchSelect>>()
defineExpose({ focus: () => select.value?.focus() })
const names = ref(new Map<string, string>())
const stateLabel = computed(() =>
  props.referenceState === 'disabled'
    ? t('proxies.referenceDisabled')
    : props.referenceState === 'deleted'
      ? t('proxies.referenceDeleted')
      : props.savedName || props.savedAddress || undefined,
)
const selectedOption = computed(() => {
  const value = model.value || (props.savedId ? String(props.savedId) : '')
  return value
    ? {
        value,
        label:
          names.value.get(value) ??
          (Number(value) === props.savedId ? stateLabel.value : undefined) ??
          t('proxies.unknownReference'),
      }
    : undefined
})
const selected = computed({
  get: () => model.value || (props.savedId ? String(props.savedId) : ''),
  set: (value: string) => {
    model.value = value
  },
})
async function loadOptions(q: string, signal: AbortSignal) {
  const result = await listProxies(client, { q, state: 'enabled', page_size: 100 }, signal)
  return result.items.map((proxy) => {
    const label = proxyOptionLabel(proxy)
    names.value.set(String(proxy.id), label)
    return { value: String(proxy.id), label }
  })
}
function saved(proxy: ProxyItem) {
  names.value.set(String(proxy.id), proxyOptionLabel(proxy))
  model.value = String(proxy.id)
  adding.value = false
}
</script>

<template>
  <div class="modern-proxy-select">
    <AppSearchSelect
      ref="select"
      v-model="selected"
      :label="t('proxies.select')"
      :label-hidden="labelHidden"
      :size="size"
      :placeholder="t('proxies.selectHelp')"
      :load-options="loadOptions"
      :selected-option="selectedOption"
      :disabled="disabled"
      :error="error"
    />
    <AppIconButton
      :icon="Plus"
      :label="t('proxies.new')"
      :size="size"
      :disabled="disabled"
      @click="adding = true"
    />
    <AppButton as-child variant="ghost" :size="size"
      ><a href="/proxies" target="_blank" rel="noopener"
        >{{ t('proxies.manage') }}<AppIcon :icon="ExternalLink" size="sm" /></a
    ></AppButton>
    <ProxyEditor v-if="adding" @close="adding = false" @saved="saved" />
  </div>
</template>

<style scoped>
.modern-proxy-select {
  display: flex;
  align-items: end;
  gap: var(--modern-space-2);
  flex-wrap: wrap;
  min-width: 0;
}
.modern-proxy-select > :first-child {
  flex: 1 1 220px;
  min-width: 0;
}
</style>
