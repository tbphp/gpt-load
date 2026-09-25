<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Radar, RefreshCw, Search, Trash2 } from '@lucide/vue'
import {
  clearCodexRouting,
  getCodexRoutingEvents,
  getCodexRoutingStatus,
  probeCodexRouting,
  type CodexRoutingCredential,
  type CodexRoutingVerdict,
} from '@modern/api/codex-routing'
import { useApiClient } from '@shared/http/client-context'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useMessageSource } from '@modern/app/messages'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppIconButton,
  AppOverflowText,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const statusQuery = useQuery({
  queryKey: ['modern', 'codex-routing'],
  queryFn: ({ signal }) => getCodexRoutingStatus(client, signal),
})
const eventQuery = useQuery({
  queryKey: ['modern', 'codex-routing-events'],
  queryFn: ({ signal }) => getCodexRoutingEvents(client, signal),
})
const pending = computed(() => statusQuery.isFetching.value || eventQuery.isFetching.value)
const failed = computed(() => statusQuery.isError.value || eventQuery.isError.value)
const status = computed(() => statusQuery.data.value)
const events = computed(() => eventQuery.data.value ?? [])
const probing = ref(false)
const pendingClear = ref(0)
const query = ref('')
const verdictFilter = ref('all')
const nowMs = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  nowMs.value = Date.now()
  clock = setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
})
onUnmounted(() => {
  if (clock) clearInterval(clock)
})
const verdictOptions = computed(() => [
  { value: 'all', label: t('codexRouting.filterAll') },
  ...(['pinned', 'probing', 'empty', 'stale', 'rotated', 'degraded'] as const).map((key) => ({
    value: key,
    label: t('codexRouting.verdicts.' + key),
  })),
])
const metrics = computed(() => {
  const counts = status.value?.counts
  const cooling = (counts?.empty ?? 0) + (counts?.stale ?? 0) + (counts?.degraded ?? 0)
  return [
    { key: 'pinned', value: counts?.pinned ?? 0, tone: 'success' as const, hint: t('codexRouting.countsHint.pinned') },
    { key: 'probing', value: counts?.probing ?? 0, tone: 'info' as const, hint: t('codexRouting.countsHint.probing') },
    { key: 'cooling', value: cooling, tone: 'warning' as const, hint: t('codexRouting.countsHint.cooling') },
    { key: 'events', value: events.value.length, tone: 'neutral' as const, hint: t('codexRouting.countsHint.events') },
  ]
})
const filteredCredentials = computed(() => {
  const items = status.value?.credentials ?? []
  const needle = query.value.trim().toLowerCase()
  return items.filter((item) => {
    if (verdictFilter.value !== 'all' && item.verdict !== verdictFilter.value) return false
    if (!needle) return true
    const hay = [
      String(item.id),
      item.groupName,
      item.region,
      item.lastModel,
      item.servedModel,
      item.proxyRegion,
      item.verdict,
    ]
      .join(' ')
      .toLowerCase()
    return hay.includes(needle)
  })
})

async function refresh(): Promise<void> {
  await Promise.all([statusQuery.refetch(), eventQuery.refetch()])
}
usePageRefresh({ refresh, pending, updatedAt: () => status.value?.observedAt })
useMessageSource(() =>
  failed.value && status.value
    ? {
        tone: 'warning',
        text: t('codexRouting.failed'),
        action: { label: t('ui.retry'), run: refresh },
      }
    : undefined,
)

function verdictTone(verdict: CodexRoutingVerdict) {
  if (verdict === 'pinned') return 'success'
  if (verdict === 'stale' || verdict === 'probing') return 'warning'
  if (verdict === 'rotated' || verdict === 'degraded') return 'danger'
  return 'neutral'
}

function remainingSeconds(expiresAt: number | null, snapshot: number | null): number | null {
  void nowMs.value
  if (expiresAt !== null) return Math.max(0, Math.floor((expiresAt - nowMs.value) / 1000))
  if (snapshot === null) return null
  const observed = status.value?.observedAt
  if (!observed) return snapshot
  return Math.max(0, snapshot - Math.floor((nowMs.value - observed) / 1000))
}

