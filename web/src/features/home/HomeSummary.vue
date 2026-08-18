<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HomeBaseDto } from '@/app/resources/home'
import { formatDuration, formatInteger, formatLocalInstant, formatLocalTime } from '@/lib/format'

const props = defineProps<{
  base: HomeBaseDto
  observedAtMs: number | null
  uptimeNowMs: number
}>()

const { locale, t } = useI18n()

const updated = computed(() =>
  props.observedAtMs === null ? '—' : formatLocalTime(props.observedAtMs, locale.value),
)
const updatedTitle = computed(() =>
  props.observedAtMs === null ? undefined : formatLocalInstant(props.observedAtMs, locale.value),
)
</script>

<template>
  <header class="home-summary__header">
    <div>
      <h1 id="home-title" class="home-summary__facts">
        <span class="home-summary__fact">
          <strong>{{ formatInteger(base.inventory.group_count, locale) }}</strong>
          <span>{{ t('home.ledger.factGroups') }}</span>
        </span>
        <span class="home-summary__separator" aria-hidden="true"> · </span>
        <span class="home-summary__fact">
          <strong>
            {{ formatInteger(base.inventory.available_credential_count, locale) }}/{{
              formatInteger(base.inventory.credential_count, locale)
            }}
          </strong>
          <span>{{ t('home.ledger.factAvailableCredentials') }}</span>
        </span>
        <span class="home-summary__separator" aria-hidden="true"> · </span>
        <span class="home-summary__fact">
          <strong>{{ formatInteger(base.inventory.model_count, locale) }}</strong>
          <span>{{ t('home.ledger.factModels') }}</span>
        </span>
      </h1>
    </div>
    <dl class="home-summary__stamp">
      <div>
        <dt>{{ t('home.ledger.updated') }}</dt>
        <dd :title="updatedTitle">{{ updated }}</dd>
      </div>
      <div>
        <dt>{{ t('home.ledger.version') }}</dt>
        <dd>{{ base.version }}</dd>
      </div>
      <div>
        <dt>{{ t('home.ledger.uptime') }}</dt>
        <dd>{{ formatDuration(base.started_at_ms, uptimeNowMs, locale) }}</dd>
      </div>
    </dl>
  </header>
</template>

<style scoped>
.home-summary__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 22px;
  flex-wrap: wrap;
  /*
   * 不画下边线：分隔线一律由「开启新板块」的元素自己的 border-top 承担。
   * 否则没有告警时，这条线会和下一个板块的上边线并排出现两条。
   * 下方间距同样交给板块的 margin-top，这里不再重复留白。
   */
}

.home-summary__facts {
  max-width: none;
  margin: 0;
  color: var(--color-text-muted);
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  line-height: var(--line-compact);
  letter-spacing: -0.015em;
}

.home-summary__fact {
  color: var(--color-text-muted);
}

.home-summary__fact strong {
  color: var(--color-text);
  font-weight: 650;
}

.home-summary__fact > span,
.home-summary__separator {
  color: var(--color-text-muted);
  font-weight: 500;
}

.home-summary__stamp {
  display: grid;
  grid-template-columns: max-content max-content;
  gap: 5px 1ch;
  margin: 0;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: var(--line-compact);
  white-space: nowrap;
}

.home-summary__stamp div {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: baseline;
}
.home-summary__stamp dt,
.home-summary__stamp dd {
  margin: 0;
}
.home-summary__stamp dt {
  text-align: right;
}
.home-summary__stamp dd {
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  text-align: left;
}

@media (max-width: 860px) {
  .home-summary__header {
    align-items: start;
  }
  .home-summary__stamp {
    justify-content: start;
  }
}
</style>
