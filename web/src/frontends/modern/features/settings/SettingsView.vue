<script setup lang="ts">
import {
  Cable,
  Database,
  Globe,
  Monitor,
  RotateCcw,
  Route,
  Save,
  Search,
  Server,
  SlidersHorizontal,
  X,
} from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { routeStrategies, type SettingKey, type SettingNumber } from '@modern/api/settings'
import { getSystemInfo, systemInfoKey } from '@modern/api/system'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useURLState } from '@modern/app/url-state'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppIcon,
  AppIconButton,
  AppPanel,
  AppSegmentedControl,
  AppSwitch,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import { useApiClient } from '@shared/http/client-context'
import FrontendPicker from './FrontendPicker.vue'
import SettingItem from './SettingItem.vue'
import SettingsHeadersEditor from './SettingsHeadersEditor.vue'
import SettingsNumberField from './SettingsNumberField.vue'
import SettingsSystemInfo from './SettingsSystemInfo.vue'
import { useSettingsEditor } from './use-settings-editor'

const { t, n } = useI18n()
const client = useApiClient()
const {
  query,
  base,
  draft,
  dirty,
  changed,
  resets,
  saving,
  fieldErrors,
  owned,
  locked,
  restore,
  undoRestore,
  discard,
  save,
} = useSettingsEditor()
const info = useQuery({
  queryKey: systemInfoKey,
  queryFn: ({ signal }) => getSystemInfo(client, signal),
})
const sectionIDs = [
  'routing',
  'connection',
  'browser',
  'maintenance',
  'interface',
  'system',
] as const
type SectionID = (typeof sectionIDs)[number]
const sectionFields: Record<SectionID, readonly SettingKey[]> = {
  routing: ['route_strategy', 'affinity_enabled', 'affinity_ttl', 'affinity_capacity'],
  connection: [
    'proxy_config',
    'responses_websocket_enabled',
    'first_byte_timeout',
    'request_timeout',
    'stream_idle_timeout',
    'retry_count',
    'blacklist_threshold',
    'validation_interval',
  ],
  browser: ['cors', 'header_rules', 'response_header_rules'],
  maintenance: ['request_log_retention_days', 'models_dev_auto_sync_enabled'],
  interface: [],
  system: [],
}
const sectionIcons = {
  routing: Route,
  connection: Cable,
  browser: Globe,
  maintenance: Database,
  interface: Monitor,
  system: Server,
}
const timeouts: readonly SettingNumber[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const reliability: readonly SettingNumber[] = [
  'retry_count',
  'blacklist_threshold',
  'validation_interval',
]
const affinityNumbers: readonly SettingNumber[] = ['affinity_ttl', 'affinity_capacity']
const headerKeys = ['header_rules', 'response_header_rules'] as const
const corsLists = ['allowed_methods', 'allowed_headers', 'exposed_headers'] as const
const state = useURLState(
  ['q', 'section'],
  (query) => ({
    q: typeof query.q === 'string' ? query.q : '',
    section: sectionIDs.includes(query.section as SectionID)
      ? (query.section as SectionID)
      : ('routing' as SectionID),
  }),
  (value) => ({
    ...(value.q ? { q: value.q } : {}),
    ...(value.section !== 'routing' ? { section: value.section } : {}),
  }),
)
const active = ref<SectionID>(state.value.section)
const scroller = ref<HTMLElement>()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const discardOpen = ref(false)
let scrollFrame: number | undefined
const words = computed(() => state.value.q.toLocaleLowerCase().trim().split(/\s+/u).filter(Boolean))
const sectionText = (id: SectionID) =>
  t('settingsForm.sections.' + id) + ' ' + t('settingsForm.sectionHelp.' + id)
function matches(key: SettingKey): boolean {
  const section = sectionIDs.find((id) => sectionFields[id].includes(key))!
  const text = [
    key,
    t('settingsForm.fields.' + key),
    t('settingsForm.hints.' + key),
    sectionText(section),
    key === 'cors'
      ? Object.keys(base.value?.values.cors ?? {})
          .map((key) => t('settingsForm.cors.' + key))
          .join(' ')
      : '',
  ]
    .join(' ')
    .toLocaleLowerCase()
  return words.value.every((word) => text.includes(word))
}
const visibleSections = computed(() =>
  sectionIDs.filter((id) => {
    if (sectionFields[id].length) return sectionFields[id].some(matches)
    const extra =
      id === 'interface'
        ? [t('settingsForm.frontend'), t('frontend.modern.title'), t('frontend.classic.title')]
        : ['version', 'database', 'dataDir', 'authKeySource', 'encryptionSource', 'encryption'].map(
            (key) => t('settingsForm.system.' + key),
          )
    const text = (sectionText(id) + ' ' + extra.join(' ')).toLocaleLowerCase()
    return words.value.every((word) => text.includes(word))
  }),
)
const strategyOptions = computed(() =>
  routeStrategies.map((value) => ({ value, label: t('settingsForm.strategies.' + value) })),
)
const proxyOptions = computed(() =>
  ['inherit', 'direct', 'custom'].map((value) => ({
    value,
    label: t('settingsForm.proxy.' + value),
  })),
)
function settingState(key: SettingKey) {
  return {
    overridden: owned(key),
    changed: changed.value.includes(key),
    resetting: resets.value.has(key),
    locked: locked(key),
    disabled: saving.value,
  }
}
function settingItem(key: SettingKey) {
  return {
    ...settingState(key),
    label: t('settingsForm.fields.' + key),
    hint: t('settingsForm.hints.' + key),
    controlId: 'settings-' + key,
  }
}
function disabled(key: SettingKey): boolean {
  return saving.value || locked(key)
}
function clearSearch(): void {
  state.value = { ...state.value, q: '' }
}
function scrollToSection(id: SectionID): void {
  const body = scroller.value
  const target = body?.querySelector<HTMLElement>('#settings-section-' + id)
  if (!body || !target) return
  active.value = id
  const inset = Number.parseFloat(getComputedStyle(body).paddingTop) || 0
  body.scrollTo({
    top:
      target.getBoundingClientRect().top -
      body.getBoundingClientRect().top +
      body.scrollTop -
      inset,
  })
}
function selectSection(id: SectionID): void {
  if (state.value.section === id) {
    scrollToSection(id)
    return
  }
  state.value = { ...state.value, section: id }
}
function updateActive(): void {
  if (scrollFrame !== undefined) return
  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = undefined
    const body = scroller.value
    if (!body) return
    const top =
      body.getBoundingClientRect().top + (Number.parseFloat(getComputedStyle(body).paddingTop) || 0)
    const ids = visibleSections.value
    const atBottom =
      body.scrollHeight > body.clientHeight &&
      body.scrollTop + body.clientHeight >= body.scrollHeight - 1
    active.value =
      (atBottom
        ? ids.at(-1)
        : [...ids]
            .reverse()
            .find(
              (id) =>
                (body.querySelector('#settings-section-' + id)?.getBoundingClientRect().top ??
                  Infinity) <=
                top + 1,
            )) ??
      ids[0] ??
      'routing'
  })
}
watch(
  () => state.value.section,
  (id) => {
    void nextTick(() => scrollToSection(id))
  },
)
watch(
  () => state.value.q,
  () => {
    void nextTick(() => {
      scroller.value?.scrollTo({ top: 0 })
      updateActive()
    })
  },
)
watch(
  () => Boolean(base.value),
  (ready) => {
    if (ready) void nextTick(() => scrollToSection(state.value.section))
  },
  { immediate: true },
)
async function submit(): Promise<void> {
  const result = await save()
  if (result !== 'invalid') return
  clearSearch()
  await nextTick()
  const input = scroller.value?.querySelector<HTMLElement>('[aria-invalid="true"]')
  input?.scrollIntoView({ block: 'center' })
  input?.focus({ preventScroll: true })
}
function confirmDiscard(): void {
  discardOpen.value = false
  discard()
}
async function beforeFrontendSwitch(): Promise<boolean> {
  if (!(await guard.value?.confirm())) return false
  discard()
  await nextTick()
  return true
}
async function refresh(): Promise<void> {
  await Promise.all([query.refetch(), info.refetch()])
}
const pending = computed(() => saving.value || query.isFetching.value || info.isFetching.value)
useLoadingActivity(() => info.isFetching.value)
usePageRefresh({
  refresh,
  pending,
  updatedAt: computed(() => query.dataUpdatedAt.value || undefined),
})
onScopeDispose(() => {
  if (scrollFrame !== undefined) cancelAnimationFrame(scrollFrame)
})
</script>

