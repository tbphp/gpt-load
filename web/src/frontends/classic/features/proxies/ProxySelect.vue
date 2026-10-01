<script setup lang="ts">
import { Plus, ExternalLink } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { listProxies, proxyListKey, type ProxyItem } from '@shared/proxies/api'
import AppCombobox from '@/components/ui/AppCombobox.vue'
import IconButton from '@/components/ui/IconButton.vue'
import ProxyEditor from './ProxyEditor.vue'

const props = defineProps<{
  disabled?: boolean
  savedId?: number
  savedName?: string
  referenceState?: string
  invalid?: boolean
  describedBy?: string
  id?: string
}>()
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const client = useApiClient()
const adding = ref(false)
const created = ref<ProxyItem>()
const query = useQuery({
  queryKey: [...proxyListKey, 'options'],
  queryFn: ({ signal }) => listProxies(client, { state: 'enabled' }, signal, true),
})
const options = computed(() => {
  const rows = query.data.value?.items ?? []
  return rows.map((row) => ({ value: String(row.id), label: `${row.name} · ${row.display_url}` }))
})
const selected = computed({
  get: () => model.value || (props.savedId ? String(props.savedId) : ''),
  set: (value: string) => {
    model.value = value
  },
})
const savedLabel = computed(() =>
  created.value && Number(model.value) === created.value.id
    ? `${created.value.name} · ${created.value.display_url}`
    : props.referenceState === 'disabled'
      ? t('proxies.referenceDisabled')
      : props.referenceState === 'deleted'
        ? t('proxies.referenceDeleted')
        : (props.savedName ?? t('proxies.unknownReference')),
)
function saved(proxy: ProxyItem) {
  created.value = proxy
  model.value = String(proxy.id)
  adding.value = false
}
</script>

<template>
  <div class="proxy-selector">
    <AppCombobox
      :id="id"
      v-model="selected"
      :label="t('proxies.select')"
      :placeholder="t('proxies.selectHelp')"
      :options="options"
      :empty-text="t(query.isError.value ? 'proxies.loadFailed' : 'proxies.noResults')"
      selection-only
      :selected-label="savedLabel"
      :disabled="disabled"
      :invalid="invalid"
      :described-by="describedBy"
    />
    <IconButton :label="t('proxies.new')" :disabled="disabled" @click="adding = true"
      ><Plus :size="16"
    /></IconButton>
    <a href="/proxies" target="_blank" rel="noopener" :aria-label="t('proxies.manage')"
      ><ExternalLink :size="16" /><span>{{ t('proxies.manage') }}</span></a
    >
    <ProxyEditor v-if="adding" @close="adding = false" @saved="saved" />
  </div>
</template>

<style scoped>
.proxy-selector {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  flex-wrap: wrap;
}
.proxy-selector > :first-child {
  flex: 1 1 200px;
  min-width: 0;
}
.proxy-selector a {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-text-muted);
  text-decoration: none;
  font-size: var(--text-label-sm);
}
</style>
