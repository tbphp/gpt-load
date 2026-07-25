<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { Check, ChevronLeft, ChevronRight, Search, TriangleAlert } from 'lucide-vue-next'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import {
  createGroup,
  discoverModels,
  importGroupKeys,
  isUpstreamUrlConflictData,
  type GroupCreateRequest,
  type UpstreamUrlConflictData,
} from '@/api/control/groups'
import type { GroupProtocol } from '@/api/control/types'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import ModelDraftEditor from '@/components/config/ModelDraftEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import { channelPresets, type ChannelPreset } from './channel-presets'
import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import KeyTextarea from './KeyTextarea.vue'
import type { ImportDraft } from './model-draft'
import { createModelDraft, toGroupModels } from './model-draft'
import { useDirtyNavigation } from './use-dirty-navigation'

const props = defineProps<{ initialDraft?: ImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const router = useRouter()
const { t } = useI18n()

function freshDraft(): ImportDraft {
  return {
    mode: 'new',
    step: 1,
    preset_id: 'openai',
    name: '',
    upstream_url: channelPresets[0]!.upstream_url,
    protocols: [...channelPresets[0]!.protocols],
    keys: '',
    header_rules: { set: {}, remove: [] },
    models: [],
  }
}

const source = props.initialDraft ?? freshDraft()
const draft = reactive<ImportDraft>({
  ...source,
  protocols: [...source.protocols],
  header_rules: { set: { ...source.header_rules.set }, remove: [...source.header_rules.remove] },
  models: source.models.map((model) => ({ ...model })),
})
const pending = ref(false)
const discoveryReady = ref(draft.step > 1 && draft.models.length > 0)
const discoveryFailed = ref(false)
const manualMode = ref(
  draft.models.length > 0 ||
    (props.initialDraft?.step === 2 && props.initialDraft.models.length === 0),
)
const errorKey = ref('')
const conflict = ref<UpstreamUrlConflictData | null>(null)
const completed = ref(false)
let activeController: AbortController | undefined

const keyAnalysis = computed(() => analyzeKeys(draft.keys))
const dirty = computed(
  () =>
    !completed.value &&
    (draft.name !== '' ||
      draft.keys !== '' ||
      draft.step !== 1 ||
      draft.upstream_url !== channelPresets[0]!.upstream_url ||
      draft.protocols.join(',') !== 'openai' ||
      Object.keys(draft.header_rules.set).length > 0 ||
      draft.header_rules.remove.length > 0),
)
const headerRulesValid = computed(() => {
  const names = [...Object.keys(draft.header_rules.set), ...draft.header_rules.remove]
    .map((name) => name.trim().toLocaleLowerCase())
    .filter(Boolean)
  return new Set(names).size === names.length
})
const canDiscover = computed(
  () =>
    !pending.value &&
    draft.upstream_url.trim() !== '' &&
    draft.protocols.length > 0 &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys &&
    headerRulesValid.value,
)
const canReview = computed(() => !pending.value && toGroupModels(draft.models).length > 0)
const protocols: GroupProtocol[] = ['openai', 'anthropic', 'gemini']

useDirtyNavigation(dirty)
const unregisterRecovery = recovery.register(() => (completed.value ? null : snapshotDraft()))

function snapshotDraft(): ImportDraft {
  return {
    ...draft,
    protocols: [...draft.protocols],
    header_rules: { set: { ...draft.header_rules.set }, remove: [...draft.header_rules.remove] },
    models: draft.models.map((model) => ({ ...model })),
  }
}

function startAction(): AbortController {
  activeController?.abort()
  activeController = new AbortController()
  pending.value = true
  errorKey.value = ''
  return activeController
}

function finishAction(controller: AbortController): void {
  if (activeController === controller) {
    activeController = undefined
    pending.value = false
  }
}

function invalidateDiscovery(): void {
  if (activeController) {
    activeController.abort()
    activeController = undefined
    pending.value = false
  }
  if (!discoveryReady.value && !discoveryFailed.value) return
  discoveryReady.value = false
  discoveryFailed.value = false
  manualMode.value = false
  draft.models = []
  if (draft.step > 1) draft.step = 1
}

watch(
  [
    () => draft.upstream_url,
    () => draft.keys,
    () => draft.protocols.join('\u0000'),
    () => JSON.stringify(draft.header_rules),
  ],
  invalidateDiscovery,
)

function applyPreset(event: Event): void {
  const id = (event.target as HTMLSelectElement).value as ChannelPreset['id']
  const preset = channelPresets.find((item) => item.id === id)!
  draft.preset_id = id
  draft.upstream_url = preset.upstream_url
  draft.protocols = [...preset.protocols]
}

function toggleProtocol(protocol: GroupProtocol, checked: boolean): void {
  draft.protocols = checked
    ? [...new Set([...draft.protocols, protocol])]
    : draft.protocols.filter((item) => item !== protocol)
}

async function runDiscovery(): Promise<void> {
  if (!canDiscover.value) return
  const controller = startAction()
  conflict.value = null
  try {
    const result = await discoverModels(
      api,
      {
        upstream_url: draft.upstream_url,
        protocols: [...draft.protocols],
        keys: draft.keys,
        config: { header_rules: snapshotDraft().header_rules },
      },
      controller.signal,
    )
    if (activeController !== controller) return
    draft.models = createModelDraft(result.models)
    discoveryReady.value = true
    discoveryFailed.value = false
    manualMode.value = true
    draft.step = 2
  } catch (error: unknown) {
    if (activeController !== controller || error instanceof RequestCancelledError) return
    discoveryFailed.value = true
    manualMode.value = false
    draft.step = 2
    errorKey.value = 'import.discoveryFailed'
  } finally {
    finishAction(controller)
  }
}

function showManualPath(): void {
  manualMode.value = true
  errorKey.value = ''
}

function buildCreateBody(confirmSameURL: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    ...(name ? { name } : {}),
    upstream_url: draft.upstream_url,
    protocols: [...draft.protocols],
    models: toGroupModels(draft.models),
    config: { header_rules: snapshotDraft().header_rules },
    keys: draft.keys,
    confirm_same_upstream_url: confirmSameURL,
  }
}

