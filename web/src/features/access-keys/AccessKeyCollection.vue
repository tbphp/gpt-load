<script setup lang="ts">
import { Pencil } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { revealAccessKey } from '@/app/resources/access-keys'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import MobileRecordCard from '@/components/ui/MobileRecordCard.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import { presentAccessKey } from './access-key-presenter'
import AccessKeySecret from './AccessKeySecret.vue'
import AccessKeyTable from './AccessKeyTable.vue'
import { useEphemeralSecret } from './use-ephemeral-secret'

const props = defineProps<{ accessKeys: AccessKeyDto[]; groups: GroupSummary[] }>()
const emit = defineEmits<{
  edit: [accessKey: AccessKeyDto, trigger: HTMLElement]
  deleted: [name: string]
}>()
const client = useApiClient()
const { locale, t } = useI18n()
const secret = useEphemeralSecret()
const revealPending = ref<number>()
const revealFailed = ref<number>()
const mobile = ref(false)
let revealController: AbortController | undefined
let mediaQuery: MediaQueryList | undefined
let mounted = true

try {
  mediaQuery = window.matchMedia('(max-width: 767px)')
  mobile.value = mediaQuery.matches
} catch {
  mediaQuery = undefined
}

const presentations = computed(() =>
  props.accessKeys.map((accessKey) =>
    presentAccessKey(accessKey, props.groups, {
      locale: locale.value,
      labels: {
        groups: t('accessKeys.filterGroups'),
        protocols: t('accessKeys.filterProtocols'),
        models: t('accessKeys.filterModels'),
        allGroups: t('accessKeys.allGroups'),
        allProtocols: t('accessKeys.allProtocols'),
        allModels: t('accessKeys.allModels'),
        unlimited: t('accessKeys.unlimited'),
      },
      protocolLabel: (protocol) => t(`common.protocols.${protocol}`),
    }),
  ),
)
const revealedId = computed(() => {
  const owner = secret.owner.value
  if (!owner?.startsWith('access-key:')) return undefined
  const id = Number(owner.slice('access-key:'.length))
  return Number.isSafeInteger(id) ? id : undefined
})
const revealedValue = computed(() =>
  revealedId.value === undefined
    ? undefined
    : (secret.read(secretOwner(revealedId.value)) ?? undefined),
)

function secretOwner(id: number): string {
  return `access-key:${id}`
}

function source(id: number): AccessKeyDto {
  const accessKey = props.accessKeys.find((candidate) => candidate.id === id)
  if (!accessKey) throw new Error(`ACCESS_KEY_SOURCE_MISSING:${id}`)
  return accessKey
}

function forwardEdit(accessKey: AccessKeyDto, trigger: HTMLElement): void {
  emit('edit', accessKey, trigger)
}

async function toggleReveal(id: number): Promise<void> {
  if (revealedId.value === id && revealedValue.value) {
    secret.clear()
    revealFailed.value = undefined
    return
  }
  revealController?.abort()
  secret.clear()
  const expectedEpoch = secret.epoch.value
  const controller = new AbortController()
  revealController = controller
  revealPending.value = id
  revealFailed.value = undefined
  try {
    const result = await revealAccessKey(client, id, controller.signal)
    if (mounted && revealController === controller && secret.epoch.value === expectedEpoch) {
      secret.expose(secretOwner(id), result.key)
    }
  } catch (error: unknown) {
    if (mounted && revealController === controller && !(error instanceof RequestCancelledError)) {
      revealFailed.value = id
    }
  } finally {
    if (revealController === controller) {
      revealController = undefined
      revealPending.value = undefined
    }
  }
}

function updateMedia(event: MediaQueryListEvent): void {
  mobile.value = event.matches
}

function conceal(): void {
  const controller = revealController
  revealController = undefined
  controller?.abort()
  revealPending.value = undefined
  revealFailed.value = undefined
  secret.clear()
}

defineExpose({ conceal })

onMounted(() => mediaQuery?.addEventListener('change', updateMedia))

