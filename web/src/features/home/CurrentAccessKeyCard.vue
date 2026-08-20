<script setup lang="ts">
import { Gauge, KeyRound, LockKeyhole } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyCollectionItemDto } from '@/api/control/types'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatInteger } from '@/lib/format'

const props = defineProps<{ accessKey: AccessKeyCollectionItemDto }>()
const { locale, t } = useI18n()

const rpm = computed(() =>
  props.accessKey.rpm_limit === 0
    ? t('home.ledger.currentAccessKey.unlimited')
    : t('home.ledger.currentAccessKey.rpmValue', {
        count: formatInteger(props.accessKey.rpm_limit, locale.value),
      }),
)
const protocols = computed(() =>
  props.accessKey.filters.protocols.length === 0
    ? t('home.ledger.currentAccessKey.allProtocols')
    : props.accessKey.filters.protocols.join(', '),
)
const groups = computed(() =>
  props.accessKey.filters.groups.length === 0
    ? t('home.ledger.currentAccessKey.allGroups')
    : props.accessKey.filters.groups.map((id) => `#${id}`).join(', '),
)
const models = computed(() =>
  props.accessKey.filters.models.length === 0
    ? t('home.ledger.currentAccessKey.allModels')
    : props.accessKey.filters.models.join(', '),
)
const costLimits = computed(() => props.accessKey.cost_limit_status)

function periodLabel(seconds: number): string {
  if (seconds % 86_400 === 0) {
    return t('home.ledger.currentAccessKey.costLimits.periodDays', { count: seconds / 86_400 })
  }
  if (seconds % 3_600 === 0) {
    return t('home.ledger.currentAccessKey.costLimits.periodHours', { count: seconds / 3_600 })
  }
  if (seconds % 60 === 0) {
    return t('home.ledger.currentAccessKey.costLimits.periodMinutes', { count: seconds / 60 })
  }
  return t('home.ledger.currentAccessKey.costLimits.periodSeconds', { count: seconds })
}

function ruleLabel(kind: 'total' | 'periodic', periodSeconds: number): string {
  return kind === 'total'
    ? t('home.ledger.currentAccessKey.costLimits.total')
    : t('home.ledger.currentAccessKey.costLimits.periodic', {
        period: periodLabel(periodSeconds),
      })
}

function usagePercent(used: string, limit: string): number {
  const parsedLimit = Number(limit)
  if (!Number.isFinite(parsedLimit) || parsedLimit <= 0) return 0
  return Math.min(100, Math.max(0, (Number(used) / parsedLimit) * 100))
}
</script>

<template>
  <section class="current-access-key" aria-labelledby="current-access-key-title">
    <header class="current-access-key__header">
      <div class="current-access-key__title">
        <KeyRound :size="16" aria-hidden="true" />
        <div>
          <p>{{ t('home.ledger.currentAccessKey.eyebrow') }}</p>
          <h2 id="current-access-key-title">{{ accessKey.name }}</h2>
        </div>
      </div>
      <div class="current-access-key__identity">
        <StatusBadge tone="success" size="compact">
          {{ t('home.ledger.currentAccessKey.active') }}
        </StatusBadge>
        <code>{{ accessKey.masked_key }}</code>
      </div>
    </header>

    <div class="current-access-key__boundary">
      <LockKeyhole :size="14" aria-hidden="true" />
      <span>{{ t('home.ledger.currentAccessKey.readOnly') }}</span>
    </div>

    <dl class="current-access-key__facts">
      <div>
        <dt>{{ t('home.ledger.currentAccessKey.rpm') }}</dt>
        <dd>{{ rpm }}</dd>
      </div>
      <div>
        <dt>{{ t('home.ledger.currentAccessKey.protocols') }}</dt>
        <OverflowTooltip as="dd" :content="protocols">{{ protocols }}</OverflowTooltip>
      </div>
      <div>
        <dt>{{ t('home.ledger.currentAccessKey.groups') }}</dt>
        <OverflowTooltip as="dd" :content="groups">{{ groups }}</OverflowTooltip>
      </div>
      <div>
        <dt>{{ t('home.ledger.currentAccessKey.models') }}</dt>
        <OverflowTooltip as="dd" :content="models">{{ models }}</OverflowTooltip>
      </div>
      <div>
        <dt>{{ t('home.ledger.currentAccessKey.lastRequest') }}</dt>
        <dd>
          <AppDateTime
            v-if="accessKey.last_request_at_ms !== null"
            :instant="accessKey.last_request_at_ms"
            :locale="locale"
          />
          <span v-else>{{ t('home.ledger.currentAccessKey.neverRequested') }}</span>
        </dd>
      </div>
    </dl>

    <section class="current-access-key__limits" aria-labelledby="cost-limits-title">
      <header>
        <div>
          <Gauge :size="15" aria-hidden="true" />
          <h3 id="cost-limits-title">{{ t('home.ledger.currentAccessKey.costLimits.title') }}</h3>
        </div>
        <StatusBadge
          :tone="costLimits === null ? 'neutral' : costLimits.allowed ? 'success' : 'danger'"
          size="compact"
        >
          {{
            t(
              costLimits === null
                ? 'home.ledger.currentAccessKey.costLimits.notConfigured'
                : costLimits.allowed
                  ? 'home.ledger.currentAccessKey.costLimits.available'
                  : 'home.ledger.currentAccessKey.costLimits.blocked',
            )
          }}
        </StatusBadge>
      </header>

      <p v-if="costLimits === null" class="current-access-key__limit-note">
        {{ t('home.ledger.currentAccessKey.costLimits.notConfiguredDescription') }}
      </p>
      <div v-else class="current-access-key__limit-list">
        <article v-for="rule in costLimits.rules" :key="rule.id">
          <div class="current-access-key__limit-heading">
            <strong>{{ ruleLabel(rule.kind, rule.period_seconds) }}</strong>
            <StatusBadge
              :tone="
                rule.status === 'exhausted'
                  ? 'danger'
                  : rule.status === 'inactive'
                    ? 'neutral'
                    : 'success'
              "
              size="compact"
            >
              {{ t(`accessKeys.costLimits.status.${rule.status}`) }}
            </StatusBadge>
          </div>
          <p>
            {{
              t('home.ledger.currentAccessKey.costLimits.usage', {
                used: rule.used_usd,
                limit: rule.limit_usd,
                remaining: rule.remaining_usd,
              })
            }}
          </p>
          <progress
            :value="usagePercent(rule.used_usd, rule.limit_usd)"
            max="100"
            :aria-label="ruleLabel(rule.kind, rule.period_seconds)"
          />
          <div class="current-access-key__limit-recovery">
            <template v-if="rule.status === 'inactive'">
              {{ t('home.ledger.currentAccessKey.costLimits.startsOnNextRequest') }}
            </template>
            <template v-else-if="rule.window_ends_at_ms !== null">
              {{
                t(
                  rule.status === 'exhausted'
                    ? 'home.ledger.currentAccessKey.costLimits.availableAgain'
                    : 'home.ledger.currentAccessKey.costLimits.resetsAt',
                )
              }}
              <AppRelativeTime
                :instant="rule.window_ends_at_ms"
                :locale="locale"
                :empty-label="t('home.ledger.currentAccessKey.costLimits.notAutomatic')"
                hint
              />
            </template>
            <template v-else-if="rule.status === 'exhausted'">
              {{ t('home.ledger.currentAccessKey.costLimits.notAutomatic') }}
            </template>
          </div>
        </article>
      </div>
      <p v-if="costLimits !== null" class="current-access-key__limit-note">
        {{ t('home.ledger.currentAccessKey.costLimits.estimateNote') }}
      </p>
    </section>
  </section>