function ttlLabel(seconds: number | null): string {
  if (seconds === null) return '—'
  if (seconds <= 0) return '0s'
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return rest ? `${minutes}m ${rest}s` : `${minutes}m`
}

function liveTtl(item: CodexRoutingCredential): string {
  return ttlLabel(remainingSeconds(item.expiresAt, item.ttlSeconds))
}

function ticketLabel(item: CodexRoutingCredential): string {
  if (!item.ticketLen) return '—'
  const ttl = remainingSeconds(item.ticketExpiresAt, item.ticketTTLSeconds)
  return ttl === null ? `#${item.ticketLen}` : `#${item.ticketLen} · ${ttlLabel(ttl)}`
}

function ttlDatetime(item: CodexRoutingCredential): string | undefined {
  if (item.expiresAt) return new Date(item.expiresAt).toISOString()
  return undefined
}

function eventTime(ms: number): string {
  return new Date(ms).toLocaleTimeString()
}

async function invalidate(): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: ['modern', 'codex-routing'] })
  await queryClient.invalidateQueries({ queryKey: ['modern', 'codex-routing-events'] })
}

async function probeOne(id: number): Promise<void> {
  probing.value = true
  try {
    await probeCodexRouting(client, id)
    await invalidate()
  } finally {
    probing.value = false
  }
}

async function probeDue(): Promise<void> {
  const due = (status.value?.credentials ?? []).filter((item) =>
    ['empty', 'stale', 'rotated', 'degraded'].includes(item.verdict),
  )
  probing.value = true
  try {
    for (const item of due) await probeCodexRouting(client, item.id)
    await invalidate()
  } finally {
    probing.value = false
  }
}

async function clearOne(id: number): Promise<void> {
  if (pendingClear.value !== id) {
    pendingClear.value = id
    return
  }
  pendingClear.value = 0
  await clearCodexRouting(client, id)
  await invalidate()
}
</script>