async function finishSuccess(controller: AbortController, groupID: number): Promise<void> {
  if (activeController !== controller) return
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.list() }),
    queryClient.invalidateQueries({ queryKey: controlQueryKeys.health() }),
  ])
  if (activeController !== controller) return
  completed.value = true
  draft.keys = ''
  recovery.clear()
  await router.push({ name: 'group-detail', params: { id: groupID } })
}

async function submitCreate(confirmSameURL = false): Promise<void> {
  if (pending.value || toGroupModels(draft.models).length === 0) return
  const controller = startAction()
  if (!confirmSameURL) conflict.value = null
  try {
    const result = await createGroup(api, buildCreateBody(confirmSameURL), controller.signal)
    if (activeController !== controller) return
    await finishSuccess(controller, result.group_id)
  } catch (error: unknown) {
    if (activeController !== controller || error instanceof RequestCancelledError) return
    if (
      error instanceof ApiError &&
      error.code === 'UPSTREAM_URL_CONFLICT' &&
      isUpstreamUrlConflictData(error.data)
    ) {
      conflict.value = error.data
      return
    }
    errorKey.value = 'import.createFailed'
  } finally {
    finishAction(controller)
  }
}

async function appendToGroup(groupID: number): Promise<void> {
  if (pending.value) return
  const controller = startAction()
  try {
    await importGroupKeys(api, groupID, { keys: draft.keys }, controller.signal)
    if (activeController !== controller) return
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.keys(groupID) }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.detail(groupID) }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.list() }),
      queryClient.invalidateQueries({ queryKey: controlQueryKeys.health() }),
    ])
    if (activeController !== controller) return
    completed.value = true
    draft.keys = ''
    recovery.clear()
    await router.push({ name: 'group-detail', params: { id: groupID } })
  } catch (error: unknown) {
    if (activeController === controller && !(error instanceof RequestCancelledError))
      errorKey.value = 'import.appendFailed'
  } finally {
    finishAction(controller)
  }
}

function returnToEdit(): void {
  conflict.value = null
  draft.step = 1
}

onBeforeUnmount(() => {
  activeController?.abort()
  activeController = undefined
  unregisterRecovery()
})
</script>

