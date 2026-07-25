<script setup lang="ts">
import { KeyRound, Plus, Save, X } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'

import { useApiClient } from '@/api/client-context'
import { createAccessKey, updateAccessKey } from '@/api/control/access-keys'
import type { AccessKeyDto, GroupSummary, Protocol } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SecretValue from '@/components/ui/SecretValue.vue'

import { accessKeyProtocols, buildAccessKeyModelOptions } from './access-key-options'
import {
  buildAccessKeyUpdatePatch,
  buildCreateAccessKeyInput,
  createAccessKeyDraft,
  isAccessKeyDraftValid,
  type AccessKeyDraft,
} from './access-key-patch'

const props = defineProps<{
  open: boolean
  accessKey: AccessKeyDto | null
  groups: GroupSummary[]
}>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const nameInput = ref<HTMLInputElement>()
const base = ref<AccessKeyDto | null>(null)
const draft = ref<AccessKeyDraft>(createAccessKeyDraft())
const result = ref<AccessKeyDto | null>(null)
const pending = ref(false)
const failed = ref(false)
const modelInput = ref('')
let controller: AbortController | undefined

const editing = computed(() => base.value !== null)
const modelOptions = computed(() =>
  buildAccessKeyModelOptions(props.groups, draft.value.filters.models),
)
const patch = computed(() =>
  base.value
    ? buildAccessKeyUpdatePatch(base.value, draft.value)
    : buildCreateAccessKeyInput(draft.value),
)
const dirty = computed(() => !base.value || Object.keys(patch.value).length > 0)
const valid = computed(() => isAccessKeyDraftValid(draft.value))

function clearLocalState(): void {
  controller?.abort()
  controller = undefined
  base.value = null
  draft.value = createAccessKeyDraft()
  result.value = null
  pending.value = false
  failed.value = false
  modelInput.value = ''
}

async function resetForOpen(): Promise<void> {
  base.value = props.accessKey
  draft.value = createAccessKeyDraft(props.accessKey)
  result.value = null
  failed.value = false
  modelInput.value = ''
  await nextTick()
  await nextTick()
  nameInput.value?.focus()
}

function setOpen(open: boolean): void {
  if (!open) clearLocalState()
  emit('update:open', open)
}

watch(
  () => [props.open, props.accessKey] as const,
  ([open]) => {
    if (open) void resetForOpen()
    else clearLocalState()
  },
  { immediate: true },
)

function toggleGroup(groupId: number, checked: boolean): void {
  draft.value.filters.groups = checked
    ? [...new Set([...draft.value.filters.groups, groupId])]
    : draft.value.filters.groups.filter((id) => id !== groupId)
}

function toggleProtocol(protocol: Protocol, checked: boolean): void {
  draft.value.filters.protocols = checked
    ? [...new Set([...draft.value.filters.protocols, protocol])]
    : draft.value.filters.protocols.filter((value) => value !== protocol)
}

function addModel(): void {
  const model = modelInput.value.trim()
  if (!model || draft.value.filters.models.includes(model)) return
  draft.value.filters.models = [...draft.value.filters.models, model]
  modelInput.value = ''
}

function removeModel(model: string): void {
  draft.value.filters.models = draft.value.filters.models.filter((value) => value !== model)
}

function setRPM(event: Event): void {
  draft.value.rpm_limit = Number((event.target as HTMLInputElement).value)
}

async function save(): Promise<void> {
  if (pending.value || !valid.value || !dirty.value) return
  const currentBase = base.value
  const updateBody = currentBase ? buildAccessKeyUpdatePatch(currentBase, draft.value) : null
  if (updateBody && Object.keys(updateBody).length === 0) return

  pending.value = true
  failed.value = false
  result.value = null
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const saved = currentBase
      ? await updateAccessKey(client, currentBase.id, updateBody!, activeController.signal)
      : await createAccessKey(
          client,
          buildCreateAccessKeyInput(draft.value),
          activeController.signal,
        )
    if (controller !== activeController || !props.open) return
    result.value = saved
    if (currentBase) {
      base.value = saved
      draft.value = createAccessKeyDraft(saved)
    }
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.accessKeys.list() })
  } catch (error: unknown) {
    if (!(error instanceof RequestCancelledError)) failed.value = true
  } finally {
    if (controller === activeController) controller = undefined
    pending.value = false
  }
}

onBeforeUnmount(clearLocalState)
</script>