watch(
  () => props.accessKeys.map(({ id }) => id),
  (ids) => {
    if (revealedId.value !== undefined && !ids.includes(revealedId.value)) secret.clear()
  },
)

onBeforeUnmount(() => {
  mounted = false
  mediaQuery?.removeEventListener('change', updateMedia)
  conceal()
})
</script>

<template>
  <div class="access-key-collection">
    <div v-if="mobile" class="access-key-collection__cards">
      <MobileRecordCard
        v-for="record in presentations"
        :key="record.id"
        :label="record.name"
        :data-test="`access-key-card-${record.id}`"
      >
        <template #header>
          <h2>{{ record.name }}</h2>
          <StatusBadge :tone="record.status === 'active' ? 'success' : 'neutral'">
            {{ t(`accessKeys.status.${record.status}`) }}
          </StatusBadge>
        </template>

        <AccessKeySecret
          :id="record.id"
          :masked-key="record.maskedKey"
          :revealed-value="revealedId === record.id ? revealedValue : undefined"
          :pending="revealPending === record.id"
          :failed="revealFailed === record.id"
          @toggle="toggleReveal"
        />
        <dl class="access-key-card__summary">
          <dt>{{ t('accessKeys.columns.filters') }}</dt>
          <dd>{{ record.scopeSummary }}</dd>
          <dt>{{ t('accessKeys.columns.rpm') }}</dt>
          <dd>{{ record.rpm }}</dd>
        </dl>
        <details>
          <summary>{{ t('accessKeys.details') }}</summary>
          <dl>
            <template v-for="scope in record.scopeRows" :key="scope.label">
              <dt>{{ scope.label }}</dt>
              <dd>{{ scope.value }}</dd>
            </template>
            <dt>{{ t('accessKeys.createdAt') }}</dt>
            <dd>
              <AppDateTime :instant="record.createdAt" :locale="locale" />
            </dd>
            <dt>{{ t('accessKeys.updatedAt') }}</dt>
            <dd>
              <AppDateTime :instant="record.updatedAt" :locale="locale" />
            </dd>
          </dl>
        </details>

        <template #actions>
          <button
            type="button"
            class="access-key-card__edit"
            :data-test="`access-key-edit-${record.id}`"
            @click="emit('edit', source(record.id), $event.currentTarget as HTMLElement)"
          >
            <Pencil :size="16" aria-hidden="true" />{{ t('accessKeys.edit') }}
          </button>
          <AccessKeyDeleteDialog
            :access-key="source(record.id)"
            :total="accessKeys.length"
            @deleted="emit('deleted', $event)"
          />
        </template>
      </MobileRecordCard>
    </div>

    <AccessKeyTable
      v-else
      :access-keys="accessKeys"
      :presentations="presentations"
      :revealed-id="revealedId"
      :revealed-value="revealedValue"
      :reveal-pending-id="revealPending"
      :reveal-failed-id="revealFailed"
      @toggle-reveal="toggleReveal"
      @edit="forwardEdit"
      @deleted="emit('deleted', $event)"
    />
  </div>
</template>

<style scoped>
.access-key-collection {
  min-width: 0;
}

.access-key-collection__cards {
  display: grid;
  gap: var(--space-3);
}

.access-key-collection__cards h2 {
  min-width: 0;
  margin: 0;
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}

.access-key-collection__cards :deep(.status-badge) {
  flex-shrink: 0;
  white-space: nowrap;
}

.access-key-card__summary {
  display: grid;
  grid-template-columns: minmax(6rem, auto) minmax(0, 1fr);
  gap: var(--space-2);
  margin: var(--space-3) 0 0;
}

.access-key-card__summary dt {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.access-key-card__summary dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.access-key-collection details {
  margin-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.access-key-collection summary {
  min-height: var(--touch-target);
  color: var(--color-action);
  cursor: pointer;
  font-weight: 650;
}

.access-key-collection details dl {
  margin-top: var(--space-3);
}

.access-key-card__edit {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  border: 0;
  background: transparent;
  color: var(--color-action);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
</style>
