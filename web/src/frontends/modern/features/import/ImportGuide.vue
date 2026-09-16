<script setup lang="ts">
import { CirclePlus } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import {
  AppChannelIcon,
  AppConfirmDialog,
  AppSearchSelect,
  AppSegmentedControl,
} from '@modern/components/ui'
import { useURLState } from '@modern/app/url-state'
import { useMessageSource } from '@modern/app/messages'
import { useApiClient } from '@shared/http/client-context'
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const client = useApiClient()
const router = useRouter()
const view = useURLState(
  ['import_mode', 'import_group'],
  (query) => ({
    mode: query.import_mode === 'new' ? 'new' : 'existing',
    group: typeof query.import_group === 'string' ? query.import_group : '',
  }),
  (value) => ({
    ...(value.mode === 'new' ? { import_mode: 'new' } : {}),
    ...(value.group ? { import_group: value.group } : {}),
  }),
)
const mode = computed({
  get: () => view.value.mode,
  set: (mode: string) => {
    view.value = { ...view.value, mode }
  },
})
const groupID = computed({
  get: () => view.value.group,
  set: (group: string) => {
    view.value = { ...view.value, group }
  },
})
const pending = ref(false)
const query = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
})
const groups = computed(
  () => new Map(query.data.value?.items.map((group) => [String(group.id), group])),
)
const options = computed(() =>
  [...groups.value].map(([value, group]) => ({
    value,
    label: group.name,
    keywords: [group.channelName, group.channelID],
  })),
)
const modes = computed(() => [
  { value: 'existing', label: t('groupWorkflows.importExisting') },
  { value: 'new', label: t('groupWorkflows.importNew') },
])
async function proceed(): Promise<void> {
  if (pending.value || (mode.value === 'existing' && !groups.value.has(groupID.value))) return
  pending.value = true
  try {
    await router.push(
      mode.value === 'new'
        ? { name: 'modern-groups', query: { panel: 'create' } }
        : { name: 'modern-group-detail', params: { id: groupID.value }, query: { panel: 'add' } },
    )
  } finally {
    pending.value = false
  }
}
useMessageSource(() =>
  query.isError.value
    ? {
        text: t('groups.edit.loadFailed'),
        tone: 'danger',
        action: { label: t('ui.retry'), run: () => query.refetch() },
      }
    : undefined,
)
</script>
<template>
  <AppConfirmDialog
    open
    :icon="CirclePlus"
    :title="t('shell.importCredentials')"
    :confirm-label="t('groupWorkflows.openImport')"
    :pending="pending"
    :disabled="mode === 'existing' && !groups.has(groupID)"
    @cancel="emit('close')"
    @confirm="proceed"
  >
    <AppSegmentedControl
      v-model="mode"
      :label="t('groupWorkflows.importTarget')"
      :options="modes"
      appearance="field"
      size="sm"
      :disabled="pending"
    />
    <AppSearchSelect
      v-if="mode === 'existing'"
      v-model="groupID"
      :label="t('groupWorkflows.chooseGroup')"
      :options="options"
      :disabled="query.isPending.value || pending"
    >
      <template #option="{ option }">
        <AppChannelIcon
          :icon="groups.get(option.value)?.channelIcon"
          :mark="groups.get(option.value)?.channelMark"
          :name="groups.get(option.value)?.channelName"
          size="sm"
        />
        <span>{{ option.label }}</span>
      </template>
    </AppSearchSelect>
  </AppConfirmDialog>
</template>
