<script setup lang="ts">
import { ArrowLeft, ChevronRight, Layers, SlidersHorizontal } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useMessages, useMessageSource } from '@modern/app/messages'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { getGroupChannels, type GroupCreateResult } from '@modern/api/group-create'
import {
  getGroupModels,
  groupModelsKey,
  groupSettingsKey,
  groupCredentialsKey,
} from '@modern/api/group-detail'
import { getGroupUsage, getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { getGroupUsageTrend } from '@modern/api/group-usage-trend'
import { usePageRefresh } from '@modern/app/page-refresh'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppCopyValue,
  AppIcon,
  AppIconButton,
  AppProtocolTag,
} from '@modern/components/ui'
import type { SemanticTone } from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import GroupAdvancedPanel from './GroupAdvancedPanel.vue'
import GroupBasicsForm from './GroupBasicsForm.vue'
import GroupCredentialAddPanel from './GroupCredentialAddPanel.vue'
import GroupCredentials from './GroupCredentials.vue'
import GroupModelsPanel from './GroupModelsPanel.vue'
import GroupOverview from './GroupOverview.vue'

const route = useRoute()
const router = useRouter()
const { t, n } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const id = computed(() => Number(route.params.id))
const valid = computed(() => Number.isSafeInteger(id.value) && id.value > 0)
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
  enabled: valid,
})
const channels = useQuery({
  queryKey: ['modern', 'group-channels'],
  queryFn: ({ signal }) => getGroupChannels(client, signal),
  enabled: valid,
})
const group = computed(() => groups.data.value?.items.find((item) => item.id === id.value))
const channel = computed(() =>
  channels.data.value?.find((item) => item.id === group.value?.channelID),
)
const models = useQuery(
  computed(() => ({
    queryKey: groupModelsKey(id.value),
    queryFn: ({ signal }: { signal: AbortSignal }) => getGroupModels(client, id.value, signal),
    enabled: Boolean(group.value),
  })),
)
const usage = useQuery(
  computed(() => ({
    queryKey: ['modern', 'group-overview-usage', id.value],
    queryFn: ({ signal }: { signal: AbortSignal }) => getGroupUsage(client, [id.value], signal),
    enabled: Boolean(group.value),
  })),
)
const groupUsage = computed(() => usage.data.value?.items.find((item) => item.id === id.value))
const trend = useQuery(
  computed(() => ({
    queryKey: ['modern', 'group-usage-trend', id.value],
    queryFn: ({ signal }: { signal: AbortSignal }) => getGroupUsageTrend(client, id.value, signal),
    enabled: Boolean(group.value),
  })),
)
const credentials = ref<InstanceType<typeof GroupCredentials>>()
const basics = ref<InstanceType<typeof GroupBasicsForm>>()
const credentialsPending = ref(false)
const credentialsUpdatedAt = ref(0)
const basicsPending = ref(false)
const basicsUpdatedAt = ref(0)
const messages = useMessages()
let panelUpdate: ReturnType<typeof router.replace> | undefined
let panelTarget: string | undefined
const panel = computed({
  get: () =>
    ['models', 'advanced', 'add'].includes(String(route.query.panel))
      ? (route.query.panel as 'models' | 'advanced' | 'add')
      : undefined,
  set: (value) => {
    void setPanel(value)
  },
})
function setPanel(value?: 'models' | 'advanced' | 'add') {
  if (panelUpdate && panelTarget === value) return panelUpdate
  const query = { ...route.query }
  if (value) query.panel = value
  else {
    delete query.panel
    delete query.pick_models
    delete query.sync_models
    delete query.sync_mode
  }
  panelTarget = value
  const operation = router.replace({ query })
  panelUpdate = operation
  const clear = () => {
    if (panelUpdate === operation) panelUpdate = undefined
  }
  void operation.then(clear, clear)
  return operation
}
const tone = computed<SemanticTone>(() =>
  group.value?.availability === 'ready'
    ? 'success'
    : group.value?.availability === 'limited'
      ? 'warning'
      : group.value?.availability === 'paused'
        ? 'neutral'
        : 'danger',
)
const returnTo = computed(() => {
  const from = route.query.from
  return typeof from === 'string' && /^\/groups(?:\?|$)/u.test(from) ? from : '/groups'
})
async function refreshUsage(): Promise<void> {
  await Promise.all([usage.refetch(), trend.refetch()])
}
async function refresh(): Promise<void> {
  await Promise.all([
    groups.refetch(),
    channels.refetch(),
    models.refetch(),
    usage.refetch(),
    trend.refetch(),
    credentials.value?.refresh(),
    basics.value?.refresh(),
  ])
}
usePageRefresh({
  refresh,
  pending: () =>
    groups.isFetching.value ||
    models.isFetching.value ||
    usage.isFetching.value ||
    trend.isFetching.value ||
    basicsPending.value ||
    credentialsPending.value,
  updatedAt: () =>
    Math.max(
      groups.dataUpdatedAt.value,
      models.dataUpdatedAt.value,
      usage.dataUpdatedAt.value,
      trend.dataUpdatedAt.value,
      basicsUpdatedAt.value,
      credentialsUpdatedAt.value,
    ) || undefined,
})
function settingsSaved(): void {
  void cache.invalidateQueries({ queryKey: groupQueryKey })
  void cache.invalidateQueries({ queryKey: groupCredentialsKey(id.value) })
  void cache.invalidateQueries({ queryKey: groupSettingsKey(id.value) })
}
function modelsSaved(): void {
  void cache.invalidateQueries({ queryKey: groupQueryKey })
  void cache.invalidateQueries({ queryKey: groupCredentialsKey(id.value) })
}
async function groupDeleted(): Promise<void> {
  const deletedID = id.value
  await router.replace(returnTo.value)
  for (const key of [
    'group-settings',
    'group-models',
    'group-model-names',
    'group-credentials',
    'credential-detail',
    'credential-trends',
    'group-overview-usage',
    'group-usage-trend',
  ]) {
    await cache.cancelQueries({ queryKey: ['modern', key, deletedID] })
    cache.removeQueries({ queryKey: ['modern', key, deletedID] })
  }
  void cache.invalidateQueries({ queryKey: groupQueryKey })
}
async function added(result: GroupCreateResult): Promise<void> {
  await setPanel(undefined)
  messages.show({
    tone: 'success',
    text: t('groupDetail.importResult', {
      added: n(result.added),
      duplicated: n(result.duplicated),
    }),
  })
  void cache.invalidateQueries({ queryKey: groupCredentialsKey(id.value) })
  void cache.invalidateQueries({ queryKey: groupQueryKey })
}
useMessageSource(() =>
  groups.isError.value && group.value
    ? {
        text: t('groupDetail.refreshFailed'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: refresh },
      }
    : undefined,
)
useMessageSource(() =>
  channels.isError.value
    ? {
        text: t('groupDetail.channelFailed'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: () => channels.refetch() },
      }
    : undefined,
)
</script>

