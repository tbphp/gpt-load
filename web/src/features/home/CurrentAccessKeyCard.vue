<script setup lang="ts">
import { Gauge, KeyRound, LockKeyhole } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  AccessKeyCollectionItemDto,
  AccessKeyCostLimitRuleStatusDto,
} from '@/api/control/types'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatInteger, formatUSD } from '@/lib/format'
import { quotaProgressTone } from '@/lib/quota-progress'

import AccessKeyCostLimitWindowTime from '@/features/access-keys/AccessKeyCostLimitWindowTime.vue'

const props = defineProps<{ accessKey: AccessKeyCollectionItemDto }>()
const { locale, n, t } = useI18n()

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

function remainingPercent(rule: AccessKeyCostLimitRuleStatusDto): number {
  if (rule.status === 'inactive') return 100
  const limit = Number(rule.limit_usd)
  const remaining = Number(rule.remaining_usd)
  if (!Number.isFinite(limit) || !Number.isFinite(remaining) || limit <= 0) return 0
  return Math.round(Math.max(0, Math.min(100, (remaining / limit) * 100)))
}

function ruleTone(rule: AccessKeyCostLimitRuleStatusDto): 'success' | 'warning' | 'danger' {
  return quotaProgressTone(remainingPercent(rule), rule.status === 'exhausted')
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

    <section
      v-if="costLimits !== null && costLimits.rules.length > 0"
      class="current-access-key__limits"
      aria-labelledby="cost-limits-title"
    >
      <header>
        <div>
          <Gauge :size="15" aria-hidden="true" />
          <h3 id="cost-limits-title">{{ t('home.ledger.currentAccessKey.costLimits.title') }}</h3>
        </div>
        <StatusBadge :tone="costLimits.allowed ? 'success' : 'danger'" size="compact">
          {{
            t(
              costLimits.allowed
                ? 'home.ledger.currentAccessKey.costLimits.available'
                : 'home.ledger.currentAccessKey.costLimits.blocked',
            )
          }}
        </StatusBadge>
      </header>

      <div class="current-access-key__limit-list">
        <article
          v-for="rule in costLimits.rules"
          :key="rule.id"
          :class="`current-access-key__limit-card--${ruleTone(rule)}`"
        >
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
                used: formatUSD(rule.used_usd, locale),
                limit: formatUSD(rule.limit_usd, locale),
                remaining: formatUSD(rule.remaining_usd, locale),
              })
            }}
          </p>
          <div class="current-access-key__limit-progress">
            <QuotaProgressBar
              :value="remainingPercent(rule)"
              :tone="ruleTone(rule)"
              :label="ruleLabel(rule.kind, rule.period_seconds)"
              :value-text="
                t('accessKeys.costLimits.remainingPercent', {
                  value: n(remainingPercent(rule)),
                })
              "
            />
            <strong>{{ n(remainingPercent(rule)) }}%</strong>
          </div>
          <div class="current-access-key__limit-recovery">
            <template v-if="rule.kind === 'periodic'">
              {{
                t(
                  rule.status === 'inactive'
                    ? 'home.ledger.currentAccessKey.costLimits.previewEndsAt'
                    : rule.status === 'exhausted'
                      ? 'home.ledger.currentAccessKey.costLimits.availableAgain'
                      : 'home.ledger.currentAccessKey.costLimits.resetsAt',
                )
              }}
              <AccessKeyCostLimitWindowTime :rule="rule" />
            </template>
            <template v-else-if="rule.status === 'exhausted'">
              {{ t('home.ledger.currentAccessKey.costLimits.notAutomatic') }}
            </template>
          </div>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.current-access-key {
  display: grid;
  gap: 14px;
  margin-top: var(--space-4);
  /* 同上：只留上边线，避免和下一个板块的上边线撞成两条。 */
  border-top: 1px solid var(--color-border-subtle);
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
  border: 1px solid color-mix(in srgb, var(--color-action) 24%, var(--color-border-subtle));
  border-left: 3px solid var(--color-success);
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--color-action-soft) 46%, var(--color-surface));
  padding: var(--space-3);
  box-shadow:
    0 1px 2px color-mix(in srgb, var(--color-action) 14%, transparent),
    0 8px 24px color-mix(in srgb, var(--color-action) 10%, transparent);
}
.current-access-key__limit-list article.current-access-key__limit-card--warning {
  border-left-color: var(--color-warning);
}
.current-access-key__limit-list article.current-access-key__limit-card--danger {
  border-color: var(--color-feedback-danger-border);
  border-left-color: var(--color-danger);
  background: color-mix(in srgb, var(--color-danger-bg) 82%, var(--color-surface));
  box-shadow:
    0 1px 2px color-mix(in srgb, var(--color-danger) 14%, transparent),
    0 8px 24px color-mix(in srgb, var(--color-danger) 9%, transparent);
}
.current-access-key__limit-heading {
  justify-content: space-between;
  gap: var(--space-2);
}
.current-access-key__limit-list p {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.current-access-key__limit-progress {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}
.current-access-key__limit-progress strong {
  min-width: 36px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  font-weight: 650;
  text-align: right;
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