<template>
  <div class="import-workflow">
    <ol class="stepper" :aria-label="t('import.progress')">
      <li
        v-for="number in [1, 2, 3]"
        :key="number"
        :class="{ active: draft.step === number, done: draft.step > number }"
      >
        <span
          ><Check v-if="draft.step > number" :size="14" aria-hidden="true" /><template v-else>{{
            number
          }}</template></span
        >
        {{ t(`import.steps.${number}`) }}
      </li>
    </ol>

    <SurfaceCard v-if="draft.step === 1" class="import-card">
      <header>
        <h2>{{ t('import.connection.title') }}</h2>
        <p>{{ t('import.connection.description') }}</p>
      </header>
      <div class="connection-grid">
        <label
          ><span>{{ t('import.connection.preset') }}</span
          ><select data-test="preset" :value="draft.preset_id" @change="applyPreset">
            <option v-for="preset in channelPresets" :key="preset.id" :value="preset.id">
              {{ t(preset.labelKey) }}
            </option>
          </select></label
        >
        <label
          ><span>{{ t('import.connection.name') }}</span
          ><input v-model="draft.name" data-test="group-name" autocomplete="off"
        /></label>
        <label class="wide"
          ><span>{{ t('import.connection.url') }}</span
          ><input
            v-model="draft.upstream_url"
            data-test="upstream-url"
            type="url"
            autocomplete="off"
            spellcheck="false"
        /></label>
      </div>
      <fieldset>
        <legend>{{ t('import.connection.protocols') }}</legend>
        <label v-for="protocol in protocols" :key="protocol" class="protocol-option"
          ><input
            :data-test="`protocol-${protocol}`"
            type="checkbox"
            :checked="draft.protocols.includes(protocol)"
            @change="toggleProtocol(protocol, ($event.target as HTMLInputElement).checked)"
          />{{ protocol }}</label
        >
      </fieldset>
      <KeyTextarea v-model="draft.keys" :disabled="pending" />
      <details
        class="advanced"
        :open="
          Object.keys(draft.header_rules.set).length > 0 || draft.header_rules.remove.length > 0
        "
      >
        <summary>{{ t('import.connection.advanced') }}</summary>
        <HeaderRulesEditor v-model="draft.header_rules" />
      </details>
      <footer class="card-actions">
        <AppButton
          data-test="discover"
          :disabled="!canDiscover"
          :busy="pending"
          @click="runDiscovery"
          ><Search :size="16" aria-hidden="true" />{{ t('import.discover') }}</AppButton
        >
      </footer>
    </SurfaceCard>

    <SurfaceCard v-else-if="draft.step === 2" class="import-card">
      <header>
        <h2>{{ t('import.models.title') }}</h2>
        <p>{{ t('import.models.stepDescription') }}</p>
      </header>
      <InlineFeedback v-if="errorKey" tone="warning">{{ t(errorKey) }}</InlineFeedback>
      <button
        v-if="discoveryFailed && !manualMode"
        data-test="manual-path"
        class="manual-path"
        type="button"
        @click="showManualPath"
      >
        {{ t('import.models.manualPath') }}
      </button>
      <ModelDraftEditor v-if="manualMode" v-model="draft.models" />
      <footer class="card-actions split">
        <AppButton variant="secondary" @click="draft.step = 1"
          ><ChevronLeft :size="16" aria-hidden="true" />{{ t('import.back') }}</AppButton
        ><AppButton data-test="review" :disabled="!canReview" @click="draft.step = 3"
          >{{ t('import.review') }}<ChevronRight :size="16" aria-hidden="true"
        /></AppButton>
      </footer>
    </SurfaceCard>

    <SurfaceCard v-else class="import-card">
      <header>
        <h2>{{ t('import.reviewTitle') }}</h2>
        <p>{{ t('import.reviewDescription') }}</p>
      </header>
      <dl class="review-list">
        <div>
          <dt>{{ t('import.connection.name') }}</dt>
          <dd>{{ draft.name || t('import.automaticName') }}</dd>
        </div>
        <div>
          <dt>{{ t('import.connection.url') }}</dt>
          <dd>
            <code>{{ draft.upstream_url }}</code>
          </dd>
        </div>
        <div>
          <dt>{{ t('import.connection.protocols') }}</dt>
          <dd>{{ draft.protocols.join(', ') }}</dd>
        </div>
        <div>
          <dt>{{ t('import.keys.label') }}</dt>
          <dd>{{ t('import.keys.count', { count: keyAnalysis.nonEmptyCount }) }}</dd>
        </div>
        <div>
          <dt>{{ t('import.models.title') }}</dt>
          <dd>
            {{
              toGroupModels(draft.models)
                .map((model) => model.id)
                .join(', ')
            }}
          </dd>
        </div>
      </dl>
      <InlineFeedback v-if="errorKey" tone="danger">{{ t(errorKey) }}</InlineFeedback>
      <section v-if="conflict" class="conflict" aria-live="polite">
        <h3><TriangleAlert :size="18" aria-hidden="true" />{{ t('import.conflict.title') }}</h3>
        <p>{{ t('import.conflict.description') }}</p>
        <div v-for="group in conflict.groups" :key="group.id" class="conflict-group">
          <strong>{{ group.name }}</strong
          ><AppButton
            :data-test="`conflict-append-${group.id}`"
            variant="secondary"
            @click="appendToGroup(group.id)"
            >{{ t('import.conflict.append') }}</AppButton
          >
        </div>
        <div class="conflict-actions">
          <AppButton data-test="conflict-confirm-separate" @click="submitCreate(true)">{{
            t('import.conflict.separate')
          }}</AppButton
          ><AppButton
            data-test="conflict-edit"
            variant="ghost"
            :disabled="pending"
            @click="returnToEdit"
            >{{ t('import.conflict.edit') }}</AppButton
          >
        </div>
      </section>
      <footer v-else class="card-actions split">
        <AppButton variant="secondary" :disabled="pending" @click="draft.step = 2"
          ><ChevronLeft :size="16" aria-hidden="true" />{{ t('import.back') }}</AppButton
        ><AppButton data-test="create" :busy="pending" @click="submitCreate(false)">{{
          t('import.create')
        }}</AppButton>
      </footer>
    </SurfaceCard>
  </div>