<template>
  <div class="modern-group-workspace">
    <AppCollectionState
      v-if="valid && groups.isPending.value"
      :title="t('collection.loading')"
      loading
    />
    <AppCollectionState
      v-else-if="!group"
      :title="t(groups.isError.value ? 'groups.edit.loadFailed' : 'groupDetail.notFound')"
      :error="groups.isError.value"
    >
      <AppButton v-if="groups.isError.value" @click="groups.refetch()">{{
        t('ui.retry')
      }}</AppButton>
      <AppButton as-child
        ><RouterLink :to="returnTo">{{ t('groupDetail.back') }}</RouterLink></AppButton
      >
    </AppCollectionState>
    <div v-else class="modern-group-workspace-layout">
      <div class="modern-group-workspace-main">
        <header class="modern-group-workspace-header">
          <AppIconButton
            :icon="ArrowLeft"
            :label="t('groupDetail.back')"
            @click="router.push(returnTo)"
          />
          <AppChannelIcon
            :icon="group.channelIcon"
            :mark="group.channelMark"
            :name="group.channelName"
            size="hero"
            surface
          />
          <div class="modern-group-workspace-identity">
            <div class="modern-group-workspace-name">
              <h1>{{ group.name }}</h1>
              <AppBadge :tone="tone" class="modern-group-workspace-status" dot>{{
                t('groups.row.state.' + group.availability)
              }}</AppBadge>
              <AppBadge variant="outline">{{
                t('groups.connection.' + group.connectionType)
              }}</AppBadge>
            </div>
            <div class="modern-group-workspace-meta">
              <AppCopyValue
                v-if="group.endpoint"
                class="modern-group-workspace-endpoint"
                :value="group.endpoint"
                :label="t('groups.copyURL')"
              />
              <span v-else>{{ group.channelName }}</span>
              <span class="modern-group-workspace-routing"
                >{{ t('groupDetail.weightValue', { value: n(group.weight) })
                }}<span aria-hidden="true">·</span
                >{{ t('groups.board.price', { value: group.priceMultiplier }) }}</span
              >
            </div>
            <div v-if="channel?.nativeProtocols.length" class="modern-group-workspace-protocols">
              <span>{{ t('groupDetail.supportedProtocols') }}</span>
              <div>
                <AppProtocolTag
                  v-for="protocol in channel.nativeProtocols"
                  :key="protocol"
                  :protocol="protocol"
                />
              </div>
            </div>
          </div>
        </header>
        <GroupCredentials
          :key="group.id"
          ref="credentials"
          :group="group"
          :channel="channel"
          @add="panel = 'add'"
          @changed="cache.invalidateQueries({ queryKey: groupQueryKey })"
          @pending="credentialsPending = $event"
          @updated-at="credentialsUpdatedAt = $event"
        />
      </div>
      <aside class="modern-group-workspace-sidebar" :aria-label="t('groupDetail.settings')">
        <GroupBasicsForm
          :key="group.id"
          ref="basics"
          :group="group"
          :operation-pending="credentialsPending"
          @deleted="groupDeleted"
          @saved="settingsSaved"
          @pending="basicsPending = $event"
          @updated-at="basicsUpdatedAt = $event"
        >
          <template #overview
            ><GroupOverview
              :group="group"
              :usage="groupUsage"
              :trend="trend.data.value"
              :loading="usage.isFetching.value || trend.isFetching.value"
              :incomplete="usage.data.value?.incomplete ?? false"
              :failed="usage.isError.value || trend.isError.value"
              @retry="refreshUsage"
          /></template>
          <nav class="modern-group-settings-links" :aria-label="t('groupDetail.settings')">
            <AppButton
              :icon="Layers"
              variant="ghost"
              class="modern-group-settings-link"
              @click="panel = 'models'"
              ><span>{{ t('groupDetail.modelsAndAliases') }}</span
              ><small>{{ n(models.data.value?.length ?? group.modelCount) }}</small
              ><AppIcon :icon="ChevronRight" size="sm"
            /></AppButton>
            <AppButton
              :icon="SlidersHorizontal"
              variant="ghost"
              class="modern-group-settings-link"
              @click="panel = 'advanced'"
              ><span>{{ t('groupDetail.advanced') }}</span
              ><AppIcon :icon="ChevronRight" size="sm"
            /></AppButton>
          </nav>
        </GroupBasicsForm>
      </aside>
      <GroupModelsPanel
        v-if="panel === 'models'"
        :key="group.id"
        :group="group"
        :channel="channel"
        @close="panel = undefined"
        @saved="modelsSaved"
      />
      <GroupAdvancedPanel
        v-if="panel === 'advanced'"
        :key="group.id"
        :group="group"
        :channel="channel"
        :models="models.data.value ?? []"
        @close="panel = undefined"
        @saved="settingsSaved"
      />
      <GroupCredentialAddPanel
        v-if="panel === 'add' && channel"
        :key="group.id"
        :group="group"
        :channel="channel"
        @close="panel = undefined"
        @saved="added"
      />
    </div>
  </div>
