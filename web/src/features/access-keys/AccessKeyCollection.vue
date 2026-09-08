<script setup lang="ts">
import { ArrowRight, Ellipsis } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import type { AccessKeyCollectionItemDto, GroupOptionDto } from '@/api/control/types'
import { revealAccessKey } from '@/app/resources/access-keys'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import { monitorLocation } from '@/app/route-locations'
import { formatEstimatedCost, formatInteger, formatTokens, formatUSD } from '@/lib/format'
import { quotaProgressTone } from '@/lib/quota-progress'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import AccessKeyCostLimitResetDialog from './AccessKeyCostLimitResetDialog.vue'
import { presentAccessKeyCollection } from './access-key-presenter'

const props = defineProps<{
  usageWindow: { range: '7d'; from_ms: number; to_ms: number; observed_at_ms: number }
  accessKeys: readonly AccessKeyCollectionItemDto[]
  groups: readonly GroupOptionDto[]
  total: number
  filteredTotal: number
  page: number
  pageSize: number
  optimisticEnabled: ReadonlyMap<number, boolean>
  busyIds: ReadonlySet<number>
  lockedIds: ReadonlySet<number>
}>()
const emit = defineEmits<{
  open: [accessKey: AccessKeyCollectionItemDto, trigger: HTMLElement]
  clone: [accessKey: AccessKeyCollectionItemDto]
  toggle: [accessKey: AccessKeyCollectionItemDto, enabled: boolean]
  deleted: [name: string]
  reset: [name: string]
}>()
const router = useRouter()
function viewUsage(id: number): void {
  void router.push(
    monitorLocation({
      tab: 'usage',
      access_key_id: String(id),
      from_ms: String(props.usageWindow.from_ms),
      to_ms: String(props.usageWindow.to_ms),
    }),
  )
}
function viewLogs(id: number): void {
  void router.push(
    monitorLocation({
      tab: 'logs',
      access_key_id: String(id),
      from_ms: String(props.usageWindow.from_ms),
      to_ms: String(props.usageWindow.to_ms),
    }),
  )
}
const client = useApiClient()
const { locale, t } = useI18n()
const copyControllers = useAbortControllerPool()
const sources = computed(
  () => new Map(props.accessKeys.map((accessKey) => [accessKey.id, accessKey])),
)

const presentations = computed(() =>
  presentAccessKeyCollection(props.accessKeys, props.groups, {
    locale: locale.value,
    labels: {
      groups: t('accessKeys.filterGroups'),
      protocols: t('accessKeys.filterProtocols'),
      models: t('accessKeys.filterModels'),
      allGroups: t('accessKeys.allGroups'),
      allProtocols: t('accessKeys.allProtocols'),
      allModels: t('accessKeys.allModels'),
      unlimited: t('accessKeys.unlimited'),
      costRules: (count) => t('accessKeys.costLimits.ruleCount', { count }),
      priceMultiplier: (value) => t('common.priceMultiplier.value', { value }),
    },
    protocolLabel: (protocol) => protocol,
  }),
)
function source(id: number): AccessKeyCollectionItemDto {
  const accessKey = sources.value.get(id)
  if (!accessKey) throw new Error(`ACCESS_KEY_SOURCE_MISSING:${id}`)
  return accessKey
}

const quotas = computed(
  () =>
    new Map(
      props.accessKeys.map((key) => {
        const rules = key.cost_limit_status?.rules ?? []
        const percent = rules.length
          ? Math.min(
              ...rules.map((rule) =>
                rule.status === 'inactive'
                  ? 100
                  : Math.round(
                      Math.max(
                        0,
                        Math.min(100, (Number(rule.remaining_usd) / Number(rule.limit_usd)) * 100),
                      ),
                    ),
              ),
            )
          : undefined
        const tooltip = rules.length
          ? rules
              .map((rule) => {
                const label =
                  rule.kind === 'total'
                    ? t('accessKeys.distribution.totalQuota')
                    : t('accessKeys.distribution.periodic', { hours: rule.period_seconds / 3600 })
                return `${label} · ${t('accessKeys.distribution.remaining', { amount: formatUSD(rule.remaining_usd, locale.value) })}`
              })
              .join('\n')
          : t('accessKeys.unlimited')
        return [
          key.id,
          {
            percent,
            tone: quotaProgressTone(
              percent ?? 100,
              rules.some((rule) => rule.status === 'exhausted'),
            ),
            tooltip,
          },
        ] as const
      }),
    ),
)
const menuKeyID = ref<number>()
const dialogKey = ref<AccessKeyCollectionItemDto | null>(null)
const deleteDialog = ref<InstanceType<typeof AccessKeyDeleteDialog>>()
const resetDialog = ref<InstanceType<typeof AccessKeyCostLimitResetDialog>>()