<template>
  <div class="modern-codex-routing">
    <AppCollectionState v-if="pending && !status" :title="t('codexRouting.loading')" loading />
    <AppCollectionState v-else-if="failed && !status" :title="t('codexRouting.failed')" error>
      <AppButton variant="outline" @click="refresh">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-else-if="status">
      <div class="modern-codex-routing-toolbar">
        <p class="modern-codex-routing-kicker">{{ t('codexRouting.kicker') }}</p>
        <div class="modern-codex-routing-toolbar-actions">
          <AppBadge :tone="status.enabled ? 'success' : 'neutral'">
            {{ status.enabled ? t('codexRouting.enabled') : t('codexRouting.disabled') }}
          </AppBadge>
          <AppButton variant="outline" :icon="RefreshCw" :loading="pending" @click="refresh">
            {{ t('codexRouting.refresh') }}
          </AppButton>
          <AppButton
            variant="primary"
            :icon="Radar"
            :loading="probing"
            :disabled="!status.enabled"
            @click="probeDue"
          >
            {{ t('codexRouting.probeAll') }}
          </AppButton>
        </div>
      </div>

      <section class="modern-codex-routing-card" :aria-label="t('codexRouting.introTitle')">
        <h2>{{ t('codexRouting.introTitle') }}</h2>
        <p>{{ t('codexRouting.intro') }}</p>
        <p v-if="status.probeRelayConfigured">{{ t('codexRouting.relayReady') }}</p>
        <p v-else-if="status.probeProxyConfigured">{{ t('codexRouting.proxyReady') }}</p>
        <p v-else class="is-warn">{{ t('codexRouting.proxyMissing') }}</p>
      </section>

      <section class="modern-codex-routing-metrics" :aria-label="t('codexRouting.counts.pinned')">
        <article v-for="metric in metrics" :key="metric.key" class="modern-codex-routing-metric">
          <h3>{{ t('codexRouting.counts.' + metric.key) }}</h3>
          <strong :data-tone="metric.value ? metric.tone : undefined">{{ metric.value }}</strong>
          <p>{{ metric.hint }}</p>
        </article>
      </section>

      <div class="modern-codex-routing-layout">
        <div class="modern-codex-routing-main">
          <section class="modern-codex-routing-card">
            <header class="modern-codex-routing-card-head">
              <div>
                <h2>{{ t('codexRouting.credentials') }}</h2>
                <p>{{ t('codexRouting.credentialsHint') }}</p>
              </div>
            </header>
            <div class="modern-codex-routing-filters">
              <AppTextField
                v-model="query"
                :icon="Search"
                :label="t('codexRouting.search')"
                label-hidden
                :placeholder="t('codexRouting.search')"
              />
              <AppSelect
                v-model="verdictFilter"
                :label="t('codexRouting.verdict')"
                label-hidden
                :options="verdictOptions"
              />
            </div>
            <AppCollectionState
              v-if="!filteredCredentials.length"
              :title="t('codexRouting.emptyCredentials')"
              :description="t('codexRouting.emptyCredentialsHelp')"
            />
            <div v-else class="modern-codex-routing-table" role="table">
              <div class="modern-codex-routing-row is-head" role="row">
                <span>{{ t('codexRouting.credential') }}</span>
                <span>{{ t('codexRouting.protocol') }}</span>
                <span>{{ t('codexRouting.verdict') }}</span>
                <span>{{ t('codexRouting.target') }}</span>
                <span>{{ t('codexRouting.ticket') }}</span>
                <span>{{ t('codexRouting.ttl') }}</span>
                <span />
              </div>
              <div v-for="item in filteredCredentials" :key="item.id" class="modern-codex-routing-row" role="row">
                <div>
                  <strong>#{{ item.id }} · {{ item.lastModel || '—' }}</strong>
                  <AppOverflowText :text="item.groupName || '—'" />
                  <span class="is-meta">{{ item.region || '—' }}{{ item.proxyRegion ? ` · ${item.proxyRegion}` : '' }}</span>
                </div>
                <span>{{
                  status.mint ? t('codexRouting.protocols.mint') : t('codexRouting.protocols.transparent')
                }}</span>
                <AppBadge :tone="verdictTone(item.verdict)">
                  {{ t('codexRouting.verdicts.' + item.verdict) }}
                </AppBadge>
                <span>{{ item.region || '—' }} / {{ status.targetGateway || '—' }}</span>
                <span class="modern-codex-routing-ttl">{{ ticketLabel(item) }}</span>
                <time class="modern-codex-routing-ttl" :datetime="ttlDatetime(item)">{{ liveTtl(item) }}</time>
                <div class="modern-codex-routing-actions">
                  <AppIconButton
                    :icon="Radar"
                    :label="t('codexRouting.probe')"
                    :disabled="probing || !status.enabled"
                    @click="probeOne(item.id)"
                  />
                  <AppIconButton
                    :icon="Trash2"
                    :label="
                      pendingClear === item.id ? t('codexRouting.confirmClear') : t('codexRouting.clear')
                    "
                    variant="ghost"
                    @click="clearOne(item.id)"
                  />
                </div>
              </div>
            </div>
            <p class="is-meta">
              {{
                t('codexRouting.showing', {
                  shown: filteredCredentials.length,
                  total: status.credentials.length,
                })
              }}
            </p>
          </section>

          <section class="modern-codex-routing-card">
            <header class="modern-codex-routing-card-head">
              <div>
                <h2>{{ t('codexRouting.events') }}</h2>
                <p>{{ t('codexRouting.eventsHint') }}</p>
              </div>
            </header>
            <AppCollectionState v-if="!events.length" :title="t('codexRouting.emptyEvents')" />
            <ol v-else class="modern-codex-routing-events">
              <li v-for="(item, index) in events.slice().reverse()" :key="index">
                <div>
                  <strong>
                    {{ t('codexRouting.kinds.' + item.kind) }} ·
                    {{ t('codexRouting.actions.' + (item.action || 'empty')) }}
                  </strong>
                  <p>
                    #{{ item.credentialID }} · {{ item.model || '—' }} · {{ item.region || '—' }}
                    <template v-if="item.proxyRegion"> · {{ item.proxyRegion }}</template>
                    <template v-if="item.turnStateLen"> · 票长 {{ item.turnStateLen }}</template>
                  </p>
                </div>
                <time>{{ eventTime(item.time) }}</time>
              </li>
            </ol>
          </section>
        </div>

        <aside class="modern-codex-routing-card modern-codex-routing-settings">
          <header class="modern-codex-routing-card-head">
            <div>
              <h2>{{ t('codexRouting.settings') }}</h2>
              <p>{{ t('codexRouting.settingsHint') }}</p>
            </div>
            <AppBadge tone="neutral">{{ t('codexRouting.envLocked') }}</AppBadge>
          </header>
          <dl>
            <div>
              <dt>{{ t('codexRouting.enabled') }}</dt>
              <dd>{{ status.enabled ? 'on' : 'off' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.transparent') }}</dt>
              <dd>{{ status.transparent ? 'on' : 'off' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.mint') }}</dt>
              <dd>{{ status.mint ? 'on' : 'off' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.relay') }}</dt>
              <dd>{{ status.probeRelayConfigured ? 'on' : 'off' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.target') }}</dt>
              <dd>{{ status.targetGateway || '—' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.probeRegions') }}</dt>
              <dd>{{ status.probeRegions.join(' · ') || '—' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.maxRotates') }}</dt>
              <dd>{{ status.maxRotates || '—' }}</dd>
            </div>
            <div>
              <dt>{{ t('codexRouting.ticketTTL') }}</dt>
              <dd>{{ status.ticketTTLSeconds || '—' }}</dd>
            </div>
          </dl>
          <p class="is-meta">{{ t('codexRouting.settingsNote') }}</p>
        </aside>
      </div>
    </template>
  </div>
</template>

<style scoped>
.modern-codex-routing {
  display: grid;
  gap: var(--modern-space-5);
}
.modern-codex-routing-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-codex-routing-kicker {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  letter-spacing: var(--modern-tracking-label);
  text-transform: uppercase;
}
.modern-codex-routing-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-codex-routing-card {
  display: grid;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-codex-routing-card h2,
.modern-codex-routing-metric h3 {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-codex-routing-card p,
.modern-codex-routing-metric p {
  margin: 0;
  color: var(--modern-muted);
}
.modern-codex-routing-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-codex-routing-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-codex-routing-metric {
  display: grid;
  gap: var(--modern-space-2);
  padding: var(--modern-space-4);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-codex-routing-metric strong {
  font-size: var(--modern-font-size-title);
  line-height: var(--modern-leading-title);
}
.modern-codex-routing-metric strong[data-tone='success'] {
  color: var(--modern-success);
}
.modern-codex-routing-metric strong[data-tone='warning'] {
  color: var(--modern-warning);
}
.modern-codex-routing-metric strong[data-tone='danger'] {
  color: var(--modern-danger);
}
.modern-codex-routing-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 18rem;
  gap: var(--modern-space-5);
  align-items: start;
}
.modern-codex-routing-main {
  display: grid;
  gap: var(--modern-space-5);
  min-width: 0;
}
.modern-codex-routing-filters {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 10rem;
  gap: var(--modern-space-3);
}
.modern-codex-routing-table {
  display: grid;
}
.modern-codex-routing-row {
  display: grid;
  grid-template-columns: minmax(9rem, 1.4fr) 4.5rem 6rem minmax(7rem, 1fr) 8rem 7rem auto;
  gap: var(--modern-space-3);
  align-items: center;
  padding-block: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-codex-routing-row.is-head {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-codex-routing-ttl {
  font-variant-numeric: tabular-nums;
  font-feature-settings: 'tnum';
  white-space: nowrap;
}
.modern-codex-routing-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-1);
}
.modern-codex-routing-events {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-codex-routing-events li {
  display: flex;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-codex-routing-events p,
.modern-codex-routing-events time,
.is-meta {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-codex-routing-settings dl {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
}
.modern-codex-routing-settings div {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-codex-routing-settings dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-codex-routing-settings dd {
  margin: 0;
}
.is-warn {
  color: var(--modern-warning);
}
@media (max-width: 1150px) {
  .modern-codex-routing-layout,
  .modern-codex-routing-metrics {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 760px) {
  .modern-codex-routing-filters,
  .modern-codex-routing-row {
    grid-template-columns: 1fr;
  }
}
</style>
