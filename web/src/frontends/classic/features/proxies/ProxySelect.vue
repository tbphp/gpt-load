<script setup lang="ts">
import { Plus, ExternalLink } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { listProxies, proxyListKey, proxyOptionLabel, type ProxyItem } from '@shared/proxies/api'
import AppCombobox from '@/components/ui/AppCombobox.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import ProxyEditor from './ProxyEditor.vue'

const props = defineProps<{
  disabled?: boolean
  savedId?: number
  savedName?: string
  savedAddress?: string
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
  return rows.map((row) => ({ value: String(row.id), label: proxyOptionLabel(row) }))
})
const selected = computed({
  get: () => model.value || (props.savedId ? String(props.savedId) : ''),
  set: (value: string) => {
    model.value = value
  },
})
const savedLabel = computed(() =>
  created.value && Number(model.value) === created.value.id
    ? proxyOptionLabel(created.value)
    : props.referenceState === 'disabled'
      ? t('proxies.referenceDisabled')
      : props.referenceState === 'deleted'
        ? t('proxies.referenceDeleted')
        : props.savedName || props.savedAddress || t('proxies.unknownReference'),
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
      size="sm"
      selection-only
      :selected-label="savedLabel"
      :disabled="disabled"
      :invalid="invalid"
      :described-by="describedBy"
    />
    <AppTooltip :content="t('proxies.new')">
      <IconButton
        :label="t('proxies.new')"
        size="xs"
        variant="ghost"
        :disabled="disabled"
        @click="adding = true"
      >
        <Plus :size="14" aria-hidden="true" />
      </IconButton>
    </AppTooltip>
    <AppTooltip :content="t('proxies.manage')">
      <a href="/proxies" target="_blank" rel="noopener" :aria-label="t('proxies.manage')">
        <ExternalLink :size="14" aria-hidden="true" />
      </a>
    </AppTooltip>
    <ProxyEditor v-if="adding" @close="adding = false" @saved="saved" />
  </div>
</template>

<style scoped>
.proxy-selector {
  --control-sm: var(--setting-control-height);
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  width: 100%;
}
.proxy-selector > :first-child {
  min-width: 0;
}
.proxy-selector a {
  display: inline-flex;
  width: 28px;
  height: 28px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  color: var(--color-text-muted);
  text-decoration: none;
}
.proxy-selector a:hover {
  background: var(--color-action-soft);
  color: var(--color-action);
}
.proxy-selector a:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
@media (max-width: 860px) {
  .proxy-selector a {
    width: var(--touch-target);
    height: var(--touch-target);
  }
}
</style>