<template>
  <AppDrawer
    :open="open"
    :title="editing ? t('accessKeys.drawer.editTitle') : t('accessKeys.drawer.createTitle')"
    :description="t('accessKeys.drawer.description')"
    :close-label="t('accessKeys.drawer.close')"
    @update:open="setOpen"
  >
    <template #trigger><slot name="trigger" /></template>

    <form class="access-key-drawer" @submit.prevent="save">
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.drawer.saveFailed')
      }}</InlineFeedback>

      <label class="access-key-drawer__field" for="access-key-name">
        <span>{{ t('accessKeys.drawer.name') }}</span>
        <input
          id="access-key-name"
          ref="nameInput"
          v-model="draft.name"
          data-test="access-key-name"
          type="text"
          autocomplete="off"
          :disabled="pending"
        />
      </label>

      <label v-if="editing" class="access-key-drawer__field" for="access-key-status">
        <span>{{ t('accessKeys.drawer.status') }}</span>
        <select
          id="access-key-status"
          v-model="draft.status"
          data-test="access-key-status"
          :disabled="pending"
        >
          <option value="active">{{ t('accessKeys.status.active') }}</option>
          <option value="disabled">{{ t('accessKeys.status.disabled') }}</option>
        </select>
      </label>

      <fieldset>
        <legend>{{ t('accessKeys.drawer.groups') }}</legend>
        <p>{{ t('accessKeys.drawer.emptyMeansAll') }}</p>
        <label v-for="group in groups" :key="group.id" class="access-key-drawer__check">
          <input
            type="checkbox"
            :checked="draft.filters.groups.includes(group.id)"
            :disabled="pending"
            @change="toggleGroup(group.id, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ group.name }}</span>
        </label>
      </fieldset>

      <fieldset>
        <legend>{{ t('accessKeys.drawer.protocols') }}</legend>
        <p>{{ t('accessKeys.drawer.emptyMeansAll') }}</p>
        <label
          v-for="protocol in accessKeyProtocols"
          :key="protocol"
          class="access-key-drawer__check"
        >
          <input
            type="checkbox"
            :checked="draft.filters.protocols.includes(protocol)"
            :disabled="pending"
            @change="toggleProtocol(protocol, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ t(`group.protocols.${protocol}`) }}</span>
        </label>
      </fieldset>

      <fieldset>
        <legend>{{ t('accessKeys.drawer.models') }}</legend>
        <p>{{ t('accessKeys.drawer.modelsDescription') }}</p>
        <div class="access-key-drawer__model-entry">
          <input
            v-model="modelInput"
            data-test="access-key-model-input"
            type="text"
            list="access-key-model-options"
            autocomplete="off"
            :placeholder="t('accessKeys.drawer.modelPlaceholder')"
            :disabled="pending"
            @keydown.enter.prevent="addModel"
          />
          <datalist id="access-key-model-options">
            <option v-for="model in modelOptions" :key="model" :value="model" />
          </datalist>
          <AppButton
            variant="secondary"
            :disabled="pending || !modelInput.trim()"
            @click="addModel"
          >
            <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.drawer.addModel') }}
          </AppButton>
        </div>
        <div v-if="draft.filters.models.length" class="access-key-drawer__models">
          <span v-for="model in draft.filters.models" :key="model" class="access-key-drawer__model">
            <code>{{ model }}</code>
            <button
              type="button"
              :aria-label="t('accessKeys.drawer.removeModel', { model })"
              :disabled="pending"
              @click="removeModel(model)"
            >
              <X :size="15" aria-hidden="true" />
            </button>
          </span>
        </div>
      </fieldset>

      <label class="access-key-drawer__field" for="access-key-rpm">
        <span>{{ t('accessKeys.drawer.rpm') }}</span>
        <input
          id="access-key-rpm"
          data-test="access-key-rpm"
          type="number"
          min="0"
          step="1"
          :value="draft.rpm_limit"
          :disabled="pending"
          @input="setRPM"
        />
        <small>{{ t('accessKeys.drawer.rpmDescription') }}</small>
      </label>

      <section v-if="result" class="access-key-drawer__result" aria-live="polite">
        <div class="access-key-drawer__result-title">
          <KeyRound :size="18" aria-hidden="true" />
          <strong>{{ t('accessKeys.drawer.resultTitle') }}</strong>
        </div>
        <p>{{ t('accessKeys.drawer.resultDescription') }}</p>
        <div class="access-key-drawer__secret">
          <SecretValue
            :value="result.key"
            :reveal-label="t('common.reveal')"
            :conceal-label="t('common.conceal')"
            button-test="access-key-result-reveal"
          />
          <CopyButton
            :value="result.key"
            :label="t('accessKeys.copy')"
            :success-label="t('common.copied')"
            :failure-label="t('common.copyFailed')"
          />
        </div>
      </section>

      <div class="access-key-drawer__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          data-test="access-key-save"
          type="submit"
          :busy="pending"
          :disabled="!valid || !dirty"
        >
          <Save :size="16" aria-hidden="true" />{{ t('accessKeys.drawer.save') }}
        </AppButton>
      </div>
    </form>
  </AppDrawer>
</template>

<style scoped>
.access-key-drawer {
  display: grid;
  gap: var(--space-5);
  font-size: 1rem;
}
.access-key-drawer__field,
fieldset {
  display: grid;
  gap: var(--space-2);
}
.access-key-drawer__field > span,
legend {
  font-weight: 700;
}
input,
select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
fieldset {
  min-width: 0;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
fieldset p,
small,
.access-key-drawer__result p {
  margin: 0;
  color: var(--color-text-muted);
}
.access-key-drawer__check {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__check input {
  width: 20px;
  min-height: 20px;
}
.access-key-drawer__model-entry,
.access-key-drawer__secret,
.access-key-drawer__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__model-entry input {
  flex: 1 1 220px;
}
.access-key-drawer__models {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.access-key-drawer__model {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding-left: var(--space-2);
}
.access-key-drawer__model button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.access-key-drawer__result {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-success);
  border-radius: var(--radius-control);
  background: var(--color-success-bg);
  padding: var(--space-3);
}
.access-key-drawer__result-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-success);
}
.access-key-drawer__actions {
  justify-content: flex-end;
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}
</style>