<template>
  <div class="modern-settings-workspace">
    <form class="modern-settings-search" role="search" @submit.prevent>
      <AppTextField
        v-model="state.q"
        :label="t('settingsForm.search')"
        :placeholder="t('settingsForm.searchPlaceholder')"
        :icon="Search"
        type="search"
        label-hidden
      >
        <template v-if="state.q" #suffix>
          <AppIconButton
            :icon="X"
            :label="t('settingsForm.clearSearch')"
            size="xs"
            @click="clearSearch"
          />
        </template>
      </AppTextField>
    </form>
    <AppCollectionState
      v-if="!base"
      :loading="query.isPending.value"
      :error="query.isError.value"
      :title="t(query.isPending.value ? 'settingsForm.loading' : 'settingsForm.failed')"
    >
      <AppButton v-if="query.isError.value" @click="refresh">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <div v-else-if="draft" class="modern-settings-layout">
      <nav class="modern-settings-navigation" :aria-label="t('settingsForm.categories')">
        <p class="modern-settings-nav-caption">{{ t('settingsForm.categories') }}</p>
        <div class="modern-settings-nav-items">
          <AppButton
            v-for="id in visibleSections"
            :key="id"
            :variant="active === id ? 'brand' : 'ghost'"
            :aria-current="active === id ? 'location' : undefined"
            :icon="sectionIcons[id]"
            class="modern-settings-nav-item"
            @click="selectSection(id)"
          >
            <span class="modern-settings-nav-label">{{ t('settingsForm.sections.' + id) }}</span>
            <AppBadge
              v-if="sectionFields[id].some((key) => changed.includes(key))"
              size="xs"
              tone="brand"
              >{{ n(sectionFields[id].filter((key) => changed.includes(key)).length) }}</AppBadge
            >
          </AppButton>
        </div>
      </nav>
      <form
        id="modern-settings-form"
        ref="scroller"
        class="modern-settings-body"
        novalidate
        @submit.prevent="submit"
        @scroll.passive="updateActive"
      >
        <AppCollectionState
          v-if="!visibleSections.length"
          :icon="Search"
          :title="t('settingsForm.empty')"
        >
          <AppButton @click="clearSearch">{{ t('settingsForm.clearSearch') }}</AppButton>
        </AppCollectionState>
        <AppPanel
          v-for="id in visibleSections"
          :id="'settings-section-' + id"
          :key="id"
          :title="t('settingsForm.sections.' + id)"
          :description="t('settingsForm.sectionHelp.' + id)"
          compact
          class="modern-settings-section"
        >
          <div class="modern-settings-fields">
            <template v-if="id === 'routing'">
              <SettingItem
                v-if="matches('route_strategy')"
                v-bind="settingItem('route_strategy')"
                wrap-control
                class="modern-settings-block"
                @reset="restore('route_strategy')"
                @undo="undoRestore('route_strategy')"
              >
                <AppSegmentedControl
                  id="settings-route_strategy"
                  :model-value="draft.route_strategy"
                  :label="t('settingsForm.fields.route_strategy')"
                  :options="strategyOptions"
                  appearance="field"
                  :disabled="disabled('route_strategy')"
                  @update:model-value="
                    draft.route_strategy =
                      $event === 'weighted_mix' ? 'weighted_mix' : 'native_first'
                  "
                />
              </SettingItem>
              <div
                v-if="matches('affinity_enabled') || affinityNumbers.some(matches)"
                class="modern-settings-block modern-settings-group"
              >
                <SettingItem
                  v-if="matches('affinity_enabled')"
                  v-bind="settingItem('affinity_enabled')"
                  :description="t('settingsForm.hints.affinity_enabled')"
                  @reset="restore('affinity_enabled')"
                  @undo="undoRestore('affinity_enabled')"
                >
                  <AppSwitch
                    id="settings-affinity_enabled"
                    v-model="draft.affinity_enabled"
                    :label="t('settingsForm.fields.affinity_enabled')"
                    :disabled="disabled('affinity_enabled')"
                  />
                </SettingItem>
                <div v-if="affinityNumbers.some(matches)" class="modern-settings-number-grid">
                  <SettingsNumberField
                    v-for="key in affinityNumbers.filter(matches)"
                    :key="key"
                    v-model="draft[key]"
                    :setting="key"
                    v-bind="settingState(key)"
                    :error="fieldErrors[key]"
                    @reset="restore(key)"
                    @undo="undoRestore(key)"
                  />
                </div>
              </div>
            </template>
            <template v-else-if="id === 'connection'">
              <SettingItem
                v-if="matches('responses_websocket_enabled')"
                v-bind="settingItem('responses_websocket_enabled')"
                class="modern-settings-block"
                @reset="restore('responses_websocket_enabled')"
                @undo="undoRestore('responses_websocket_enabled')"
              >
                <AppSwitch
                  id="settings-responses_websocket_enabled"
                  v-model="draft.responses_websocket_enabled"
                  :label="t('settingsForm.fields.responses_websocket_enabled')"
                  :disabled="disabled('responses_websocket_enabled')"
                />
              </SettingItem>
              <SettingItem
                v-if="matches('proxy_config')"
                v-bind="settingItem('proxy_config')"
                :description="t('settingsForm.hints.proxy_config')"
                wrap-control
                class="modern-settings-block"
                @reset="restore('proxy_config')"
                @undo="undoRestore('proxy_config')"
              >
                <AppSegmentedControl
                  id="settings-proxy_config"
                  :model-value="draft.proxy_config.mode"
                  :label="t('settingsForm.fields.proxy_config')"
                  :options="proxyOptions"
                  appearance="field"
                  :disabled="disabled('proxy_config')"
                  @update:model-value="
                    draft.proxy_config.mode =
                      $event === 'custom' || $event === 'direct' ? $event : 'inherit'
                  "
                />
                <template #details>
                  <div class="modern-settings-proxy">
                    <AppTextField
                      v-if="draft.proxy_config.mode === 'custom'"
                      v-model="draft.proxy_config.url"
                      :label="t('settingsForm.proxy.url')"
                      :disabled="disabled('proxy_config')"
                      :error="fieldErrors.proxy_config"
                      :placeholder="
                        base.values.proxy_config.configured_mode === 'custom'
                          ? t('settingsForm.proxy.existing')
                          : t('settingsForm.proxy.placeholder')
                      "
                      autocomplete="off"
                      spellcheck="false"
                    />
                    <p class="modern-settings-proxy-effective">
                      <span>{{ t('settingsForm.proxy.effective') }}</span>
                      <AppCopyValue
                        v-if="base.values.proxy_config.display_url"
                        :value="base.values.proxy_config.display_url"
                      />
                      <span v-else>{{
                        t(
                          base.values.proxy_config.effective_mode === 'environment'
                            ? 'settingsForm.proxy.environment'
                            : 'settingsForm.proxy.directValue',
                        )
                      }}</span>
                    </p>
                  </div>
                </template>
              </SettingItem>
              <div
                v-if="timeouts.some(matches)"
                class="modern-settings-block modern-settings-group"
              >
                <h3 class="modern-settings-group-title">{{ t('settingsForm.timeouts') }}</h3>
                <div class="modern-settings-number-grid">
                  <SettingsNumberField
                    v-for="key in timeouts.filter(matches)"
                    :key="key"
                    v-model="draft[key]"
                    :setting="key"
                    v-bind="settingState(key)"
                    :error="fieldErrors[key]"
                    @reset="restore(key)"
                    @undo="undoRestore(key)"
                  />
                </div>
              </div>
              <div
                v-if="reliability.some(matches)"
                class="modern-settings-block modern-settings-group"
              >
                <h3 class="modern-settings-group-title">{{ t('settingsForm.reliability') }}</h3>
                <div class="modern-settings-number-grid">
                  <SettingsNumberField
                    v-for="key in reliability.filter(matches)"
                    :key="key"
                    v-model="draft[key]"
                    :setting="key"
                    v-bind="settingState(key)"
                    :error="fieldErrors[key]"
                    @reset="restore(key)"
                    @undo="undoRestore(key)"
                  />
                </div>
              </div>
            </template>
            <template v-else-if="id === 'browser'">
              <SettingItem
                v-if="matches('cors')"
                v-bind="settingItem('cors')"
                :description="t('settingsForm.hints.cors')"
                class="modern-settings-block"
                @reset="restore('cors')"
                @undo="undoRestore('cors')"
              >
                <AppSwitch
                  id="settings-cors"
                  v-model="draft.cors.enabled"
                  :label="t('settingsForm.cors.enabled')"
                  :disabled="disabled('cors')"
                />
                <template #details>
                  <div class="modern-settings-cors">
                    <AppTextArea
                      v-model="draft.cors.allowed_origins"
                      :label="t('settingsForm.cors.allowed_origins')"
                      :placeholder="t('settingsForm.cors.originsPlaceholder')"
                      :description="t('settingsForm.cors.listHint')"
                      :rows="2"
                      :error="fieldErrors['cors.allowed_origins']"
                      :disabled="disabled('cors')"
                      class="modern-settings-wide"
                    />
                    <AppTextField
                      v-for="key in corsLists"
                      :key="key"
                      v-model="draft.cors[key]"
                      :label="t('settingsForm.cors.' + key)"
                      :placeholder="
                        t(
                          key === 'allowed_methods'
                            ? 'settingsForm.cors.methodsPlaceholder'
                            : key === 'allowed_headers'
                              ? 'settingsForm.cors.headersPlaceholder'
                              : 'settingsForm.cors.exposedPlaceholder',
                        )
                      "
                      :error="fieldErrors['cors.' + key]"
                      :disabled="disabled('cors')"
                    />
                    <AppTextField
                      v-model="draft.cors.max_age"
                      :label="t('settingsForm.cors.max_age')"
                      inputmode="numeric"
                      :error="fieldErrors['cors.max_age']"
                      :disabled="disabled('cors')"
                    >
                      <template #suffix>
                        <span class="modern-settings-unit">{{ t('settingsForm.seconds') }}</span>
                      </template>
                    </AppTextField>
                    <SettingItem
                      :label="t('settingsForm.cors.allow_credentials')"
                      :hint="t('settingsForm.cors.credentialHint')"
                      control-id="settings-cors-credentials"
                      class="modern-settings-wide"
                    >
                      <AppSwitch
                        id="settings-cors-credentials"
                        v-model="draft.cors.allow_credentials"
                        :label="t('settingsForm.cors.allow_credentials')"
                        :disabled="disabled('cors')"
                      />
                    </SettingItem>
                  </div>
                </template>
              </SettingItem>
              <template v-for="key in headerKeys" :key="key">
                <SettingItem
                  v-if="matches(key)"
                  v-bind="settingItem(key)"
                  :description="t('settingsForm.hints.' + key)"
                  stacked
                  class="modern-settings-block"
                  @reset="restore(key)"
                  @undo="undoRestore(key)"
                >
                  <SettingsHeadersEditor
                    v-model="draft[key]"
                    :setting="key"
                    :errors="fieldErrors"
                    :disabled="disabled(key)"
                  />
                </SettingItem>
              </template>
            </template>
            <template v-else-if="id === 'maintenance'">
              <div
                v-if="matches('request_log_retention_days')"
                class="modern-settings-block modern-settings-retention"
              >
                <SettingsNumberField
                  v-model="draft.request_log_retention_days"
                  setting="request_log_retention_days"
                  v-bind="settingState('request_log_retention_days')"
                  :error="fieldErrors.request_log_retention_days"
                  @reset="restore('request_log_retention_days')"
                  @undo="undoRestore('request_log_retention_days')"
                />
              </div>
              <SettingItem
                v-if="matches('models_dev_auto_sync_enabled')"
                v-bind="settingItem('models_dev_auto_sync_enabled')"
                :description="t('settingsForm.hints.models_dev_auto_sync_enabled')"
                class="modern-settings-block"
                @reset="restore('models_dev_auto_sync_enabled')"
                @undo="undoRestore('models_dev_auto_sync_enabled')"
              >
                <AppSwitch
                  id="settings-models_dev_auto_sync_enabled"
                  v-model="draft.models_dev_auto_sync_enabled"
                  :label="t('settingsForm.fields.models_dev_auto_sync_enabled')"
                  :disabled="disabled('models_dev_auto_sync_enabled')"
                />
              </SettingItem>
            </template>
            <FrontendPicker
              v-else-if="id === 'interface'"
              :disabled="saving"
              :before-switch="beforeFrontendSwitch"
            />
            <SettingsSystemInfo
              v-else-if="id === 'system'"
              :data="info.data.value"
              :loading="info.isPending.value"
              :failed="info.isError.value"
              @retry="info.refetch()"
            />
          </div>
        </AppPanel>
      </form>
    </div>
    <footer v-if="dirty" class="modern-settings-savebar">
      <span>
        <AppIcon :icon="SlidersHorizontal" size="sm" />
        {{ t('settingsForm.unsaved', { count: n(changed.length) }) }}
      </span>
      <div>
        <AppButton :icon="RotateCcw" :disabled="saving" @click="discardOpen = true">{{
          t('settingsForm.discard')
        }}</AppButton>
        <AppButton
          form="modern-settings-form"
          type="submit"
          variant="primary"
          :icon="Save"
          :loading="saving"
          >{{ t('settingsForm.save') }}</AppButton
        >
      </div>
    </footer>
    <AppDraftGuard ref="guard" :dirty="dirty" :pending="saving" />
    <AppConfirmDialog
      :open="discardOpen"
      :title="t('settingsForm.discardTitle')"
      :confirm-label="t('settingsForm.discard')"
      :cancel-label="t('settingsForm.keepEditing')"
      tone="danger"
      @confirm="confirmDiscard"
      @cancel="discardOpen = false"
    />
  </div>