</template>

<style scoped>
.modern-group-workspace {
  container: modern-group-workspace / inline-size;
  display: flex;
  flex: 1;
  min-height: 0;
  min-width: 0;
}
.modern-group-workspace-layout {
  display: grid;
  flex: 1;
  min-height: 0;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) var(--modern-context-sidebar);
  gap: var(--modern-space-5);
}
.modern-group-workspace-main {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
  min-width: 0;
  min-height: 0;
}
.modern-group-workspace-header {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-4);
}
.modern-group-workspace-identity {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  flex: 1;
  padding-left: var(--modern-space-1);
}
.modern-group-workspace-name,
.modern-group-workspace-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-group-workspace-protocols {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-workspace-protocols > div {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-group-workspace-name h1 {
  overflow-wrap: anywhere;
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  letter-spacing: var(--modern-tracking-title);
}
.modern-group-workspace-status {
  border-radius: var(--modern-badge-sm);
}
.modern-group-workspace-meta {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  column-gap: var(--modern-space-3);
}
.modern-group-workspace-endpoint {
  max-width: 100%;
  font-family: var(--modern-font-mono);
}
.modern-group-workspace-routing {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-1-5);
  font-variant-numeric: tabular-nums;
}
.modern-group-workspace-sidebar {
  display: flex;
  min-width: 0;
  min-height: 0;
  border-left: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
}
.modern-group-settings-links {
  display: grid;
  gap: var(--modern-space-1);
  padding-top: var(--modern-space-3);
  margin-top: var(--modern-space-1);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-settings-link {
  width: 100%;
  justify-content: flex-start;
  gap: var(--modern-space-2);
  padding-inline: var(--modern-space-1);
}
.modern-group-settings-link > span {
  flex: 1;
  text-align: left;
}
.modern-group-settings-link > small {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-group-settings-link > :last-child {
  flex: none;
  color: var(--modern-muted);
}
@container modern-group-workspace (max-width: 980px) {
  .modern-group-workspace-layout {
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    gap: var(--modern-space-5);
  }
  .modern-group-workspace-main {
    flex: none;
    height: max(520px, calc(100dvh - var(--modern-topbar-height) - var(--modern-space-5)));
  }
  .modern-group-workspace-sidebar {
    flex: none;
    border-left: 0;
    border-top: var(--modern-line-width) solid var(--modern-border);
  }
}
@container modern-group-workspace (max-width: 420px) {
  .modern-group-workspace-header {
    gap: var(--modern-space-2);
    align-items: flex-start;
  }
  .modern-group-workspace-name h1 {
    flex-basis: 100%;
  }
  .modern-group-workspace-identity {
    padding-left: 0;
  }
}
</style>