type MenuAction = 'usage' | 'logs' | 'clone' | 'reset' | 'delete'
async function runMenuAction(id: number, action: MenuAction): Promise<void> {
  menuKeyID.value = undefined
  if (action === 'usage') return viewUsage(id)
  if (action === 'logs') return viewLogs(id)
  if (action === 'clone') return emit('clone', source(id))
  dialogKey.value = source(id)
  await nextTick()
  if (action === 'delete') deleteDialog.value?.open()
  else resetDialog.value?.open()
}

async function resolveCopyValue(id: number): Promise<string> {
  const controller = copyControllers.create()
  try {
    const result = await revealAccessKey(client, id, controller.signal)
    return result.key
  } finally {
    copyControllers.release(controller)
  }
}

function conceal(): void {
  menuKeyID.value = undefined
  copyControllers.abortAll()
}

defineExpose({ conceal })

watch(
  () => [props.page, props.accessKeys.map(({ id }) => id).join(',')],
  () => conceal(),
)
</script>

<template>
  <LedgerRecordList
    :label="t('accessKeys.collection.tableLabel')"
    :row-count="filteredTotal + 1"
    grid-class="access-keys-record-grid"
  >
    <template #header>
      <span role="columnheader">{{ t('accessKeys.columns.name') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.key') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.status') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.scope') }}</span>
      <span role="columnheader">{{ t('accessKeys.distribution.usageQuota') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.lastRequest') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.actions') }}</span>
    </template>

    <article
      v-for="(record, index) in presentations"
      :key="record.id"
      class="ledger-record-list__record access-key-record"
      role="row"
      :aria-rowindex="(page - 1) * pageSize + index + 2"
    >
      <div class="ledger-record-list__cell access-key-name" role="cell">
        <span>{{ record.name }}</span>
      </div>

      <div class="ledger-record-list__cell access-key-secret-cell" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.key') }}</span>
        <CopyChip
          layout="trailing"
          :value="record.maskedKey"
          :label="t('accessKeys.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
          :resolve-value="() => resolveCopyValue(record.id)"
        />
      </div>

      <div class="ledger-record-list__cell access-key-status" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.status') }}</span>
        <AppSwitch
          :model-value="optimisticEnabled.get(record.id) ?? record.status === 'active'"
          :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
          :label="t('accessKeys.actions.toggle', { name: record.name })"
          @update:model-value="emit('toggle', source(record.id), $event)"
        />
        <StatusBadge :tone="record.status === 'active' ? 'success' : 'neutral'">
          {{ t(`accessKeys.status.${record.status}`) }}
        </StatusBadge>
        <StatusBadge v-if="record.expired" tone="danger" size="compact">
          {{ t('accessKeys.status.expired') }}
        </StatusBadge>
        <StatusBadge v-if="record.ipRestricted" tone="neutral" size="compact">
          {{ t('accessKeys.status.ipRestricted') }}
        </StatusBadge>
        <StatusBadge v-if="record.quotaExhausted" tone="danger" size="compact">
          {{ t('accessKeys.costLimits.exhausted') }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell access-key-scope" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.scope') }}</span>
        <dl>
          <div v-for="scope in record.scopeRows" :key="scope.label">
            <dt>{{ scope.label }}</dt>
            <OverflowTooltip as="dd" :content="scope.value">{{ scope.value }}</OverflowTooltip>
          </div>
        </dl>
      </div>

      <div class="ledger-record-list__cell access-key-rpm" role="cell">
        <span class="mobile-label">{{ t('accessKeys.distribution.usageQuota') }}</span>
        <template v-if="source(record.id).usage">
          <strong>{{
            formatEstimatedCost(source(record.id).usage!.estimated_cost_nano_usd, locale)
          }}</strong>
          <span>{{
            t('accessKeys.distribution.usageValue', {
              requests: formatInteger(source(record.id).usage!.request_count, locale),
              tokens: formatTokens(source(record.id).usage!.total_tokens, locale),
            })
          }}</span>
        </template>
        <AppTooltip
          v-if="record.costLimitRuleCount > 0"
          :content="quotas.get(record.id)!.tooltip"
          align="start"
        >
          <span class="access-key-quota" tabindex="0">
            <span>{{ t('accessKeys.distribution.remainingLabel') }}</span>
            <QuotaProgressBar
              :value="quotas.get(record.id)!.percent"
              :tone="quotas.get(record.id)!.tone"
              :label="t('accessKeys.distribution.remainingLabel')"
              :value-text="quotas.get(record.id)!.tooltip"
              compact
            />
          </span>
        </AppTooltip>
      </div>

      <div class="ledger-record-list__cell access-key-last-request" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.lastRequest') }}</span>
        <AppRelativeTime
          :instant="record.lastRequestAt"
          :locale="locale"
          :empty-label="t('accessKeys.collection.neverRequested')"
        />
        <span class="access-key-expiry" :class="{ 'access-key-expiry--expired': record.expired }"
          >{{ t('accessKeys.distribution.expires') }}
          <AppDateTime
            v-if="source(record.id).expires_at_ms !== null"
            :instant="source(record.id).expires_at_ms!"
            :locale="locale"
          /><span v-else>{{ t('accessKeys.distribution.neverExpires') }}</span></span
        >
      </div>

      <div class="ledger-record-list__cell record-actions" role="cell">
        <AppPopover
          :open="menuKeyID === record.id"
          align="end"
          content-class="app-popover__content--access-key-menu"
          @update:open="menuKeyID = $event ? record.id : undefined"
        >
          <template #trigger>
            <IconButton
              variant="ghost"
              size="compact"
              :label="t('accessKeys.actions.more')"
              :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
              ><Ellipsis :size="16" aria-hidden="true"
            /></IconButton>
          </template>
          <div class="access-key-menu">
            <button type="button" @click="runMenuAction(record.id, 'usage')">
              {{ t('accessKeys.distribution.viewUsage') }}
            </button>
            <button type="button" @click="runMenuAction(record.id, 'logs')">
              {{ t('accessKeys.distribution.viewLogs') }}
            </button>
            <button type="button" @click="runMenuAction(record.id, 'clone')">
              {{ t('accessKeys.distribution.clone') }}
            </button>
            <button
              v-if="record.costLimitRuleCount > 0"
              type="button"
              @click="runMenuAction(record.id, 'reset')"
            >
              {{ t('accessKeys.reset.open') }}
            </button>
            <button
              type="button"
              class="access-key-menu__danger"
              @click="runMenuAction(record.id, 'delete')"
            >
              {{ t('accessKeys.delete.open') }}
            </button>
          </div>
        </AppPopover>
        <IconButton
          variant="ghost"
          size="compact"
          :label="t('accessKeys.collection.openDetailsFor', { name: record.name })"
          :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
          @click="emit('open', source(record.id), $event.currentTarget as HTMLElement)"
        >
          <ArrowRight :size="15" aria-hidden="true" />
        </IconButton>
      </div>
    </article>
  </LedgerRecordList>
  <template v-if="dialogKey">
    <AccessKeyDeleteDialog
      ref="deleteDialog"
      hide-trigger
      :access-key="dialogKey"
      :total="total"
      @deleted="emit('deleted', $event)"
    />
    <AccessKeyCostLimitResetDialog
      ref="resetDialog"
      :access-key="dialogKey"
      @reset="emit('reset', $event)"
    />
  </template>
</template>

<style scoped>
.access-key-expiry {
  display: block;
  margin-top: 4px;
  font-size: var(--text-label-xs);
}
.access-key-expiry--expired {
  color: var(--color-danger);
}
.access-key-quota {
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  align-items: center;
  gap: 7px;
  min-height: 24px;
  font-family: var(--font-sans);
  font-size: var(--text-label-xs);
}
.access-key-quota :deep(.quota-progress) {
  width: 76px;
}
.access-key-quota:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.access-key-menu {
  display: grid;
  gap: 2px;
}
.access-key-menu button {
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 7px 10px;
  font: inherit;
  font-size: var(--text-sm);
  text-align: left;
  cursor: pointer;
}
.access-key-menu button:hover,
.access-key-menu button:focus-visible {
  background: var(--color-surface-sunken);
}
.access-key-menu .access-key-menu__danger {
  color: var(--color-danger);
}

.access-keys-record-grid {
  --ledger-record-list-grid: minmax(100px, 0.8fr) minmax(145px, 1fr) 136px minmax(148px, 1fr)
    minmax(145px, 1fr) minmax(120px, 0.9fr) 64px;
  --ledger-record-list-column-gap: 14px;
}

.access-key-name {
  min-width: 0;
  color: var(--color-text);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.access-key-secret-cell,
.access-key-status,
.access-key-scope,
.access-key-rpm,
.access-key-last-request {
  min-width: 0;
}

.access-key-rpm {
  display: grid;
  gap: 2px;
}

.access-key-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
}

.access-key-scope dl {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  align-items: baseline;
  gap: 3px 6px;
  margin: 0;
}

.access-key-scope dl > div {
  display: contents;
}

.access-key-scope dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-align: left;
}

