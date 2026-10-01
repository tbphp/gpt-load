<script setup lang="ts">
import { Plus, Upload, Play, Pause, Trash2, Pencil, FlaskConical } from '@lucide/vue'
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
import { useRoute, useRouter, type LocationQuery } from 'vue-router'
import ProxyEditor from './ProxyEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import DataTable from '@/components/ui/DataTable.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const { t, n, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const route = useRoute()
const router = useRouter()
const positivePage = (value: unknown) =>
  Number.isSafeInteger(Number(value)) && Number(value) > 0 ? Number(value) : 1
const parseFilters = (query: LocationQuery): ProxyFilters => ({
  q: String(query.q ?? ''),
  state: String(query.state ?? ''),
  scheme: String(query.scheme ?? ''),
  used: String(query.used ?? ''),
  test: String(query.test ?? ''),
  sort: query.sort === 'latency' ? 'latency' : 'name',
  page: positivePage(query.page),
  page_size: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
})
const filters = ref(parseFilters(route.query))
watch(
  () => route.query,
  (query) => {
    const value = parseFilters(query)
    if (JSON.stringify(value) !== JSON.stringify(filters.value)) {
      filters.value = value
      selected.value = new Set()
    }
  },
)
watch(filters, (value) => {
  void router
    .replace({
      query: Object.fromEntries(
        Object.entries(value)
          .filter(([, v]) => v !== '')
          .map(([key, value]) => [key, String(value)]),
      ),
    })
    .catch(() => {
      error.value = t('proxies.operationFailed')
    })
})

const query = useQuery({
  queryKey: computed(() => [...proxyListKey, filters.value]),
  queryFn: ({ signal }) => listProxies(client, filters.value, signal),
  placeholderData: keepPreviousData,
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
  <section class="proxy-page">
    <PageHeader :title="t('proxies.title')" appearance="ledger"
      ><template #actions
        ><AppButton variant="secondary" :busy="query.isFetching.value" @click="query.refetch()">{{
          t('common.refresh')
        }}</AppButton
        ><AppButton variant="secondary" @click="importing = true"
          ><Upload :size="16" />{{ t('proxies.import') }}</AppButton
        ><AppButton @click="editor = null"
          ><Plus :size="16" />{{ t('proxies.new') }}</AppButton
        ></template
      ></PageHeader
    >
    <div class="proxy-toolbar">
      <AppTextInput
        v-model="search"
        :label="t('proxies.search')"
        :placeholder="t('proxies.search')"
      />
      <AppSelect
        :model-value="filters.scheme"
        :options="schemes"
        :label="t('proxies.protocol')"
        @update:model-value="change({ scheme: $event })"
      />
      <AppSelect
        :model-value="filters.used"
        :options="usages"
        :label="t('proxies.references')"
        @update:model-value="change({ used: $event })"
      />
      <AppSelect
        :model-value="filters.test"
        :options="tests"
        :label="t('proxies.latestTest')"
        @update:model-value="change({ test: $event })"
      />
      <AppSelect
        :model-value="filters.sort"
        :options="sorts"
        :label="t('proxies.sortName')"
        @update:model-value="change({ sort: $event })"
      />
    </div>
    <div class="proxy-test-target">
      <label for="proxy-test-url">{{ t('proxies.testURL') }}</label
      ><AppTextInput
        id="proxy-test-url"
        v-model="target"
        :label="t('proxies.testURL')"
        :disabled="!initialized"
        :invalid="Boolean(target) && !validTestURL(target.trim())"
        @blur="saveTarget"
      /><span role="status">{{ t('proxies.' + targetState) }}</span>
      <AppButton v-if="targetState === 'saveFailed'" variant="secondary" @click="saveTarget">{{
        t('proxies.save')
      }}</AppButton>
      <AppButton
        variant="secondary"
        :disabled="running || preparing || !query.data.value?.total || !validTestURL(target.trim())"
        :busy="preparing"
        @click="runFiltered"
        >{{ t('proxies.testFiltered') }}
        <span v-if="query.data.value">({{ n(query.data.value.total) }})</span></AppButton
      >
    </div>
    <SegmentedControl
      :model-value="filters.state"
      :options="states"
      :label="t('proxies.status')"
      @update:model-value="change({ state: $event })"
    />
    <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback
    ><InlineFeedback v-if="notice" tone="success">{{ notice }}</InlineFeedback>
    <div v-if="total" class="proxy-progress" role="status" aria-live="polite">
      <span>{{ progress }}</span
      ><AppButton
        v-if="running"
        variant="secondary"
        size="compact"
        @click="testController?.abort()"
        >{{ t('proxies.stop') }}</AppButton
      ><AppButton
        v-else-if="failed.length"
        variant="secondary"
        size="compact"
        @click="run([...failed])"
        >{{ t('proxies.retryFailed') }}</AppButton
      >
    </div>
    <div class="proxy-batch">
      <label class="proxy-checkbox"
        ><input
          type="checkbox"
          :checked="allSelected"
          :indeterminate="selected.size > 0 && !allSelected"
          :aria-label="t('proxies.selectPage')"
          @change="
            selected = ($event.target as HTMLInputElement).checked
              ? new Set(rows.map((row) => row.id))
              : new Set()
          " /></label
      ><template v-if="selected.size"
        ><span>{{ t('proxies.selected', { count: n(selected.size) }) }}</span
        ><AppButton
          size="compact"
          variant="secondary"
          :disabled="running || !validTestURL(target.trim())"
          @click="run([...selected])"
          >{{ t('proxies.testSelected') }}</AppButton
        ><IconButton
          :label="t('proxies.enable')"
          :disabled="pending"
          @click="act('enable', [...selected])"
          ><Play :size="16" /></IconButton
        ><IconButton
          :label="t('proxies.disable')"
          :disabled="pending"
          @click="act('disable', [...selected])"
          ><Pause :size="16" /></IconButton
        ><IconButton
          :label="t('proxies.delete')"
          :disabled="pending"
          @click="act('delete', [...selected])"
          ><Trash2 :size="16" /></IconButton
      ></template>
    </div>
    <div class="proxy-table">
      <DataTable :caption="t('proxies.title')" appearance="editorial" dense>
        <thead>
          <tr>
            <th />
            <th>{{ t('proxies.name') }}</th>
            <th>{{ t('proxies.protocol') }}</th>
            <th>{{ t('proxies.status') }}</th>
            <th>{{ t('proxies.references') }}</th>
            <th>{{ t('proxies.latestTest') }}</th>
            <th>{{ t('proxies.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="query.isError.value">
            <td colspan="7">{{ t('proxies.loadFailed') }}</td>
          </tr>
          <tr v-else-if="!rows.length">
            <td colspan="7">{{ t('proxies.noResults') }}</td>
          </tr>
          <tr v-for="row in rows" :key="row.id">
            <td>
              <input
                type="checkbox"
                :checked="selected.has(row.id)"
                :aria-label="row.name"
                @change="toggle(row.id, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td>
              <div class="proxy-identity">
                <OverflowTooltip :content="row.name">{{ row.name }}</OverflowTooltip
                ><OverflowTooltip :content="row.display_url"
                  ><small>{{ row.display_url }}</small></OverflowTooltip
                >
              </div>
            </td>
            <td>{{ row.scheme.toUpperCase() }}</td>
            <td>
              <StatusBadge :tone="row.enabled ? 'success' : 'neutral'">{{
                t(row.enabled ? 'proxies.enabled' : 'proxies.disabled')
              }}</StatusBadge>
            </td>
            <td>
              <AppButton variant="ghost" size="compact" @click="showReferences(row.id)"
                >{{ t('proxies.groups', { count: n(row.group_count) }) }} ·
                {{ t('proxies.credentials', { count: n(row.credential_count) })
                }}<StatusBadge v-if="row.global" tone="info">{{
                  t('proxies.global')
                }}</StatusBadge></AppButton
              >
            </td>
            <td>
              <span v-if="row.last_test_at_ms === null">{{ t('proxies.untested') }}</span>
              <div v-else class="proxy-result">
                <OverflowTooltip :content="row.last_test_url"
                  >{{
                    row.last_test_error
                      ? t('proxies.' + row.last_test_error)
                      : `HTTP ${row.last_test_status_code}`
                  }}
                  · {{ n(row.last_test_duration_ms ?? 0) }} ms</OverflowTooltip
                ><small>{{ time(row.last_test_at_ms) }}</small>
              </div>
            </td>
            <td>
              <div class="proxy-actions">
                <IconButton
                  :label="t('proxies.test')"
                  :busy="testing.has(row.id)"
                  :disabled="running || !validTestURL(target.trim())"
                  @click="run([row.id])"
                  ><FlaskConical :size="16" /></IconButton
                ><IconButton :label="t('proxies.edit')" @click="editor = row"
                  ><Pencil :size="16" /></IconButton
                ><IconButton
                  :label="t(row.enabled ? 'proxies.disable' : 'proxies.enable')"
                  :disabled="pending"
                  @click="act(row.enabled ? 'disable' : 'enable', [row.id])"
                  ><Pause v-if="row.enabled" :size="16" /><Play v-else :size="16" /></IconButton
                ><IconButton
                  :label="t('proxies.delete')"
                  :disabled="pending"
                  @click="act('delete', [row.id])"
                  ><Trash2 :size="16"
                /></IconButton>
              </div>
            </td>
          </tr>
        </tbody>
      </DataTable>
    </div>
    <PaginationBar
      :page="filters.page"
      :page-size="filters.page_size"
      :total-items="query.data.value?.total ?? 0"
      :total-pages="Math.ceil((query.data.value?.total ?? 0) / filters.page_size)"
      :pending="query.isFetching.value"
      @previous="change({ page: filters.page - 1 })"
      @next="change({ page: filters.page + 1 })"
      @update:page-size="change({ page_size: $event })"
    />
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
      :close-label="t('proxies.cancel')"
      :cancel-label="t('proxies.cancel')"
      :confirm-label="t('proxies.' + (confirmation?.action ?? 'delete'))"
      tone="danger"
      :pending="pending"
      @update:open="!$event && (confirmation = undefined)"
      @confirm="confirm"
      ><InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback></AppConfirmDialog
    >
    <AppDrawer
      :open="importing"
      :title="t('proxies.import')"
      :description="t('proxies.importHelp')"
      :close-label="t('proxies.cancel')"
      :dismissible="!pending"
      @update:open="importing = $event"
      ><form class="proxy-panel" @submit.prevent="importRows">
        <label for="proxy-import">{{ t('proxies.importHelp') }}</label
        ><textarea
          id="proxy-import"
          v-model="importText"
          :disabled="pending"
          :aria-label="t('proxies.address')"
          autocomplete="off"
          spellcheck="false"
          rows="12"
        /><InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback
        ><AppButton type="submit" :busy="pending" :disabled="!importText.trim()">{{
          t('proxies.import')
        }}</AppButton>
      </form></AppDrawer
    >
    <AppDrawer
      :open="Boolean(detail)"
      :title="t('proxies.references')"
      :description="t('proxies.inheritWarning')"
      :close-label="t('proxies.cancel')"
      @update:open="!$event && (detail = undefined)"
      ><div class="proxy-panel">
        <RouterLink
          v-if="detail?.global"
          :to="{ name: 'settings', query: { section: 'connection' } }"
          >{{ t('proxies.global') }}</RouterLink
        ><RouterLink
          v-for="reference in detail?.groups"
          :key="reference.group_id"
          :to="{ name: 'group-detail', params: { id: reference.group_id } }"
          >{{ reference.group_name }} · {{ t('proxies.openGroup') }}</RouterLink
        ><RouterLink
          v-for="reference in detail?.credentials"
          :key="reference.credential_id"
          :to="{
            name: 'group-detail',
            params: { id: reference.group_id },
            query: { tab: 'credentials', expanded_credential_ids: String(reference.credential_id) },
          }"
          >{{ reference.group_name }} ·
          {{ reference.label || t('proxies.openCredential') }}</RouterLink
        >
        <p v-if="detail && !detail.global && !detail.groups.length && !detail.credentials.length">
          {{ t('proxies.noReferences') }}
        </p>
      </div></AppDrawer
    >
  </section>
</template>

<style scoped>
.proxy-page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}
.proxy-toolbar,
.proxy-test-target,
.proxy-batch,
.proxy-progress,
.proxy-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}
.proxy-toolbar > :first-child {
  flex: 1 1 200px;
}
.proxy-test-target > :nth-child(2) {
  flex: 1 1 300px;
}
.proxy-test-target > span,
.proxy-result small,
.proxy-identity small {
  color: var(--color-text-muted);
}
.proxy-progress {
  justify-content: space-between;
}
.proxy-batch {
  min-height: var(--control-md);
}
.proxy-identity,
.proxy-result {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-width: 340px;
}
.proxy-table {
  min-width: 0;
}
.proxy-panel {
  display: grid;
  gap: var(--space-4);
}
.proxy-panel textarea {
  width: 100%;
  resize: vertical;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  font-family: var(--font-mono);
}
.proxy-panel a {
  color: var(--color-accent);
}
</style>
