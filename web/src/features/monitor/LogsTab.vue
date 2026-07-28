<script setup lang="ts">
import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from '@tanstack/vue-query'
import { ListFilter } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { accessKeyOptionsQueryOptions } from '@/app/resources/access-keys'
import { groupListQueryOptions } from '@/app/resources/groups'
import {
  listRequestLogs,
  requestLogInfiniteQueryOptions,
  type RequestLogItemDto,
  type RequestLogPageDto,
} from '@/app/resources/request-logs'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  applyLogFilterDraft,
  createLogFilterDraft,
  parseAppliedLogFilters,
  serializeAppliedLogFilters,
  validateLogFilterDraft,
  type LogFilterDraft,
  type LogFilterErrors,
} from './log-filters'
import LogDetailDrawer from './LogDetailDrawer.vue'
import { parseSelectedRequestID } from './monitor-route'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const { locale, t } = useI18n()
const appliedFilters = computed(() => parseAppliedLogFilters(route.query))
const draft = ref(createLogFilterDraft(appliedFilters.value))
const filterErrors = ref<LogFilterErrors>({})
const selectedRequestID = computed(() => parseSelectedRequestID(route.query))
const refreshPending = ref(false)
const refreshFailed = ref(false)
let refreshOwner = 0
let refreshController: AbortController | undefined
let detailFocusTimer: number | undefined
const groupsQuery = useQuery(groupListQueryOptions(client))
const accessKeyOptionsQuery = useQuery(accessKeyOptionsQueryOptions(client))
const logsQuery = useInfiniteQuery(requestLogInfiniteQueryOptions(client, appliedFilters))
const logs = computed(() => {
  const unique: RequestLogItemDto[] = []
  const seen = new Set<string>()
  for (const page of logsQuery.data.value?.pages ?? []) {
    for (const log of page.items) {
      if (seen.has(log.request_id)) continue
      seen.add(log.request_id)
      unique.push(log)
    }
  }
  return unique
})
const hasAppliedFilters = computed(() => Object.keys(appliedFilters.value).length > 0)
const appliedDraft = computed(() => createLogFilterDraft(appliedFilters.value))
const draftIsDirty = computed(() =>
  (Object.keys(draft.value) as Array<keyof LogFilterDraft>).some(
    (field) => draft.value[field] !== appliedDraft.value[field],
  ),
)
const unsavedChanges = useUnsavedChanges(draftIsDirty)
const appliedFilterSignature = computed(() =>
  JSON.stringify(serializeAppliedLogFilters(appliedFilters.value)),
)
const currentTimezone = computed(
  () => new Intl.DateTimeFormat(locale.value).resolvedOptions().timeZone || 'UTC',
)
const lastSuccessfulRefreshAt = computed(() =>
  logsQuery.dataUpdatedAt.value > 0 ? new Date(logsQuery.dataUpdatedAt.value) : null,
)
const logsAreStale = computed(() => refreshFailed.value || logsQuery.isRefetchError.value)
const appliedFilterChips = computed(() => {
  const filters = appliedFilters.value
  const errors = validateLogFilterDraft(appliedDraft.value)
  const values: string[] = []
  if (appliedDraft.value.from && !errors.from) {
    values.push(t('monitor.logs.filters.appliedFrom', { value: appliedDraft.value.from }))
  }
  if (appliedDraft.value.to && !errors.to) {
    values.push(t('monitor.logs.filters.appliedTo', { value: appliedDraft.value.to }))
  }
  if (filters.group_id !== undefined && !errors.group_id) {
    const group = groupsQuery.data.value?.find(({ id }) => id === filters.group_id)
    values.push(
      t('monitor.logs.filters.appliedGroup', {
        value: group ? `${group.name} · #${group.id}` : `#${filters.group_id}`,
      }),
    )
  }
  if (filters.model !== undefined && !errors.model) {
    values.push(t('monitor.logs.filters.appliedModel', { value: filters.model }))
  }
  if (filters.access_key_id !== undefined && !errors.access_key_id) {
    const accessKey = accessKeyOptionsQuery.data.value?.find(
      ({ id }) => id === filters.access_key_id,
    )
    values.push(
      t('monitor.logs.filters.appliedAccessKey', {
        value: accessKey ? `${accessKey.name} · #${accessKey.id}` : `#${filters.access_key_id}`,
      }),
    )
  }
  if (filters.status !== undefined && !errors.status) {
    values.push(
      t('monitor.logs.filters.appliedStatus', {
        value: t(`monitor.logs.status.${filters.status}`),
      }),
    )
  }
  if (filters.request_id !== undefined && !errors.request_id) {
    values.push(t('monitor.logs.filters.appliedRequestId', { value: filters.request_id }))
  }
  return values
})