.access-key-scope dd {
  min-width: 0;
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.access-key-rpm,
.access-key-last-request {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.record-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

@media (max-width: 1120px) {
  .access-keys-record-grid {
    --ledger-record-list-grid: minmax(95px, 1fr) minmax(125px, 1fr) 136px minmax(130px, 1fr)
      minmax(130px, 1fr) minmax(115px, 1fr) 64px;
    --ledger-record-list-column-gap: 10px;
  }

  .access-key-last-request :deep(.app-relative-time) {
    line-height: var(--line-compact);
    overflow-wrap: anywhere;
    white-space: normal;
  }
}

@media (max-width: 1023px) and (min-width: 861px) {
  .access-keys-record-grid {
    --ledger-record-list-grid: minmax(90px, 0.8fr) minmax(125px, 1fr) 136px minmax(130px, 1fr) 64px;
  }

  .access-keys-record-grid :deep(.ledger-record-list__header > :nth-child(4)),
  .access-keys-record-grid :deep(.ledger-record-list__header > :nth-child(6)),
  .access-key-scope,
  .access-key-last-request {
    display: none;
  }
}

@media (max-width: 860px) {
  .access-key-name {
    grid-column: 1 / -1;
  }

  .access-key-secret-cell {
    grid-column: 1 / -1;
    border-top: 1px solid var(--color-border-subtle);
    border-bottom: 1px solid var(--color-border-subtle);
    padding: 11px 0;
  }

  .access-key-scope {
    grid-column: 1 / -1;
  }

  .access-key-rpm,
  .access-key-last-request {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .access-key-rpm {
    border-right: 1px solid var(--color-border-subtle);
    padding-right: 12px;
  }

  .record-actions {
    grid-column: 1 / -1;
    flex-wrap: wrap;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .record-actions :deep(.app-button),
  .record-actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }

  .mobile-label {
    display: inline;
  }

  .access-key-status .mobile-label {
    flex-basis: 100%;
  }
}

@media (max-width: 560px) {
  .access-keys-record-grid {
    --ledger-record-list-card-grid: 76px minmax(0, 1fr);
  }
}
</style>

<style>
.app-popover__content--access-key-menu {
  width: 164px;
  padding: 5px;
}
</style>
