<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  FailureCategory,
  RequestLogAction,
  RequestLogItemDto,
  RequestLogStatus,
} from '@/api/control/request-logs'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{
  open: boolean
  log: RequestLogItemDto | null
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
const { t } = useI18n()

const failureCategories: readonly FailureCategory[] = [
  'ok',
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
]
const actions: readonly RequestLogAction[] = [
  'terminate',
  'retry',
  'cooldown_key',
  'fail_key',
  'skip_group',
]
const orderedAttempts = computed(() =>
  [...(props.log?.attempts ?? [])].sort((left, right) => left.sequence - right.sequence),
)
const inspectorTarget = computed(() => {
  if (!props.log) return { name: 'monitor', query: { tab: 'inspector' } }
  return {
    name: 'monitor',
    query: {
      tab: 'inspector',
      protocol: props.log.protocol,
      external_model: props.log.client_model,
      access_key_id: props.log.access_key.id,
    },
  }
})

function statusTone(status: RequestLogStatus): 'success' | 'danger' | 'warning' | 'neutral' {
  if (status === 'success') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'incomplete') return 'warning'
  return 'neutral'
}

function failureLabel(value: string): string {
  return failureCategories.includes(value as FailureCategory)
    ? t(`monitor.logs.failureCategory.${value}`)
    : t('monitor.logs.failureCategory.unknown')
}

function actionLabel(value: string): string {
  return actions.includes(value as RequestLogAction)
    ? t(`monitor.logs.action.${value}`)
    : t('monitor.logs.action.unknown')
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
</script>

<template>
  <AppDrawer
    :open="open"
    :title="t('monitor.logs.drawer.title')"
    :description="t('monitor.logs.drawer.description')"
    :close-label="t('monitor.logs.drawer.close')"
    @update:open="$emit('update:open', $event)"
  >
    <template #trigger><slot name="trigger" /></template>

    <div v-if="log" class="log-detail">
      <section class="log-detail__section" aria-labelledby="log-detail-summary-heading">
        <h2 id="log-detail-summary-heading">{{ t('monitor.logs.drawer.summary') }}</h2>
        <dl class="log-detail__facts">
          <div class="log-detail__wide">
            <dt>{{ t('monitor.logs.drawer.requestId') }}</dt>
            <dd class="log-detail__copy">
              <code>{{ log.request_id }}</code>
              <CopyButton
                :value="log.request_id"
                :label="t('monitor.logs.drawer.copyRequestId')"
                :success-label="t('common.copied')"
                :failure-label="t('common.copyFailed')"
              />
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.completedAt') }}</dt>
            <dd>
              <time :datetime="log.completed_at">{{ log.completed_at }}</time>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.status') }}</dt>
            <dd>
              <StatusBadge :tone="statusTone(log.status)">
                {{ t(`monitor.logs.status.${log.status}`) }}
              </StatusBadge>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.protocol') }}</dt>
            <dd>{{ t(`group.protocols.${log.protocol}`) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.accessKey') }}</dt>
            <dd>{{ accessKeyLabel(log) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.clientModel') }}</dt>
            <dd>
              <code>{{ log.client_model }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
            <dd>
              <code>{{ log.upstream_model }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.statusCode') }}</dt>
            <dd>{{ log.status_code }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
            <dd>{{ log.duration_ms }} ms</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.affinity') }}</dt>
            <dd>{{ log.affinity_hit ? t('monitor.logs.yes') : t('monitor.logs.no') }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
            <dd>
              <code>{{ log.error_code || t('monitor.logs.none') }}</code>
            </dd>
          </div>
        </dl>
        <div class="log-detail__summary">
          <h3>{{ t('monitor.logs.drawer.errorSummary') }}</h3>
          <p data-test="log-error-summary">{{ log.error_summary || t('monitor.logs.none') }}</p>
        </div>
        <RouterLink
          class="log-detail__inspector"
          data-test="log-inspector-link"
          :to="inspectorTarget"
        >
          {{ t('monitor.logs.drawer.openInspector') }}
        </RouterLink>
      </section>

      <section class="log-detail__section" aria-labelledby="log-detail-attempts-heading">
        <h2 id="log-detail-attempts-heading">{{ t('monitor.logs.drawer.attempts') }}</h2>
        <p v-if="orderedAttempts.length === 0" class="log-detail__empty">
          {{ t('monitor.logs.drawer.noAttempts') }}
        </p>
        <article
          v-for="attempt in orderedAttempts"
          v-else
          :key="attempt.sequence"
          class="log-attempt"
          :data-test="`log-attempt-${attempt.sequence}`"
        >
          <header>
            <h3>{{ t('monitor.logs.drawer.attempt', { sequence: attempt.sequence }) }}</h3>
            <StatusBadge :tone="attempt.committed ? 'success' : 'neutral'">
              {{
                attempt.committed
                  ? t('monitor.logs.drawer.committed')
                  : t('monitor.logs.drawer.notCommitted')
              }}
            </StatusBadge>
          </header>
          <dl class="log-detail__facts">
            <div>
              <dt>{{ t('monitor.logs.drawer.group') }}</dt>
              <dd>{{ attempt.group_name }} · #{{ attempt.group_id }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.upstreamKey') }}</dt>
              <dd>
                <code>{{ attempt.key_mask }}</code> · #{{ attempt.key_id }}
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
              <dd>
                <code>{{ attempt.upstream_model }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.statusCode') }}</dt>
              <dd>{{ attempt.status_code }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
              <dd>{{ attempt.duration_ms }} ms</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.failureCategory') }}</dt>
              <dd>{{ failureLabel(attempt.failure_category) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.action') }}</dt>
              <dd>{{ actionLabel(attempt.action) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.willRetry') }}</dt>
              <dd>{{ attempt.will_retry ? t('monitor.logs.yes') : t('monitor.logs.no') }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
              <dd>
                <code>{{ attempt.error_code || t('monitor.logs.none') }}</code>
              </dd>
            </div>
          </dl>
          <div class="log-detail__summary">
            <h4>{{ t('monitor.logs.drawer.errorSummary') }}</h4>
            <p>{{ attempt.error_summary || t('monitor.logs.none') }}</p>
          </div>
        </article>
      </section>
    </div>
  </AppDrawer>
</template>

<style scoped>
.log-detail {
  display: grid;
  gap: var(--space-6);
}

.log-detail__section {
  display: grid;
  gap: var(--space-3);
}

.log-detail__section h2,
.log-detail__section h3,
.log-detail__section h4,
.log-detail__section p {
  margin: 0;
}

.log-detail__section h2 {
  font-size: 1rem;
}

.log-detail__facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.log-detail__facts > div {
  min-width: 0;
}

.log-detail__facts dt {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.log-detail__facts dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
}

.log-detail__wide {
  grid-column: 1 / -1;
}

.log-detail__copy {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.log-detail code,
.log-detail time {
  font-family: var(--font-mono);
}

.log-detail__summary,
.log-attempt {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  padding: var(--space-3);
}

.log-detail__summary {
  display: grid;
  gap: var(--space-2);
}

.log-detail__summary p {
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.log-detail__inspector {
  color: var(--color-primary);
  font-weight: 650;
}

.log-attempt {
  display: grid;
  gap: var(--space-3);
}

.log-attempt > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.log-detail__empty {
  color: var(--color-text-muted);
}
</style>