watch(appliedFilterSignature, () => {
  cancelManualRefresh()
  draft.value = createLogFilterDraft(appliedFilters.value)
  filterErrors.value = {}
})

function statusTone(
  status: 'success' | 'error' | 'incomplete' | 'canceled',
): 'success' | 'danger' | 'warning' | 'neutral' {
  if (status === 'success') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'incomplete') return 'warning'
  return 'neutral'
}

function accessKeyLabel(log: RequestLogItemDto): string {
  if (log.access_key.deleted) {
    return t('monitor.logs.accessKey.deleted', { id: log.access_key.id })
  }
  if (log.access_key.name) {
    return t('monitor.logs.accessKey.named', {
      id: log.access_key.id,
      name: log.access_key.name,
    })
  }
  return `#${log.access_key.id}`
}

function formatLocalInstant(value: Date): string {
  return new Intl.DateTimeFormat(locale.value, {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(value)
}

function loadMore(): void {
  if (refreshPending.value || !logsQuery.hasNextPage.value || logsQuery.isFetchingNextPage.value) {
    return
  }
  void logsQuery.fetchNextPage()
}

async function applyFilters(): Promise<void> {
  const errors = validateLogFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return
  await unsavedChanges.runWithoutPrompt(() =>
    router.push({
      name: 'monitor',
      query: serializeAppliedLogFilters(applyLogFilterDraft(draft.value)),
    }),
  )
}

function filterError(field: keyof LogFilterDraft): string | undefined {
  const key = filterErrors.value[field]
  return key ? t(key) : undefined
}

function resetFilters(): void {
  filterErrors.value = {}
  draft.value = createLogFilterDraft()
}

async function setDetailOpen(requestID: string, open: boolean): Promise<void> {
  const query = serializeAppliedLogFilters(appliedFilters.value)
  if (open) query.selected_request_id = requestID
  await unsavedChanges.runWithoutPrompt(() =>
    open ? router.push({ name: 'monitor', query }) : router.replace({ name: 'monitor', query }),
  )
  if (open) return
  window.clearTimeout(detailFocusTimer)
  detailFocusTimer = window.setTimeout(() => {
    document.getElementById(`log-details-${requestID}`)?.focus()
  }, 30)
}

function cancelManualRefresh(): void {
  refreshOwner += 1
  refreshController?.abort()
  refreshController = undefined
  refreshPending.value = false
  refreshFailed.value = false
}

async function refreshFirstPage(): Promise<void> {
  if (refreshPending.value) return
  const owner = ++refreshOwner
  const filters = { ...appliedFilters.value }
  const queryKey = controlQueryKeys.logs.list(filters)
  refreshPending.value = true
  refreshFailed.value = false

  await queryClient.cancelQueries({ queryKey, exact: true })
  if (owner !== refreshOwner) return

  const controller = new AbortController()
  refreshController = controller
  try {
    const page = await listRequestLogs(client, filters, undefined, controller.signal)
    if (owner !== refreshOwner || controller.signal.aborted) return
    queryClient.setQueryData<InfiniteData<RequestLogPageDto, string | null>>(queryKey, {
      pages: [page],
      pageParams: [null],
    })
  } catch (error: unknown) {
    if (
      owner === refreshOwner &&
      !controller.signal.aborted &&
      !(error instanceof RequestCancelledError)
    ) {
      refreshFailed.value = true
    }
  } finally {
    if (owner === refreshOwner) {
      refreshController = undefined
      refreshPending.value = false
    }
  }
}

onBeforeUnmount(() => {
  cancelManualRefresh()
  window.clearTimeout(detailFocusTimer)
})
</script>

<template>
  <div class="logs-tab">
    <form
      class="logs-filter-form"
      data-test="logs-filter-form"
      :aria-label="t('monitor.logs.filters.label')"
      @submit.prevent="applyFilters"
    >
      <div class="logs-filter-grid">
        <FormField
          id="logs-from"
          :label="t('monitor.logs.filters.from')"
          :error="filterError('from')"
        >
          <template #default="{ describedBy }">
            <input
              id="logs-from"
              v-model="draft.from"
              data-test="logs-from"
              type="datetime-local"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('from') ? 'true' : undefined"
            />
          </template>
        </FormField>
        <FormField id="logs-to" :label="t('monitor.logs.filters.to')" :error="filterError('to')">
          <template #default="{ describedBy }">
            <input
              id="logs-to"
              v-model="draft.to"
              data-test="logs-to"
              type="datetime-local"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('to') ? 'true' : undefined"
            />
          </template>
        </FormField>
        <FormField
          id="logs-group"
          :label="t('monitor.logs.filters.group')"
          :description="t('monitor.logs.filters.groupHelp')"
          :error="filterError('group_id')"
        >
          <template #default="{ describedBy }">
            <select
              id="logs-group"
              v-model="draft.group_id"
              data-test="logs-group"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('group_id') ? 'true' : undefined"
              :disabled="groupsQuery.isError.value"
            >
              <option value="">{{ t('monitor.logs.filters.anyGroup') }}</option>
              <option
                v-if="
                  draft.group_id &&
                  !groupsQuery.data.value?.some((group) => String(group.id) === draft.group_id)
                "
                :value="draft.group_id"
              >
                #{{ draft.group_id }}
              </option>
              <option
                v-for="group in groupsQuery.data.value ?? []"
                :key="group.id"
                :value="String(group.id)"
              >
                {{ group.name }} · #{{ group.id }}
              </option>
            </select>
          </template>
        </FormField>
        <FormField
          id="logs-model"
          :label="t('monitor.logs.filters.model')"
          :error="filterError('model')"
        >
          <template #default="{ describedBy }">
            <input
              id="logs-model"
              v-model="draft.model"
              data-test="logs-model"
              type="text"
              autocomplete="off"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('model') ? 'true' : undefined"
            />
          </template>
        </FormField>
        <FormField
          id="logs-access-key"
          :label="t('monitor.logs.filters.accessKey')"
          :error="filterError('access_key_id')"
        >
          <template #default="{ describedBy }">
            <select
              id="logs-access-key"
              v-model="draft.access_key_id"
              data-test="logs-access-key"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('access_key_id') ? 'true' : undefined"
              :disabled="accessKeyOptionsQuery.isError.value"
            >
              <option value="">{{ t('monitor.logs.filters.anyAccessKey') }}</option>
              <option
                v-if="
                  draft.access_key_id &&
                  !accessKeyOptionsQuery.data.value?.some(
                    (key) => String(key.id) === draft.access_key_id,
                  )
                "
                :value="draft.access_key_id"
              >
                #{{ draft.access_key_id }}
              </option>
              <option
                v-for="key in accessKeyOptionsQuery.data.value ?? []"
                :key="key.id"
                :value="String(key.id)"
              >
                {{ key.name }} · #{{ key.id }}
              </option>
            </select>
          </template>
        </FormField>
        <FormField
          id="logs-status"
          :label="t('monitor.logs.filters.status')"
          :error="filterError('status')"
        >
          <template #default="{ describedBy }">
            <select
              id="logs-status"
              v-model="draft.status"
              data-test="logs-status"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('status') ? 'true' : undefined"
            >
              <option value="">{{ t('monitor.logs.filters.anyStatus') }}</option>
              <option value="success">{{ t('monitor.logs.status.success') }}</option>
              <option value="error">{{ t('monitor.logs.status.error') }}</option>
              <option value="incomplete">{{ t('monitor.logs.status.incomplete') }}</option>
              <option value="canceled">{{ t('monitor.logs.status.canceled') }}</option>
            </select>
          </template>
        </FormField>
        <FormField
          id="logs-request-id"
          :label="t('monitor.logs.filters.requestId')"
          :error="filterError('request_id')"
        >
          <template #default="{ describedBy }">
            <input
              id="logs-request-id"
              v-model="draft.request_id"
              data-test="logs-request-id"
              type="text"
              autocomplete="off"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('request_id') ? 'true' : undefined"
            />
          </template>
        </FormField>
      </div>
      <p v-if="draftIsDirty" class="logs-filter-dirty" data-test="logs-filter-dirty" role="status">
        <ListFilter :size="16" aria-hidden="true" />
        <span>{{ t('monitor.logs.filters.dirty') }}</span>
      </p>
      <div class="logs-filter-actions">
        <AppButton type="submit">{{ t('monitor.logs.filters.apply') }}</AppButton>
        <AppButton data-test="logs-reset" variant="ghost" @click="resetFilters">
          {{ t('monitor.logs.filters.reset') }}
        </AppButton>
        <AppButton
          data-test="logs-refresh"
          variant="secondary"
          :busy="refreshPending"
          @click="refreshFirstPage"
        >
          {{ t('monitor.logs.refresh') }}
        </AppButton>
      </div>
    </form>

    <section class="logs-applied" data-test="logs-applied-filters">
      <strong>{{ t('monitor.logs.filters.applied') }}</strong>
      <span v-for="value in appliedFilterChips" :key="value" class="logs-filter-chip">
        {{ value }}
      </span>
      <span v-if="appliedFilterChips.length === 0" class="logs-filter-chip">
        {{ t('monitor.logs.filters.noneApplied') }}
      </span>
    </section>
    <section class="logs-freshness" data-test="logs-freshness">
      <span data-test="logs-timezone">
        <strong>{{ t('monitor.logs.filters.timezone') }}</strong>
        {{ currentTimezone }}
      </span>
      <span v-if="lastSuccessfulRefreshAt">
        <strong>{{ t('monitor.logs.filters.lastRefreshed') }}</strong>
        <time data-test="logs-last-refreshed" :datetime="lastSuccessfulRefreshAt.toISOString()">
          {{ formatLocalInstant(lastSuccessfulRefreshAt) }}
        </time>
      </span>
    </section>

    <InlineFeedback
      v-if="groupsQuery.isError.value"
      data-test="logs-group-options-failed"
      tone="warning"
    >
      {{ t('monitor.logs.options.groupsFailed') }}
    </InlineFeedback>
    <InlineFeedback
      v-if="accessKeyOptionsQuery.isError.value"
      data-test="logs-access-key-options-failed"
      tone="warning"
    >
      {{ t('monitor.logs.options.accessKeysFailed') }}
    </InlineFeedback>

    <div v-if="refreshFailed" class="logs-refresh-failed" data-test="logs-refresh-failed">
      <InlineFeedback tone="danger">{{ t('monitor.logs.refreshFailed') }}</InlineFeedback>
      <AppButton data-test="logs-refresh-retry" variant="secondary" @click="refreshFirstPage">
        {{ t('common.retry') }}
      </AppButton>
    </div>
    <InlineFeedback v-if="logsAreStale" data-test="logs-stale" tone="warning">
      {{ t('monitor.logs.stale') }}
    </InlineFeedback>
    <div
      v-if="logsQuery.isFetchNextPageError.value"
      class="logs-next-page-failed"
      data-test="logs-next-page-failed"
    >
      <InlineFeedback tone="danger">{{ t('monitor.logs.nextPageFailed') }}</InlineFeedback>
      <AppButton data-test="logs-next-page-retry" variant="secondary" @click="loadMore">
        {{ t('common.retry') }}
      </AppButton>
    </div>

    <QueryFeedback
      v-if="logsQuery.isPending.value"
      state="loading"
      :message="t('monitor.logs.loading')"
    />
    <QueryFeedback
      v-else-if="logsQuery.isError.value && !logsQuery.data.value"
      state="error"
      :message="t('monitor.logs.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="logsQuery.refetch()"
    />
    <DataTable v-else-if="logs.length > 0" :caption="t('monitor.logs.caption')" dense>
      <thead>
        <tr>
          <th scope="col">{{ t('monitor.logs.columns.completedAt') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.requestId') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.route') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.accessKey') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.result') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.timing') }}</th>
          <th scope="col">{{ t('monitor.logs.columns.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="log in logs" :key="log.request_id" :data-test="`log-row-${log.request_id}`">
          <td>
            <time :datetime="log.completed_at">{{ log.completed_at }}</time>
          </td>
          <td>
            <code>{{ log.request_id }}</code>
          </td>
          <td>
            <div class="log-cell-stack">
              <span>{{ t('monitor.logs.drawer.protocol') }}</span>
              <code>{{ t(`common.protocols.${log.protocol}`) }}</code>
              <span>{{ t('monitor.logs.drawer.clientModel') }}</span>
              <code>{{ log.client_model }}</code>
              <span>{{ t('monitor.logs.drawer.upstreamModel') }}</span>
              <code>{{ log.upstream_model }}</code>
            </div>
          </td>
          <td>{{ accessKeyLabel(log) }}</td>
          <td>
            <div class="log-cell-stack">
              <StatusBadge :tone="statusTone(log.status)">
                {{ t(`monitor.logs.status.${log.status}`) }}
              </StatusBadge>
              <span>{{ t('monitor.logs.drawer.statusCode') }}</span>
              <code>{{ log.status_code }}</code>
            </div>
          </td>
          <td>
            <div class="log-cell-stack">
              <code>{{ log.duration_ms }} ms</code>
              <span>{{ t('monitor.logs.attemptCount', { count: log.attempts.length }) }}</span>
            </div>
          </td>
          <td>
            <LogDetailDrawer
              :open="selectedRequestID === log.request_id"
              :log="log"
              @update:open="setDetailOpen(log.request_id, $event)"
            >
              <template #trigger>
                <AppButton
                  :id="`log-details-${log.request_id}`"
                  :data-test="`log-details-${log.request_id}`"
                  variant="ghost"
                >
                  {{ t('monitor.logs.details') }}
                </AppButton>
              </template>
            </LogDetailDrawer>
          </td>
        </tr>
      </tbody>
    </DataTable>
    <EmptyState
      v-else-if="logsQuery.data.value"
      :data-test="hasAppliedFilters ? 'logs-empty-filtered' : 'logs-empty-unfiltered'"
      :title="
        t(hasAppliedFilters ? 'monitor.logs.empty.filteredTitle' : 'monitor.logs.empty.title')
      "
      :description="
        t(
          hasAppliedFilters
            ? 'monitor.logs.empty.filteredDescription'
            : 'monitor.logs.empty.description',
        )
      "
    />
    <AppButton
      v-if="logsQuery.hasNextPage.value"
      data-test="logs-load-more"
      variant="secondary"
      :busy="logsQuery.isFetchingNextPage.value"
      :disabled="refreshPending"
      @click="loadMore"
    >
      {{ t('monitor.logs.loadMore') }}
    </AppButton>
  </div>
</template>

<style scoped>
.logs-tab {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.logs-filter-form {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}

.logs-applied,
.logs-freshness {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-3) var(--space-4);
}

.logs-filter-chip {
  border: 1px solid var(--color-border);
  border-radius: 999px;
  background: var(--color-surface-secondary);
  padding: var(--space-1) var(--space-2);
  font-size: 0.8125rem;
}

.logs-freshness {
  justify-content: space-between;
}

.logs-freshness > span {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.logs-filter-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(150px, 1fr));
  gap: var(--space-3);
}

.logs-filter-form input,
.logs-filter-form select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
}

.logs-filter-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.logs-filter-dirty,
.logs-refresh-failed,
.logs-next-page-failed {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.logs-filter-dirty {
  margin: 0;
  color: var(--color-text);
  font-weight: 650;
}

.logs-filter-dirty svg {
  color: var(--color-warning);
}

.log-cell-stack {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.log-cell-stack > span:not(.status-badge) {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.logs-tab code,
.logs-tab time {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}

.logs-tab > :deep(.app-button) {
  justify-self: center;
}

.logs-tab :deep(.inline-feedback--warning > span:last-child),
.logs-tab :deep(.inline-feedback--danger > span:last-child),
.logs-tab :deep(.query-feedback--error > span) {
  color: var(--color-text);
}

@media (max-width: 900px) {
  .logs-filter-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .logs-filter-grid {
    grid-template-columns: 1fr;
  }
}
</style>