</template>

<style scoped>
.modern-settings-workspace {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.modern-settings-search {
  flex: none;
  padding: var(--modern-space-5) 0 var(--modern-space-4);
}
.modern-settings-layout {
  display: grid;
  grid-template-columns: 184px minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  min-width: 0;
  gap: var(--modern-space-5);
}
.modern-settings-navigation {
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-1);
}
.modern-settings-nav-caption {
  padding: var(--modern-space-2) var(--modern-space-3) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-nav-items {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-settings-nav-item {
  width: 100%;
  min-width: 0;
  justify-content: flex-start;
  gap: var(--modern-space-2);
}
.modern-settings-nav-label {
  flex: 1;
  text-align: left;
  white-space: normal;
}
.modern-settings-body {
  container: modern-settings-content / inline-size;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: var(--modern-space-4);
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
  padding: var(--modern-space-1) var(--modern-space-1) var(--modern-space-5);
}
.modern-settings-section {
  flex: none;
}
.modern-settings-fields,
.modern-settings-group {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-4);
}
.modern-settings-block + .modern-settings-block {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-4);
}
.modern-settings-group-title {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-settings-number-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  gap: var(--modern-space-2) var(--modern-space-5);
  min-width: 0;
}
.modern-settings-proxy {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-settings-proxy-effective {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-2);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-proxy-effective > :first-child {
  flex: none;
}
.modern-settings-cors {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: var(--modern-space-4) var(--modern-space-5);
}
.modern-settings-wide {
  grid-column: 1 / -1;
}
.modern-settings-unit {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  white-space: nowrap;
}
.modern-settings-retention {
  display: grid;
  grid-template-columns: minmax(0, 320px);
  gap: var(--modern-space-2);
}
.modern-settings-savebar {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: var(--modern-space-3) 0;
}
.modern-settings-savebar > span,
.modern-settings-savebar > div {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-settings-savebar > span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-savebar > div {
  margin-left: auto;
}
@container modern-settings-content (max-width: 560px) {
  .modern-settings-cors {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 1150px) {
  .modern-settings-layout {
    grid-template-columns: 160px minmax(0, 1fr);
    gap: var(--modern-space-3);
  }
}
@media (max-width: 760px) {
  .modern-settings-layout {
    display: flex;
    flex-direction: column;
    gap: var(--modern-space-3);
  }
  .modern-settings-navigation {
    flex: none;
    overflow-x: auto;
  }
  .modern-settings-nav-caption {
    display: none;
  }
  .modern-settings-nav-items {
    display: flex;
    width: max-content;
  }
  .modern-settings-nav-item {
    width: auto;
    flex: none;
  }
  .modern-settings-nav-label {
    white-space: nowrap;
  }
  .modern-settings-body {
    flex: 1;
  }
}
</style>