</template>

<style scoped>
.current-access-key {
  display: grid;
  gap: 14px;
  /* 同上：只留上边线，避免和下一个板块的上边线撞成两条。 */
  border-top: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-action-soft) 28%, transparent);
  padding: 18px 0;
}

.current-access-key__header,
.current-access-key__title,
.current-access-key__identity,
.current-access-key__boundary {
  display: flex;
  align-items: center;
}

.current-access-key__header {
  justify-content: space-between;
  gap: var(--space-4);
}

.current-access-key__title {
  min-width: 0;
  gap: 10px;
}

.current-access-key__title > svg {
  color: var(--color-action);
}

.current-access-key__title p,
.current-access-key__title h2 {
  margin: 0;
}

.current-access-key__title p,
.current-access-key__facts dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.current-access-key__title h2 {
  margin-top: 2px;
  font-family: var(--font-serif);
  font-size: var(--text-lg);
  font-weight: 550;
}

.current-access-key__identity {
  min-width: 0;
  gap: var(--space-2);
}

.current-access-key__identity code {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.current-access-key__boundary {
  gap: 7px;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.current-access-key__boundary svg {
  flex: 0 0 auto;
  color: var(--color-text-faint);
}

.current-access-key__facts {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  margin: 0;
  gap: 1px;
  background: var(--color-border-subtle);
}

.current-access-key__facts > div {
  min-width: 0;
  background: var(--color-surface);
  padding: 10px 12px;
}

.current-access-key__facts dd {
  display: block;
  min-width: 0;
  overflow: hidden;
  margin: 5px 0 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.current-access-key__limits {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.current-access-key__limits > header,
.current-access-key__limits > header > div,
.current-access-key__limit-heading,
.current-access-key__limit-recovery {
  display: flex;
  align-items: center;
}
.current-access-key__limits > header {
  justify-content: space-between;
  gap: var(--space-3);
}
.current-access-key__limits > header > div {
  gap: var(--space-2);
}
.current-access-key__limits h3 {
  margin: 0;
  font-size: var(--text-meta);
}
.current-access-key__limit-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}
.current-access-key__limit-list article {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-3);
}
.current-access-key__limit-heading {
  justify-content: space-between;
  gap: var(--space-2);
}
.current-access-key__limit-list p,
.current-access-key__limit-note {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.current-access-key__limit-list progress {
  width: 100%;
  height: 7px;
  accent-color: var(--color-action);
}
.current-access-key__limit-recovery {
  min-height: 20px;
  flex-wrap: wrap;
  gap: 4px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

@media (max-width: 860px) {
  .current-access-key__facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .current-access-key__header {
    align-items: flex-start;
    flex-direction: column;
  }

  .current-access-key__facts {
    grid-template-columns: minmax(0, 1fr);
  }

  .current-access-key__limit-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
