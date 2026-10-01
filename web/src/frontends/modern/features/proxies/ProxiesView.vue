<script setup lang="ts">
import { Plus, Upload, Play, Pause, Trash2, Pencil, Search, FlaskConical, X } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { useQuery, useQueryClient, keepPreviousData } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import {
  batchProxies,
  importProxies,
  listProxies,
  proxyImpact,
  proxyListKey,
  proxyTestSucceeded,
  runProxyTests,
  saveProxyTestURL,
  validTestURL,
  type ProxyFilters,
  type ProxyImpact,
  type ProxyItem,
  type ProxyList,
} from '@shared/proxies/api'
import { useURLState, positivePage } from '@modern/app/url-state'
import { usePageRefresh } from '@modern/app/page-refresh'
import {
  AppBadge,
  AppButton,
  AppCheckbox,
  AppConfirmDialog,
  AppDialogContent,
  AppDialogHeader,
  AppIconButton,
  AppListFrame,
  AppNotice,
  AppOverflowText,
  AppPagination,
  AppSegmentedControl,
  AppSelect,
  AppTextArea,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import ProxyEditor from './ProxyEditor.vue'

const { t, n, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const filters = useURLState<ProxyFilters>(
  ['q', 'state', 'scheme', 'used', 'test', 'sort', 'page', 'page_size'],
  (q) => ({
    q: typeof q.q === 'string' ? q.q : '',
    state: typeof q.state === 'string' ? q.state : '',
    scheme: typeof q.scheme === 'string' ? q.scheme : '',
    used: typeof q.used === 'string' ? q.used : '',
    test: typeof q.test === 'string' ? q.test : '',
    sort: q.sort === 'latency' ? 'latency' : 'name',
    page: positivePage(q.page),
    page_size: [20, 50, 100].includes(Number(q.page_size)) ? Number(q.page_size) : 20,
  }),
  (value) =>
    Object.fromEntries(
      Object.entries(value)
        .filter(([, v]) => v !== '')
        .map(([key, value]) => [key, String(value)]),
    ),
)
const query = useQuery({
  queryKey: computed(() => [...proxyListKey, filters.value]),
  queryFn: ({ signal }) => listProxies(client, filters.value, signal),
  placeholderData: keepPreviousData,
})
usePageRefresh({
  refresh: () => query.refetch(),
  pending: query.isFetching,
  updatedAt: query.dataUpdatedAt,
})
const selected = ref(new Set<number>())
const search = ref(filters.value.q)
watch(
  () => filters.value.q,
  (value) => {
    search.value = value
  },
)
const target = ref('')
const savedTarget = ref('')
const targetState = ref<'saved' | 'saving' | 'saveFailed'>('saved')
let initialized = false
let targetTimer: ReturnType<typeof setTimeout> | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let savingTarget = false
let alive = true
watch(
  query.data,
  (value) => {
    if (!value) return
    if (!initialized || (!savingTarget && target.value.trim() === savedTarget.value)) {
      target.value = value.test_url
      savedTarget.value = value.test_url
      initialized = true
    }
    const maxPage = Math.max(1, Math.ceil(value.total / filters.value.page_size))
    if (filters.value.page > maxPage) filters.value = { ...filters.value, page: maxPage }
  },
  { immediate: true },
)
watch(search, (q) => {
  clearTimeout(searchTimer)
  if (q === filters.value.q) return
  searchTimer = setTimeout(() => change({ q }), 250)
})
watch(target, () => {
  clearTimeout(targetTimer)
  if (
    initialized &&
    target.value.trim() !== savedTarget.value &&
    validTestURL(target.value.trim())
  ) {
    targetState.value = 'saving'
    targetTimer = setTimeout(() => void saveTarget(), 500)
  }
})
async function saveTarget() {
  if (savingTarget || !initialized) return
  savingTarget = true
  try {
    while (validTestURL(target.value.trim()) && target.value.trim() !== savedTarget.value) {
      const value = target.value.trim()
      targetState.value = 'saving'
      await saveProxyTestURL(client, value)
      savedTarget.value = value
      cache.setQueriesData<ProxyList>({ queryKey: proxyListKey }, (previous) =>
        previous ? { ...previous, test_url: value } : previous,
      )
    }
    targetState.value = 'saved'
  } catch {
    targetState.value = 'saveFailed'
  } finally {
    savingTarget = false
  }
}
function change(value: Partial<ProxyFilters>) {
  filters.value = { ...filters.value, ...value, page: value.page ?? 1 }
  selected.value = new Set()
}
const updates = ref(new Map<number, ProxyItem>())
const rows = computed(() =>
  (query.data.value?.items ?? []).map((row) => {
    const result = updates.value.get(row.id)
    return result && (result.last_test_at_ms ?? 0) >= (row.last_test_at_ms ?? 0)
      ? {
          ...row,
          last_test_url: result.last_test_url,
          last_test_at_ms: result.last_test_at_ms,
          last_test_duration_ms: result.last_test_duration_ms,
          last_test_status_code: result.last_test_status_code,
          last_test_error: result.last_test_error,
        }
      : row
  }),
)
const allSelected = computed(
  () => rows.value.length > 0 && rows.value.every((row) => selected.value.has(row.id)),
)
function toggle(id: number, checked: boolean) {
  const next = new Set(selected.value)
  if (checked) next.add(id)
  else next.delete(id)
  selected.value = next
}
const states = computed(() =>
  ['', 'enabled', 'disabled'].map((value) => ({ value, label: t('proxies.' + (value || 'all')) })),
)
const schemes = computed(() => [
  { value: '', label: t('proxies.protocol') },
  { value: 'http', label: 'HTTP' },
  { value: 'socks5', label: 'SOCKS5' },
])
const usages = computed(() =>
  ['', 'used', 'unused'].map((value) => ({
    value,
    label: t('proxies.' + (value || 'references')),
  })),
)
const tests = computed(() =>
  ['', 'untested', 'success', 'failed'].map((value) => ({
    value,
    label: t('proxies.' + (value || 'latestTest')),
  })),
)
const sorts = computed(() => [
  { value: 'name', label: t('proxies.sortName') },
  { value: 'latency', label: t('proxies.sortLatency') },
])
const editor = ref<ProxyItem | null | undefined>()
const importing = ref(false)
const importText = ref('')
const notice = ref('')
const error = ref('')
const pending = ref(false)
const impact = ref<ProxyImpact>()
const confirmation = ref<{ ids: number[]; action: 'disable' | 'delete' }>()
const detail = ref<ProxyImpact>()
const running = ref(false)
const preparing = ref(false)
const total = ref(0)
const done = ref(0)
const successful = ref(0)
const failed = ref<number[]>([])
const testing = ref(new Set<number>())
let testController: AbortController | undefined
const progress = computed(() =>
  t('proxies.progress', {
    done: n(done.value),
    total: n(total.value),
    success: n(successful.value),
    failed: n(failed.value.length),
    cancelled: n(running.value ? 0 : total.value - done.value),
  }),
)
async function refresh() {
  await cache.invalidateQueries({ queryKey: proxyListKey })
}
async function run(ids: number[]) {
  if (running.value || !ids.length || !validTestURL(target.value.trim())) return
  const controller = new AbortController()
  testController = controller
  running.value = true
  total.value = ids.length
  done.value = successful.value = 0
  failed.value = []
  testing.value = new Set(ids)
  await runProxyTests(client, ids, target.value.trim(), controller.signal, (id, result) => {
    testing.value.delete(id)
    done.value++
    if (result) updates.value.set(id, result)
    if (result && proxyTestSucceeded(result)) successful.value++
    else failed.value.push(id)
  })
  running.value = false
  testing.value = new Set()
  if (alive) await refresh()
}
async function runFiltered() {
  if (running.value || preparing.value) return
  preparing.value = true
  error.value = ''
  try {
    const list = await listProxies(client, filters.value, undefined, true)
    if (alive) await run(list.items.map((row) => row.id))
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    preparing.value = false
  }
}
async function act(action: 'enable' | 'disable' | 'delete', ids: number[]) {
  if (!ids.length || pending.value) return
  pending.value = true
  error.value = ''
  try {
    if (action === 'enable') {
      await batchProxies(client, ids, action)
      await refresh()
    } else {
      impact.value = await proxyImpact(client, ids)
      confirmation.value = { ids, action }
    }
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
async function confirm() {
  if (!confirmation.value || pending.value) return
  pending.value = true
  error.value = ''
  try {
    await batchProxies(client, confirmation.value.ids, confirmation.value.action)
    confirmation.value = undefined
    selected.value = new Set()
    await refresh()
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
async function showReferences(id: number) {
  try {
    detail.value = await proxyImpact(client, [id])
  } catch {
    error.value = t('proxies.operationFailed')
  }
}
async function importRows() {
  if (pending.value || !importText.value.trim()) return
  pending.value = true
  error.value = ''
  try {
    const result = await importProxies(client, importText.value)
    notice.value = t('proxies.importResult', {
      imported: n(result.imported),
      duplicates: n(result.duplicates),
      lines: result.invalid_lines.length ? result.invalid_lines.join(', ') : t('proxies.noInvalid'),
    })
    importText.value = ''
    importing.value = false
    await refresh()
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
const confirmationDescription = computed(() => {
  if (!confirmation.value || !impact.value) return ''
  return [
    t('proxies.confirmImpact', {
      action: t('proxies.' + confirmation.value.action),
      count: n(confirmation.value.ids.length),
      groups: n(impact.value.groups.length),
      credentials: n(impact.value.credentials.length),
    }),
    impact.value.global ? t('proxies.globalImpact') : '',
    t('proxies.inheritWarning'),
  ]
    .filter(Boolean)
    .join(' ')
})
function time(value: number | null) {
  return value === null ? '' : new Date(value).toLocaleString(locale.value)
}
onScopeDispose(() => {
  alive = false
  clearTimeout(targetTimer)
  clearTimeout(searchTimer)
  testController?.abort()
  void saveTarget()
})
</script>

<template>
  <div class="modern-proxies">
    <div class="modern-proxy-toolbar">
      <AppTextField
        v-model="search"
        :icon="Search"
        type="search"
        :label="t('proxies.search')"
        label-hidden
        :placeholder="t('proxies.search')"
      />
      <AppSelect
        :model-value="filters.scheme"
        :options="schemes"
        :label="t('proxies.protocol')"
        label-hidden
        @update:model-value="change({ scheme: $event })"
      />
      <AppSelect
        :model-value="filters.used"
        :options="usages"
        :label="t('proxies.references')"
        label-hidden
        @update:model-value="change({ used: $event })"
      />
      <AppSelect
        :model-value="filters.test"
        :options="tests"
        :label="t('proxies.latestTest')"
        label-hidden
        @update:model-value="change({ test: $event })"
      />
      <AppSelect
        :model-value="filters.sort"
        :options="sorts"
        :label="t('proxies.sortName')"
        label-hidden
        @update:model-value="change({ sort: $event })"
      />
      <div class="modern-proxy-primary">
        <AppButton :icon="Upload" variant="outline" @click="importing = true">{{
          t('proxies.import')
        }}</AppButton
        ><AppButton :icon="Plus" @click="editor = null">{{ t('proxies.new') }}</AppButton>
      </div>
    </div>
    <div class="modern-proxy-test-target">
      <AppTextField
        v-model="target"
        :label="t('proxies.testURL')"
        :disabled="!initialized"
        :error="target && !validTestURL(target.trim()) ? t('proxies.invalidURL') : undefined"
        @blur="saveTarget"
      />
      <span role="status">{{ t('proxies.' + targetState) }}</span>
      <AppButton v-if="targetState === 'saveFailed'" variant="ghost" @click="saveTarget">{{
        t('proxies.save')
      }}</AppButton>
      <AppTooltip :label="t('proxies.testHelp')"
        ><AppButton
          :icon="FlaskConical"
          variant="outline"
          :disabled="
            running || preparing || !query.data.value?.total || !validTestURL(target.trim())
          "
          :loading="preparing"
          @click="runFiltered"
          >{{ t('proxies.testFiltered') }}
          <span v-if="query.data.value">({{ n(query.data.value.total) }})</span></AppButton
        ></AppTooltip
      >
    </div>
    <AppSegmentedControl
      :model-value="filters.state"
      :options="states"
      :label="t('proxies.status')"
      @update:model-value="change({ state: $event })"
    />
    <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    <AppNotice v-if="notice">{{ notice }}</AppNotice>
    <div v-if="total" class="modern-proxy-progress" role="status" aria-live="polite">
      <span>{{ progress }}</span>
      <AppButton v-if="running" :icon="X" variant="ghost" @click="testController?.abort()">{{
        t('proxies.stop')
      }}</AppButton>
      <AppButton v-else-if="failed.length" variant="ghost" @click="run([...failed])">{{
        t('proxies.retryFailed')
      }}</AppButton>
    </div>
    <AppListFrame :label="t('proxies.title')" :loading="query.isFetching.value">
      <template #header>
        <div class="modern-proxy-batch">
          <AppCheckbox
            :model-value="allSelected"
            :indeterminate="selected.size > 0 && !allSelected"
            :label="t('proxies.selectPage')"
            @update:model-value="selected = $event ? new Set(rows.map((row) => row.id)) : new Set()"
          />
          <template v-if="selected.size">
            <span>{{ t('proxies.selected', { count: n(selected.size) }) }}</span>
            <AppButton
              size="sm"
              variant="ghost"
              :disabled="running || !validTestURL(target.trim())"
              @click="run([...selected])"
              >{{ t('proxies.testSelected') }}</AppButton
            >
            <AppIconButton
              :icon="Play"
              :label="t('proxies.enable')"
              :disabled="pending"
              @click="act('enable', [...selected])"
            />
            <AppIconButton
              :icon="Pause"
              :label="t('proxies.disable')"
              :disabled="pending"
              @click="act('disable', [...selected])"
            />
            <AppIconButton
              :icon="Trash2"
              :label="t('proxies.delete')"
              :disabled="pending"
              @click="act('delete', [...selected])"
            />
          </template>
        </div>
        <div class="modern-proxy-row modern-proxy-heading">
          <span /><span>{{ t('proxies.name') }}</span
          ><span>{{ t('proxies.protocol') }}</span
          ><span>{{ t('proxies.status') }}</span
          ><span>{{ t('proxies.references') }}</span
          ><span>{{ t('proxies.latestTest') }}</span
          ><span>{{ t('proxies.actions') }}</span>
        </div>
      </template>
      <p v-if="query.isError.value" class="modern-proxy-empty">{{ t('proxies.loadFailed') }}</p>
      <p v-else-if="!rows.length" class="modern-proxy-empty">
        {{ t(query.data.value?.total ? 'proxies.noResults' : 'proxies.empty') }}
      </p>
      <div v-for="row in rows" :key="row.id" class="modern-proxy-row">
        <AppCheckbox
          :model-value="selected.has(row.id)"
          :label="row.name"
          @update:model-value="toggle(row.id, $event)"
        />
        <div class="modern-proxy-identity">
          <AppOverflowText :text="row.name" /><AppOverflowText :text="row.display_url" />
        </div>
        <span>{{ row.scheme.toUpperCase() }}</span>
        <AppBadge :tone="row.enabled ? 'success' : 'neutral'">{{
          t(row.enabled ? 'proxies.enabled' : 'proxies.disabled')
        }}</AppBadge>
        <AppButton
          class="modern-proxy-references"
          variant="ghost"
          size="sm"
          @click="showReferences(row.id)"
          ><span
            >{{ t('proxies.groups', { count: n(row.group_count) }) }} ·
            {{ t('proxies.credentials', { count: n(row.credential_count) }) }}</span
          ><AppBadge v-if="row.global">{{ t('proxies.global') }}</AppBadge></AppButton
        >
        <div class="modern-proxy-result">
          <span v-if="row.last_test_at_ms === null">{{ t('proxies.untested') }}</span>
          <template v-else
            ><AppTooltip :label="[row.last_test_url, time(row.last_test_at_ms)].join(' · ')"
              ><span
                >{{
                  row.last_test_error
                    ? t('proxies.' + row.last_test_error)
                    : `HTTP ${row.last_test_status_code}`
                }}
                · {{ n(row.last_test_duration_ms ?? 0) }} ms</span
              ></AppTooltip
            ><small>{{ time(row.last_test_at_ms) }}</small></template
          >
        </div>
        <div class="modern-proxy-actions">
          <AppIconButton
            :icon="FlaskConical"
            :label="t('proxies.test')"
            :loading="testing.has(row.id)"
            :disabled="running || !validTestURL(target.trim())"
            @click="run([row.id])"
          />
          <AppIconButton :icon="Pencil" :label="t('proxies.edit')" @click="editor = row" />
          <AppIconButton
            :icon="row.enabled ? Pause : Play"
            :label="t(row.enabled ? 'proxies.disable' : 'proxies.enable')"
            :disabled="pending"
            @click="act(row.enabled ? 'disable' : 'enable', [row.id])"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('proxies.delete')"
            :disabled="pending"
            @click="act('delete', [row.id])"
          />
        </div>
      </div>
      <template #footer
        ><AppPagination
          mode="total"
          :page="filters.page"
          :page-size="filters.page_size"
          :total="query.data.value?.total"
          :pending="query.isFetching.value"
          @update:page="change({ page: $event })"
          @update:page-size="change({ page_size: $event })"
      /></template>
    </AppListFrame>
    <ProxyEditor
      v-if="editor !== undefined"
      :proxy="editor ?? undefined"
      @close="editor = undefined"
      @saved="editor = undefined"
    />
    <AppConfirmDialog
      :open="Boolean(confirmation)"
      :title="
        t('proxies.confirmTitle', { action: t('proxies.' + (confirmation?.action ?? 'delete')) })
      "
      :description="confirmationDescription"
      :confirm-label="t('proxies.' + (confirmation?.action ?? 'delete'))"
      tone="danger"
      :pending="pending"
      :error="error"
      @cancel="confirmation = undefined"
      @confirm="confirm"
    />
    <DialogRoot :open="importing" @update:open="!pending && (importing = $event)"
      ><AppDialogContent
        :title="t('proxies.import')"
        :description="t('proxies.importHelp')"
        placement="editor"
        size="sheet"
        ><AppDialogHeader
          :close-label="t('proxies.cancel')"
          :title="t('proxies.import')"
          @close="!pending && (importing = false)"
        />
        <form class="modern-proxy-panel" @submit.prevent="importRows">
          <AppTextArea
            v-model="importText"
            :label="t('proxies.address')"
            :description="t('proxies.importHelp')"
            :disabled="pending"
            autocomplete="off"
            spellcheck="false"
            :rows="12"
          /><AppNotice v-if="error" tone="danger">{{ error }}</AppNotice
          ><AppButton type="submit" :loading="pending" :disabled="!importText.trim()">{{
            t('proxies.import')
          }}</AppButton>
        </form></AppDialogContent
      ></DialogRoot
    >
    <DialogRoot :open="Boolean(detail)" @update:open="!$event && (detail = undefined)"
      ><AppDialogContent
        :title="t('proxies.references')"
        :description="t('proxies.inheritWarning')"
        placement="sidebar"
        size="sheet"
        ><AppDialogHeader
          :close-label="t('proxies.cancel')"
          :title="t('proxies.references')"
          @close="detail = undefined"
        />
        <div class="modern-proxy-panel">
          <RouterLink
            v-if="detail?.global"
            :to="{ name: 'modern-settings', query: { section: 'connection' } }"
            >{{ t('proxies.global') }}</RouterLink
          ><RouterLink
            v-for="reference in detail?.groups"
            :key="reference.group_id"
            :to="{ name: 'modern-group-detail', params: { id: reference.group_id } }"
            >{{ reference.group_name }} · {{ t('proxies.openGroup') }}</RouterLink
          ><RouterLink
            v-for="reference in detail?.credentials"
            :key="reference.credential_id"
            :to="{
              name: 'modern-group-detail',
              params: { id: reference.group_id },
              query: { credential: reference.credential_id },
            }"
            >{{ reference.group_name }} ·
            {{ reference.label || t('proxies.openCredential') }}</RouterLink
          >
          <p v-if="detail && !detail.global && !detail.groups.length && !detail.credentials.length">
            {{ t('proxies.noReferences') }}
          </p>
        </div></AppDialogContent
      ></DialogRoot
    >
  </div>
</template>

<style scoped>
.modern-proxies {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  gap: var(--modern-space-3);
}
.modern-proxy-toolbar,
.modern-proxy-test-target,
.modern-proxy-primary,
.modern-proxy-batch,
.modern-proxy-progress,
.modern-proxy-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-proxy-toolbar {
  flex-wrap: wrap;
}
.modern-proxy-toolbar > :first-child {
  flex: 1 1 200px;
}
.modern-proxy-primary {
  margin-left: auto;
}
.modern-proxy-test-target {
  flex-wrap: wrap;
  align-items: end;
}
.modern-proxy-test-target > :first-child {
  flex: 1 1 320px;
}
.modern-proxy-test-target > span {
  color: var(--modern-muted);
  padding-bottom: var(--modern-space-2);
}
.modern-proxy-batch {
  padding: var(--modern-space-2) var(--modern-space-3);
  min-height: var(--modern-control-md);
}
.modern-proxy-row {
  display: grid;
  grid-template-columns:
    28px minmax(200px, 2fr) 80px 90px minmax(150px, 1fr) minmax(180px, 1fr)
    150px;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  min-width: 1050px;
}
.modern-proxy-heading {
  color: var(--modern-muted);
  background: var(--modern-surface);
}
.modern-proxy-identity,
.modern-proxy-result {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: var(--modern-space-1);
}
.modern-proxy-identity > :last-child,
.modern-proxy-result small {
  color: var(--modern-muted);
}
.modern-proxy-references {
  justify-content: flex-start;
  flex-wrap: wrap;
}
.modern-proxy-progress {
  justify-content: space-between;
  color: var(--modern-muted);
}
.modern-proxy-empty {
  padding: var(--modern-space-6);
  text-align: center;
  color: var(--modern-muted);
}
.modern-proxy-panel {
  display: grid;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
  overflow-y: auto;
}
.modern-proxy-panel a {
  color: var(--modern-accent);
  text-decoration: none;
}
</style>
