<script setup lang="ts">
import {
  Plus,
  Upload,
  Play,
  Pause,
  Trash2,
  Pencil,
  Search,
  FlaskConical,
  Network,
  RefreshCw,
  X,
} from '@lucide/vue'
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
  AppCollectionState,
  AppConfirmDialog,
  AppDialogContent,
  AppDialogHeader,
  AppFilterSummary,
  AppIcon,
  AppIconButton,
  AppListFrame,
  AppNotice,
  AppOverflowText,
  AppPagination,
  AppSegmentedControl,
  AppSelect,
  AppSortMenu,
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
const targetSaveFailed = ref(false)
const savingTarget = ref(false)
const targetState = computed(() => {
  const value = target.value.trim()
  if (!savedTarget.value || !validTestURL(value)) return ''
  if (savingTarget.value) return 'saving'
  if (value === savedTarget.value) return 'saved'
  return targetSaveFailed.value ? 'saveFailed' : 'saving'
})
let initialized = false
let targetTimer: ReturnType<typeof setTimeout> | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let alive = true
watch(
  query.data,
  (value) => {
    if (!value) return
    if (!initialized || (!savingTarget.value && target.value.trim() === savedTarget.value)) {
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
  targetSaveFailed.value = false
  if (
    initialized &&
    target.value.trim() !== savedTarget.value &&
    validTestURL(target.value.trim())
  ) {
    targetTimer = setTimeout(() => void saveTarget(), 500)
  }
})
async function saveTarget() {
  if (savingTarget.value || !initialized || !validTestURL(target.value.trim())) return
  savingTarget.value = true
  targetSaveFailed.value = false
  try {
    while (validTestURL(target.value.trim()) && target.value.trim() !== savedTarget.value) {
      const value = target.value.trim()
      await saveProxyTestURL(client, value)
      savedTarget.value = value
      cache.setQueriesData<ProxyList>({ queryKey: proxyListKey }, (previous) =>
        previous ? { ...previous, test_url: value } : previous,
      )
    }
  } catch {
    targetSaveFailed.value = true
  } finally {
    savingTarget.value = false
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
const filterDefaults = { q: '', state: '', scheme: '', used: '', test: '', sort: 'name' }
const activeFilters = computed(() =>
  [
    { key: 'q', label: t('proxies.search'), value: filters.value.q },
    {
      key: 'state',
      label: t('proxies.status'),
      value: states.value.find((option) => option.value === filters.value.state)?.label ?? '',
    },
    {
      key: 'scheme',
      label: t('proxies.protocol'),
      value: schemes.value.find((option) => option.value === filters.value.scheme)?.label ?? '',
    },
    {
      key: 'used',
      label: t('proxies.references'),
      value: usages.value.find((option) => option.value === filters.value.used)?.label ?? '',
    },
    {
      key: 'test',
      label: t('proxies.latestTest'),
      value: tests.value.find((option) => option.value === filters.value.test)?.label ?? '',
    },
    {
      key: 'sort',
      label: t('proxies.sort'),
      value: sorts.value.find((option) => option.value === filters.value.sort)?.label ?? '',
    },
  ].filter(({ key }) => {
    const field = key as keyof typeof filterDefaults
    return filters.value[field] !== filterDefaults[field]
  }),
)
const hasFilters = computed(() => activeFilters.value.some(({ key }) => key !== 'sort'))
function resetFilter(key?: string) {
  if (!key || key === 'q') {
    clearTimeout(searchTimer)
    search.value = ''
  }
  change(key ? { [key]: filterDefaults[key as keyof typeof filterDefaults] } : filterDefaults)
}
const editor = ref<ProxyItem | null | undefined>()
const importing = ref(false)
const importText = ref('')
const notice = ref('')
const error = ref('')
const pending = ref(false)
const impact = ref<ProxyImpact>()
const hasReferences = computed(() =>
  Boolean(impact.value?.global || impact.value?.groups.length || impact.value?.credentials.length),
)
const confirmation = ref<{ ids: number[]; action: 'disable' | 'delete' }>()
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
    if (action !== 'enable') {
      impact.value = await proxyImpact(client, ids)
      if (action === 'delete' || hasReferences.value) {
        confirmation.value = { ids, action }
        return
      }
    }
    await batchProxies(client, ids, action)
    selected.value = new Set()
    await refresh()
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
  if (!confirmation.value) return ''
  if (!hasReferences.value) {
    return t('proxies.deleteWarning', { count: n(confirmation.value.ids.length) })
  }
  return t('proxies.confirmWarning', { action: t('proxies.' + confirmation.value.action) })
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
        class="modern-proxy-search"
        :icon="Search"
        type="search"
        :label="t('proxies.search')"
        label-hidden
        :placeholder="t('proxies.search')"
      />
      <AppSelect
        class="modern-proxy-filter"
        :model-value="filters.scheme"
        :options="schemes"
        :label="t('proxies.protocol')"
        label-hidden
        @update:model-value="change({ scheme: $event })"
      />
      <AppSelect
        class="modern-proxy-filter"
        :model-value="filters.used"
        :options="usages"
        :label="t('proxies.references')"
        label-hidden
        @update:model-value="change({ used: $event })"
      />
      <AppSelect
        class="modern-proxy-filter"
        :model-value="filters.test"
        :options="tests"
        :label="t('proxies.latestTest')"
        label-hidden
        @update:model-value="change({ test: $event })"
      />
      <div class="modern-proxy-primary">
        <AppSortMenu
          :model-value="filters.sort"
          :options="sorts"
          :label="t('proxies.sort')"
          @update:model-value="change({ sort: $event })"
        />
        <AppButton :icon="Upload" @click="importing = true">{{ t('proxies.import') }}</AppButton>
        <AppButton :icon="Plus" variant="primary" @click="editor = null">{{
          t('proxies.new')
        }}</AppButton>
      </div>
    </div>
    <div class="modern-proxy-testbar">
      <div class="modern-proxy-test-target">
        <span class="modern-proxy-test-label" aria-hidden="true">{{ t('proxies.testURL') }}</span>
        <AppTextField
          v-model="target"
          class="modern-proxy-test-input"
          :label="t('proxies.testURL')"
          label-hidden
          type="url"
          :disabled="!initialized"
          :error="target && !validTestURL(target.trim()) ? t('proxies.invalidURL') : undefined"
          @blur="saveTarget"
        />
      </div>
      <div v-if="targetState" class="modern-proxy-save-state" role="status">
        <span :class="{ 'modern-proxy-save-error': targetSaveFailed }">{{
          t('proxies.' + targetState)
        }}</span>
        <AppButton v-if="targetState === 'saveFailed'" variant="text" size="sm" @click="saveTarget">
          {{ t('proxies.save') }}
        </AppButton>
      </div>
      <AppTooltip :label="t('proxies.testHelp')"
        ><AppButton
          :icon="FlaskConical"
          :disabled="
            running || preparing || !query.data.value?.total || !validTestURL(target.trim())
          "
          :loading="preparing"
          @click="runFiltered"
          >{{ t('proxies.testFiltered') }}
          <span v-if="query.data.value">({{ n(query.data.value.total) }})</span></AppButton
        ></AppTooltip
      >
      <div class="modern-proxy-progress" role="status" aria-live="polite">
        <template v-if="total">
          <AppOverflowText class="modern-proxy-progress-text" :text="progress" />
          <AppIconButton
            v-if="running"
            :icon="X"
            :label="t('proxies.stop')"
            size="sm"
            @click="testController?.abort()"
          />
          <AppIconButton
            v-else-if="failed.length"
            :icon="RefreshCw"
            :label="t('proxies.retryFailed')"
            size="sm"
            @click="run([...failed])"
          />
        </template>
      </div>
    </div>
    <div class="modern-proxy-filterbar">
      <AppSegmentedControl
        :model-value="filters.state"
        :options="states"
        :label="t('proxies.status')"
        @update:model-value="change({ state: $event })"
      />
      <AppFilterSummary :items="activeFilters" @remove="resetFilter" @reset="resetFilter()" />
    </div>
    <div
      v-if="error || notice || (query.isError.value && query.data.value)"
      class="modern-proxy-notices"
    >
      <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
      <AppNotice v-if="notice">{{ notice }}</AppNotice>
      <AppNotice v-if="query.isError.value && query.data.value" tone="warning">
        {{ t('collection.stale') }}
        <AppButton variant="text" size="sm" @click="query.refetch()">{{
          t('collection.retry')
        }}</AppButton>
      </AppNotice>
    </div>
    <AppListFrame
      :label="t('proxies.title')"
      :loading="Boolean(query.data.value) && query.isFetching.value"
    >
      <template #header>
        <div class="modern-proxy-batch">
          <AppCheckbox
            :model-value="allSelected"
            :indeterminate="selected.size > 0 && !allSelected"
            :label="t('proxies.selectPage')"
            :disabled="pending || !rows.length"
            @update:model-value="selected = $event ? new Set(rows.map((row) => row.id)) : new Set()"
          />
          <template v-if="selected.size">
            <span>{{ t('proxies.selected', { count: n(selected.size) }) }}</span>
            <AppIconButton
              :icon="FlaskConical"
              :label="t('proxies.testSelected')"
              size="sm"
              :disabled="running || !validTestURL(target.trim())"
              @click="run([...selected])"
            />
            <AppIconButton
              :icon="Play"
              :label="t('proxies.enable')"
              size="sm"
              :disabled="pending"
              @click="act('enable', [...selected])"
            />
            <AppIconButton
              :icon="Pause"
              :label="t('proxies.disable')"
              size="sm"
              :disabled="pending"
              @click="act('disable', [...selected])"
            />
            <AppIconButton
              :icon="Trash2"
              :label="t('proxies.delete')"
              size="sm"
              :disabled="pending"
              @click="act('delete', [...selected])"
            />
          </template>
        </div>
        <div class="modern-proxy-row modern-proxy-heading">
          <span />
          <span>{{ t('proxies.name') }}</span>
          <span>{{ t('proxies.protocol') }}</span>
          <span>{{ t('proxies.status') }}</span>
          <span>{{ t('proxies.references') }}</span>
          <span>{{ t('proxies.latestTest') }}</span>
          <span>{{ t('proxies.actions') }}</span>
        </div>
      </template>
      <AppCollectionState
        v-if="!query.data.value && query.isPending.value"
        :title="t('collection.loading')"
        loading
      />
      <AppCollectionState v-else-if="!query.data.value" :title="t('proxies.loadFailed')" error>
        <AppButton @click="query.refetch()">{{ t('collection.retry') }}</AppButton>
      </AppCollectionState>
      <AppCollectionState
        v-else-if="!rows.length"
        :icon="hasFilters ? Search : Network"
        :title="t(hasFilters ? 'proxies.noResults' : 'proxies.empty')"
      >
        <AppButton v-if="hasFilters" @click="resetFilter()">{{ t('collection.reset') }}</AppButton>
      </AppCollectionState>
      <div
        v-for="row in rows"
        :key="row.id"
        class="modern-proxy-row modern-proxy-item"
        :class="{ 'is-selected': selected.has(row.id) }"
      >
        <AppCheckbox
          :model-value="selected.has(row.id)"
          :label="row.name || row.display_url"
          label-hidden
          @update:model-value="toggle(row.id, $event)"
        />
        <div class="modern-proxy-identity">
          <span class="modern-proxy-avatar"><AppIcon :icon="Network" /></span>
          <div class="modern-proxy-description">
            <AppOverflowText class="modern-proxy-name" :text="row.name || row.display_url" />
            <AppOverflowText v-if="row.name" class="modern-proxy-address" :text="row.display_url" />
          </div>
        </div>
        <AppBadge variant="outline" mono>{{ row.scheme.toUpperCase() }}</AppBadge>
        <AppBadge :tone="row.enabled ? 'success' : 'neutral'" variant="plain" dot>{{
          t(row.enabled ? 'proxies.enabled' : 'proxies.disabled')
        }}</AppBadge>
        <div class="modern-proxy-references">
          <span v-if="row.group_count">
            {{ t('proxies.groups', { count: n(row.group_count) }) }}
          </span>
          <span v-if="row.credential_count">
            {{ t('proxies.credentials', { count: n(row.credential_count) }) }}
          </span>
          <AppBadge v-if="row.global" tone="brand" size="xs">{{ t('proxies.global') }}</AppBadge>
          <span v-if="!row.group_count && !row.credential_count && !row.global">—</span>
        </div>
        <div class="modern-proxy-result">
          <AppBadge v-if="row.last_test_at_ms === null" dot>{{ t('proxies.untested') }}</AppBadge>
          <template v-else>
            <div class="modern-proxy-result-summary">
              <AppTooltip
                :label="
                  [
                    row.last_test_error
                      ? t('proxies.' + row.last_test_error)
                      : `HTTP ${row.last_test_status_code}`,
                    row.last_test_url,
                  ]
                    .filter(Boolean)
                    .join(' · ')
                "
              >
                <AppBadge :tone="proxyTestSucceeded(row) ? 'success' : 'danger'" dot>
                  {{ t(proxyTestSucceeded(row) ? 'proxies.success' : 'proxies.failed') }}
                </AppBadge>
              </AppTooltip>
              <span v-if="row.last_test_duration_ms !== null" class="modern-proxy-latency">
                {{ n(row.last_test_duration_ms) }} ms
              </span>
            </div>
            <AppOverflowText class="modern-proxy-result-time" :text="time(row.last_test_at_ms)" />
          </template>
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
    >
      <div
        v-if="impact && hasReferences"
        class="modern-proxy-impact"
        :aria-label="t('proxies.references')"
      >
        <AppBadge v-if="impact.groups.length">
          {{ t('proxies.groups', { count: n(impact.groups.length) }) }}
        </AppBadge>
        <AppBadge v-if="impact.credentials.length">
          {{ t('proxies.credentials', { count: n(impact.credentials.length) }) }}
        </AppBadge>
        <AppBadge v-if="impact.global" tone="brand">{{ t('proxies.global') }}</AppBadge>
      </div>
    </AppConfirmDialog>
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
  </div>
</template>

<style scoped>
.modern-proxies {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.modern-proxy-toolbar,
.modern-proxy-testbar,
.modern-proxy-test-target,
.modern-proxy-save-state,
.modern-proxy-primary,
.modern-proxy-filterbar,
.modern-proxy-batch,
.modern-proxy-progress,
.modern-proxy-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-proxy-toolbar {
  flex: none;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-proxy-search {
  flex: 2 1 260px;
  min-width: 0;
}
.modern-proxy-filter {
  flex: 1 1 130px;
  min-width: 0;
}
.modern-proxy-primary {
  flex: none;
  flex-wrap: wrap;
  justify-content: flex-end;
  max-width: 100%;
  margin-left: auto;
}
.modern-proxy-testbar {
  flex: none;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding-bottom: var(--modern-space-4);
}
.modern-proxy-test-target {
  min-width: 0;
  max-width: 100%;
  align-items: flex-start;
}
.modern-proxy-test-label {
  display: flex;
  flex: none;
  min-height: var(--modern-control-md);
  align-items: center;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-proxy-test-input {
  width: 360px;
  min-width: 0;
}
.modern-proxy-save-state {
  min-height: var(--modern-control-md);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-proxy-save-error {
  color: var(--modern-danger);
}
.modern-proxy-filterbar {
  flex: none;
  flex-wrap: wrap;
  justify-content: space-between;
  padding-bottom: var(--modern-space-4);
}
.modern-proxy-notices {
  display: grid;
  flex: none;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-3);
}
.modern-proxy-batch {
  flex: none;
  min-height: var(--modern-control-nav);
  padding: 0 var(--modern-space-3) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  white-space: nowrap;
}
.modern-proxy-batch > :first-child {
  flex: none;
  margin-right: var(--modern-space-1);
}
.modern-proxy-row {
  display: grid;
  grid-template-columns:
    var(--modern-checkbox-size) minmax(240px, 2fr) 80px 90px minmax(170px, 1fr)
    minmax(200px, 1fr) 168px;
  align-items: center;
  gap: var(--modern-space-4);
  padding: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  min-width: 1120px;
}
.modern-proxy-heading {
  color: var(--modern-muted);
  background: var(--modern-surface);
  font-size: var(--modern-font-size-small);
}
.modern-proxy-item {
  transition: background-color var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-proxy-item:hover,
.modern-proxy-item:focus-within {
  background: var(--modern-control-hover);
}
.modern-proxy-item.is-selected {
  background: var(--modern-accent-soft);
}
.modern-proxy-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-proxy-avatar {
  display: flex;
  width: var(--modern-control-sm);
  height: var(--modern-control-sm);
  flex: none;
  align-items: center;
  justify-content: center;
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  color: var(--modern-muted);
}
.modern-proxy-description,
.modern-proxy-result {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: var(--modern-space-1);
}
.modern-proxy-address,
.modern-proxy-result-time {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-proxy-address {
  font-family: var(--modern-font-mono);
}
.modern-proxy-name {
  font-weight: var(--modern-weight-semibold);
}
.modern-proxy-result-summary {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-proxy-latency {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.modern-proxy-references {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-proxy-impact {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-proxy-progress {
  flex: 1 1 0;
  min-width: var(--modern-control-md);
  min-height: var(--modern-control-md);
  justify-content: flex-end;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-proxy-progress-text {
  min-width: 0;
  flex: 0 1 auto;
}
.modern-proxy-panel {
  display: grid;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
  overflow-y: auto;
}
@media (max-width: 1150px) {
  .modern-proxy-search {
    flex-basis: 100%;
  }
}
@media (max-width: 760px) {
  .modern-proxy-toolbar {
    gap: var(--modern-space-2);
  }
  .modern-proxy-filter {
    flex-basis: 120px;
  }
  .modern-proxy-test-target {
    width: 100%;
  }
  .modern-proxy-test-input {
    flex: 1;
    width: auto;
  }
  .modern-proxy-test-label,
  .modern-proxy-save-state,
  .modern-proxy-progress {
    min-height: var(--modern-touch-target);
  }
}
</style>