</template>

<style scoped>
.import-workflow {
  display: grid;
  gap: var(--space-5);
  max-width: 920px;
  margin: 0 auto;
}
.stepper {
  display: flex;
  margin: 0;
  padding: 0;
  list-style: none;
}
.stepper li {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.stepper li:not(:last-child)::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--color-border);
  margin-inline: var(--space-3);
}
.stepper li > span {
  display: grid;
  width: 26px;
  height: 26px;
  flex: none;
  place-items: center;
  border: 1px solid var(--color-border);
  border-radius: 50%;
  font-family: ui-monospace, monospace;
}
.stepper li.active {
  color: var(--color-text);
  font-weight: 650;
}
.stepper li.active > span {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: var(--color-primary-ink);
}
.stepper li.done > span {
  border-color: transparent;
  background: var(--color-success-bg);
  color: var(--color-success);
}
.import-card {
  display: grid;
  gap: var(--space-5);
  padding: var(--space-6);
}
header h2,
header p {
  margin: 0;
}
header h2 {
  font-size: 1.2rem;
}
header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.connection-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-4);
}
.connection-grid label {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
.connection-grid .wide {
  grid-column: 1 / -1;
}
.connection-grid input,
.connection-grid select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.wide input {
  font-family: ui-monospace, monospace;
}
fieldset {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  border: 0;
  margin: 0;
  padding: 0;
}
legend {
  width: 100%;
  margin-bottom: var(--space-2);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
.protocol-option {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-tag);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
}
.advanced {
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
}
.advanced summary {
  min-height: 44px;
  cursor: pointer;
  color: var(--color-text-muted);
  font-weight: 650;
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
.manual-path {
  min-height: 44px;
  border: 1px solid var(--color-primary);
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  padding: var(--space-2) var(--space-4);
  font-weight: 650;
  cursor: pointer;
}
.review-list {
  display: grid;
  gap: 0;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
}
.review-list div {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}
.review-list div:last-child {
  border-bottom: 0;
}
dt {
  color: var(--color-text-muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.conflict {
  display: grid;
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-warning) 38%, var(--color-border));
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  padding: var(--space-4);
}
.conflict h3,
.conflict p {
  margin: 0;
}
.conflict h3 {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-warning);
}
.conflict-group,
.conflict-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.conflict-actions {
  justify-content: flex-start;
  flex-wrap: wrap;
}
@media (max-width: 640px) {
  .stepper li {
    font-size: 0;
  }
  .connection-grid {
    grid-template-columns: 1fr;
  }
  .connection-grid .wide {
    grid-column: auto;
  }
  .review-list div {
    grid-template-columns: 1fr;
    gap: var(--space-1);
  }
  .conflict-group {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
