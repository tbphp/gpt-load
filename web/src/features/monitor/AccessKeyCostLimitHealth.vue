<script setup lang="ts">
import { ArrowRight, KeyRound } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { HealthAccessKeyCostLimitDto } from '@/api/control/types'
import { accessKeysLocation } from '@/app/route-locations'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import Surface from '@/components/ui/Surface.vue'

import MonitorSectionHeading from './MonitorSectionHeading.vue'

defineProps<{ accessKeys: HealthAccessKeyCostLimitDto[] }>()
const { locale, t } = useI18n()

function editLocation(accessKeyID: number) {
  return accessKeysLocation({ action: 'edit', access_key_id: String(accessKeyID) })
}

function ruleLabel(rule: HealthAccessKeyCostLimitDto['blocking_rules'][number]): string {
  if (rule.kind === 'total') return t('monitor.health.accessKeyLimits.total')
  let period: string
  if (rule.period_seconds % 86_400 === 0) {
    period = t('monitor.health.accessKeyLimits.periodDays', {
      count: rule.period_seconds / 86_400,
    })
  } else if (rule.period_seconds % 3_600 === 0) {
    period = t('monitor.health.accessKeyLimits.periodHours', {
      count: rule.period_seconds / 3_600,
    })
  } else if (rule.period_seconds % 60 === 0) {
    period = t('monitor.health.accessKeyLimits.periodMinutes', {
      count: rule.period_seconds / 60,
    })
  } else {
    period = t('monitor.health.accessKeyLimits.periodSeconds', {
      count: rule.period_seconds,
    })
  }
  return t('monitor.health.accessKeyLimits.periodic', { period })
}
</script>

<template>
  <section class="access-key-limit-health" aria-labelledby="access-key-limit-health-title">
    <MonitorSectionHeading
      id="access-key-limit-health-title"
      :title="t('monitor.health.accessKeyLimits.title')"
      :description="t('monitor.health.accessKeyLimits.description')"
    />

    <Surface :padded="false">
      <article v-for="accessKey in accessKeys" :key="accessKey.access_key_id">
        <div class="access-key-limit-health__identity">
          <KeyRound :size="15" aria-hidden="true" />
          <div>
            <strong>{{ accessKey.name }}</strong>
            <code>{{ accessKey.masked_key }}</code>
          </div>
        </div>

        <div class="access-key-limit-health__rules">
          <span v-for="rule in accessKey.blocking_rules" :key="rule.id">
            {{ ruleLabel(rule) }} · ${{ rule.used_usd }} / ${{ rule.limit_usd }}
          </span>
        </div>

        <div class="access-key-limit-health__recovery">
          <StatusBadge :tone="accessKey.recoverable ? 'warning' : 'danger'" size="compact">
            {{
              t(
                accessKey.recoverable
                  ? 'monitor.health.accessKeyLimits.temporary'
                  : 'monitor.health.accessKeyLimits.manual',
              )
            }}
          </StatusBadge>
          <span v-if="accessKey.next_available_at_ms !== null">
            {{ t('monitor.health.accessKeyLimits.availableAgain') }}
            <AppRelativeTime
              :instant="accessKey.next_available_at_ms"
              :locale="locale"
              :empty-label="t('monitor.health.accessKeyLimits.notAutomatic')"
              hint
            />
          </span>
          <span v-else>{{ t('monitor.health.accessKeyLimits.notAutomatic') }}</span>
        </div>

        <RouterLink
          class="access-key-limit-health__action"
          :to="editLocation(accessKey.access_key_id)"
        >
          {{ t('monitor.health.accessKeyLimits.manage') }}
          <ArrowRight :size="14" aria-hidden="true" />
        </RouterLink>
      </article>
    </Surface>
  </section>
</template>

<style scoped>
.access-key-limit-health {
  display: grid;
  gap: var(--space-4);
}
.access-key-limit-health :deep(.surface) {
  overflow: hidden;
}
.access-key-limit-health article {
  display: grid;
  grid-template-columns: minmax(180px, 0.8fr) minmax(240px, 1.4fr) minmax(220px, 1fr) auto;
  align-items: center;
  gap: var(--space-4);
  padding: 12px 14px;
}
.access-key-limit-health article + article {
  border-top: 1px solid var(--color-border-subtle);
}
.access-key-limit-health__identity,
.access-key-limit-health__recovery,
.access-key-limit-health__action {
  display: flex;
  align-items: center;
}
.access-key-limit-health__identity {
  min-width: 0;
  gap: var(--space-2);
}
.access-key-limit-health__identity > svg {
  flex: 0 0 auto;
  color: var(--color-danger);
}
.access-key-limit-health__identity strong,
.access-key-limit-health__identity code {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.access-key-limit-health__identity code,
.access-key-limit-health__rules,
.access-key-limit-health__recovery {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.access-key-limit-health__rules {
  display: grid;
  gap: 3px;
  font-family: var(--font-mono);
}
.access-key-limit-health__recovery {
  flex-wrap: wrap;
  gap: var(--space-2);
}
.access-key-limit-health__action {
  gap: 4px;
  color: var(--color-action);
  font-size: var(--text-sm);
  font-weight: 600;
  text-decoration: none;
}
.access-key-limit-health__action:hover {
  text-decoration: underline;
}
.access-key-limit-health__action:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 3px;
  border-radius: var(--radius-control);
}
@media (max-width: 900px) {
  .access-key-limit-health article {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
