<script setup lang="ts">
import { Eye } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useApiClient } from '@shared/http/client-context'
import { InvalidResponseError } from '@shared/http/errors'
import type { InspectionGroup } from '@modern/api/inspector'
import { getGroupCredentials } from '@modern/api/group-detail'
import {
  AppBadge,
  AppButton,
  AppIconButton,
  AppOverflowText,
  AppPagination,
} from '@modern/components/ui'
import { credentialTime } from '@modern/features/groups/credential-presentation'
import { groupWeight, reasonLabel } from './inspection-display'

const props = defineProps<{ group: InspectionGroup; observedAt: number }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const router = useRouter()
const page = ref(1)
const pageSize = ref(20)
const rows = computed(() =>
  [...props.group.credentials].sort(
    (a, b) =>
      Number(b.available) - Number(a.available) ||
      b.effectiveWeight - a.effectiveWeight ||
      a.id - b.id,
  ),
)
const visible = computed(() =>
  rows.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
watch(
  () => rows.value.length,
  () => {
    page.value = Math.min(page.value, Math.max(1, Math.ceil(rows.value.length / pageSize.value)))
  },
)
const identities = useQuery(
  computed(() => ({
    queryKey: [
      'modern',
      'inspection-identities',
      props.group.id,
      props.observedAt,
      visible.value.map((row) => row.id),
    ],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const id = props.group.id
      const missing = new Set(visible.value.map((row) => row.id))
      const filters = {
        page: 1,
        pageSize: 100,
        q: '',
        status: '' as const,
        sort: 'priority' as const,
        proxy: '' as const,
        reset: '' as const,
      }
      const first = await getGroupCredentials(client, id, filters, signal)
      const rows = [...first.items]
      first.items.forEach((row) => missing.delete(row.id))
      const pages = Math.ceil(first.total / first.pageSize)
      for (let index = 2; index <= pages && missing.size > 0; index++) {
        const next = await getGroupCredentials(client, id, { ...filters, page: index }, signal)
        if (next.total !== first.total) throw new InvalidResponseError()
        rows.push(...next.items)
        next.items.forEach((row) => missing.delete(row.id))
      }
      return new Map(rows.map((row) => [row.id, row.account || row.mask]))
    },
    enabled: props.group.credentials.length > 0,
  })),
)
function identity(id: number): string {
  const values = identities.data.value
  if (!values) return '—'
  return values.has(id) ? values.get(id) || t('logs.unavailableCredential') : t('logs.deleted')
}
function setPageSize(value: number): void {
  pageSize.value = value
  page.value = 1
}
</script>

<template>
  <div class="modern-inspection-credentials">
    <div class="modern-inspection-weights">
      <span
        >{{ t('inspector.groupWeight') }} <strong>{{ n(group.weight ?? 50) }}</strong></span
      >
      <span
        >{{ t('inspector.effectiveWeight') }} <strong>{{ n(groupWeight(group)) }}</strong></span
      >
    </div>
    <div v-if="identities.isError.value" class="modern-inspection-name-error" role="status">
      <span>{{ t('inspector.identitiesFailed') }}</span>
      <AppButton size="xs" @click="identities.refetch()">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!rows.length" class="modern-inspection-empty">{{ t('inspector.noCredentials') }}</p>
    <template v-else>
      <div
        class="modern-inspection-credential-table"
        role="table"
        :aria-label="t('inspector.credential')"
      >
        <div
          class="modern-inspection-credential-columns modern-inspection-credential-head"
          role="row"
        >
          <span role="columnheader">{{ t('inspector.credential') }}</span>
          <span role="columnheader">{{ t('inspector.status') }}</span>
          <span role="columnheader">{{ t('inspector.weight') }}</span>
          <span role="columnheader">{{ t('inspector.effectiveWeight') }}</span>
          <span role="columnheader">{{ t('inspector.cooldown') }}</span>
          <span role="columnheader" class="modern-sr-only">{{
            t('inspector.openCredential')
          }}</span>
        </div>
        <div
          v-for="row in visible"
          :key="row.id"
          class="modern-inspection-credential-columns"
          role="row"
        >
          <div role="cell"><AppOverflowText :text="identity(row.id)" /></div>
          <div role="cell">
            <AppBadge :tone="row.available ? 'success' : 'neutral'" variant="plain" size="xs" dot>
              {{ row.available ? t('inspector.available') : reasonLabel(row.reason, t) }}
            </AppBadge>
          </div>
          <span role="cell">{{ n(row.weight) }}</span>
          <span role="cell">{{ n(row.effectiveWeight) }}</span>
          <span role="cell">{{ credentialTime(row.cooldownUntil, locale) }}</span>
          <div role="cell">
            <AppIconButton
              :icon="Eye"
              :label="t('inspector.openCredential')"
              size="xs"
              variant="ghost"
              @click="
                router.push({
                  name: 'modern-group-detail',
                  params: { id: group.id },
                  query: { credential: String(row.id) },
                })
              "
            />
          </div>
        </div>
      </div>
      <AppPagination
        v-if="rows.length > 20"
        :page="page"
        :page-size="pageSize"
        :total="rows.length"
        mode="total"
        @update:page="page = $event"
        @update:page-size="setPageSize"
      />
    </template>
  </div>
</template>

<style scoped>
.modern-inspection-credentials {
  padding: var(--modern-space-3);
  background: var(--modern-subtle);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-inspection-name-error {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  color: var(--modern-danger);
  margin-bottom: var(--modern-space-3);
}
.modern-inspection-weights {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-4);
  margin-bottom: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-inspection-weights strong {
  margin-inline-start: var(--modern-space-1);
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-inspection-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-inspection-credential-table {
  min-width: 780px;
}
.modern-inspection-credential-columns {
  display: grid;
  grid-template-columns: minmax(180px, 1.4fr) minmax(180px, 1fr) 110px 110px 160px 32px;
  align-items: center;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
.modern-inspection-credential-columns > * {
  min-width: 0;
}
.modern-inspection-credential-head {
  color: var(--modern-muted);
}
</style>
