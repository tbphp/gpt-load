<script setup lang="ts">
import { KeyRound, LockKeyhole } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyCollectionItemDto } from '@/api/control/types'
import AppDateTime from '@/components/ui/AppDateTime.vue'
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
}
</style>
