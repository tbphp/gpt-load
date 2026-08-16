<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { RequestLogItemDto } from '@/app/resources/request-logs'
import { monitorLocation } from '@/app/route-locations'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { recentRequestLogWindow, recentRequestOutcome } from './recent-request'

const props = defineProps<{
  log: RequestLogItemDto | null
  loading: boolean
  failed: boolean
}>()

const { locale, t } = useI18n()

const outcome = computed(() => (props.log === null ? null : recentRequestOutcome(props.log)))
const outcomeLabel = computed(() => {
  if (outcome.value === null) return ''
  const reason = t(`home.ledger.recentRequest.${outcome.value.labelKey}`)
  return outcome.value.statusCode === null
    ? reason
    : t('home.ledger.recentRequest.failure', { code: outcome.value.statusCode, reason })
})
const logLocation = computed(() =>
  props.log === null
    ? monitorLocation({ tab: 'logs' })
    : monitorLocation({
        tab: 'logs',
        selected_request_id: props.log.request_id,
        ...recentRequestLogWindow(props.log.completed_at_ms),
      }),
)
</script>

<template>
  <section class="recent-request" aria-labelledby="recent-request-label">
    <p id="recent-request-label" class="recent-request__label">
      {{ t('home.ledger.recentRequest.label') }}
    </p>

    <SkeletonBlock
      v-if="loading"
      class="recent-request__skeleton"
      width="48%"
      height="0.9rem"
      :aria-label="t('home.ledger.recentRequest.loading')"
    />

    <p v-else-if="failed" class="recent-request__muted">
      {{ t('home.ledger.recentRequest.error') }}
    </p>

    <p v-else-if="log === null" class="recent-request__muted">
      {{ t('home.ledger.recentRequest.empty') }}
    </p>

    <RouterLink v-else class="recent-request__row" :to="logLocation">
      <AppRelativeTime
        class="recent-request__time"
        :instant="log.completed_at_ms"
        :locale="locale"
        :empty-label="t('home.ledger.recentRequest.empty')"
      />
      <span class="recent-request__sep" aria-hidden="true">·</span>
      <span class="recent-request__model">{{
        log.client_model ?? t('home.ledger.recentRequest.unknownModel')
      }}</span>
      <StatusBadge v-if="outcome" :tone="outcome.tone" size="compact">
        {{ outcomeLabel }}
      </StatusBadge>
      <span class="recent-request__go" aria-hidden="true">→</span>
    </RouterLink>
  </section>
</template>

<style scoped>
.recent-request {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  padding: 14px 0 0;
}

.recent-request__label {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.07em;
  text-transform: uppercase;
}

.recent-request__skeleton {
  max-width: 260px;
}

.recent-request__muted {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}

.recent-request__row {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  margin: -6px -8px;
  border-radius: var(--radius-control);
  padding: 6px 8px;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.recent-request__row:hover {
  background: var(--color-interactive-hover);
}

.recent-request__sep {
  color: var(--color-border-control);
}

.recent-request__model {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.recent-request__go {
  margin-left: auto;
  color: var(--color-action);
  font-family: var(--font-mono);
  font-weight: 600;
}

@media (max-width: 560px) {
  .recent-request__go {
    margin-left: 0;
  }
}
</style>
