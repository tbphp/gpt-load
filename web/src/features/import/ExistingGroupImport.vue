<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Check, ChevronLeft } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { importGroupKeys, listGroups, type GroupKeyImportResult } from '@/api/control/groups'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import KeyTextarea from './KeyTextarea.vue'
import type { ExistingGroupImportDraft } from './model-draft'
import { useDirtyNavigation } from './use-dirty-navigation'

const props = defineProps<{ initialDraft?: ExistingGroupImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const keys = ref(props.initialDraft?.keys ?? '')
const reviewing = ref(false)
const pending = ref(false)
const completed = ref(false)
const errorKey = ref('')
const result = ref<GroupKeyImportResult | null>(null)
let activeController: AbortController | undefined

function parsePositiveID(value: unknown): number | null {
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return null
  const id = Number(value)
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

const selectedID = computed(() => parsePositiveID(route.query.group_id))
const groupsQuery = useQuery({
  queryKey: controlQueryKeys.groups.list(),
  queryFn: ({ signal }) => listGroups(api, signal),
})
const selectedGroup = computed(
  () => groupsQuery.data.value?.find((group) => group.id === selectedID.value) ?? null,
)
const keyAnalysis = computed(() => analyzeKeys(keys.value))
const canReview = computed(
  () =>
    !pending.value &&
    selectedGroup.value !== null &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys,
)
const dirty = computed(() => !completed.value && keys.value !== '')

useDirtyNavigation(dirty)
const unregisterRecovery = recovery.register(() =>
  completed.value
    ? null
    : {
        mode: 'existing',
        group_id: selectedGroup.value?.id ?? null,
        keys: keys.value,
      },
)

function selectGroup(event: Event): void {
  const id = parsePositiveID((event.target as HTMLSelectElement).value)
  reviewing.value = false
  errorKey.value = ''
  result.value = null
  const query: Record<string, string> = { mode: 'existing' }
  if (id !== null) query.group_id = String(id)
  void router.push({ name: 'import', query })
}

function showReview(): void {
  if (!canReview.value) return
  errorKey.value = ''
  reviewing.value = true
}

async function submit(): Promise<void> {
  const group = selectedGroup.value
  if (
    !group ||
    pending.value ||
    keyAnalysis.value.nonEmptyCount === 0 ||
    keyAnalysis.value.tooManyKeys
  ) {
    return
  }

  activeController?.abort()
  const controller = new AbortController()
  activeController = controller
  pending.value = true
  errorKey.value = ''
  try {
    result.value = await importGroupKeys(api, group.id, { keys: keys.value }, controller.signal)
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.keys(group.id) }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.detail(group.id) }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.list() }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.health() }),
    ])
    completed.value = true
    keys.value = ''
    recovery.clear()
  } catch (error: unknown) {
    if (!(error instanceof RequestCancelledError)) errorKey.value = 'import.existing.importFailed'
  } finally {
    if (activeController === controller) {
      activeController = undefined
      pending.value = false
    }
  }
}

onBeforeUnmount(() => {
  activeController?.abort()
  unregisterRecovery()
})
</script>

<template>
  <div class="existing-import">
    <SurfaceCard class="existing-card">
      <header>
        <h2>{{ t('import.existing.title') }}</h2>
        <p>{{ t('import.existing.description') }}</p>
      </header>

      <InlineFeedback v-if="groupsQuery.isPending.value" tone="info">
        {{ t('import.existing.groupsLoading') }}
      </InlineFeedback>
      <InlineFeedback
        v-else-if="groupsQuery.isError.value && !groupsQuery.data.value"
        tone="danger"
      >
        {{ t('import.existing.groupsFailed') }}
      </InlineFeedback>

      <template v-else-if="!result">
        <label class="group-selector" for="existing-group-select">
          <span>{{ t('import.existing.groupLabel') }}</span>
          <select
            id="existing-group-select"
            data-test="existing-group"
            :value="selectedGroup?.id ?? ''"
            :disabled="pending"
            @change="selectGroup"
          >
            <option value="">{{ t('import.existing.groupPlaceholder') }}</option>
            <option v-for="group in groupsQuery.data.value ?? []" :key="group.id" :value="group.id">
              {{ group.name }}
            </option>
          </select>
        </label>

        <template v-if="!reviewing">
          <KeyTextarea v-model="keys" :disabled="pending" />
          <footer class="card-actions">
            <AppButton data-test="existing-review" :disabled="!canReview" @click="showReview">
              {{ t('import.review') }}
            </AppButton>
          </footer>
        </template>

        <template v-else>
          <section class="review" :aria-labelledby="'existing-review-title'">
            <h3 id="existing-review-title">{{ t('import.existing.reviewTitle') }}</h3>
            <p>{{ t('import.existing.reviewDescription') }}</p>
            <dl>
              <div>
                <dt>{{ t('import.existing.groupLabel') }}</dt>
                <dd>{{ selectedGroup?.name }}</dd>
              </div>
              <div>
                <dt>{{ t('import.keys.label') }}</dt>
                <dd>{{ t('import.keys.count', { count: keyAnalysis.nonEmptyCount }) }}</dd>
              </div>
            </dl>
          </section>
          <InlineFeedback v-if="errorKey" tone="danger">{{ t(errorKey) }}</InlineFeedback>
          <footer class="card-actions split">
            <AppButton variant="secondary" :disabled="pending" @click="reviewing = false">
              <ChevronLeft :size="16" aria-hidden="true" />{{ t('import.back') }}
            </AppButton>
            <AppButton data-test="existing-submit" :busy="pending" @click="submit">
              {{ t('import.existing.submit') }}
            </AppButton>
          </footer>
        </template>
      </template>

      <section v-else data-test="existing-result" class="result" role="status" aria-live="polite">
        <Check :size="22" aria-hidden="true" />
        <div>
          <h3>{{ t('import.existing.successTitle') }}</h3>
          <p>
            {{
              t('import.existing.successSummary', {
                added: result.keys_added,
                duplicated: result.keys_duplicated,
              })
            }}
          </p>
        </div>
      </section>
    </SurfaceCard>
  </div>
</template>

<style scoped>
.existing-import {
  width: 100%;
  max-width: 760px;
  margin: 0 auto;
}
.existing-card {
  display: grid;
  gap: var(--space-5);
  padding: var(--space-6);
}
header h2,
header p,
.review h3,
.review p,
.result h3,
.result p {
  margin: 0;
}
header h2,
.review h3,
.result h3 {
  font-size: 1.2rem;
}
header p,
.review p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.group-selector {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
.group-selector select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.card-actions {
  display: flex;
  justify-content: flex-end;
}
.card-actions.split {
  justify-content: space-between;
}
.card-actions :deep(.app-button) {
  gap: var(--space-2);
}
.review {
  display: grid;
  gap: var(--space-3);
}
.review dl {
  display: grid;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
}
.review dl div {
  display: grid;
  grid-template-columns: minmax(140px, 0.5fr) 1fr;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}
.review dl div:last-child {
  border-bottom: 0;
}
.review dt {
  color: var(--color-text-muted);
}
.review dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.result {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-success) 35%, var(--color-border));
  border-radius: var(--radius-card);
  background: var(--color-success-bg);
  color: var(--color-success);
  padding: var(--space-4);
}
@media (max-width: 560px) {
  .existing-card {
    padding: var(--space-4);
  }
  .review dl div {
    grid-template-columns: 1fr;
    gap: var(--space-1);
  }
}
</style>
