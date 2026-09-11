<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import {
  getGroupBasics,
  updateGroupBasics,
  type GroupBasics,
  type GroupBasicsPatch,
  type GroupRow,
} from '@modern/api/groups'
import {
  AppButton,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppNotice,
  AppSwitch,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{ group: GroupRow }>()
const emit = defineEmits<{ close: []; saved: [id: number, settings: GroupBasics] }>()
const client = useApiClient()
const { t } = useI18n()
const saved = ref<GroupBasics>()
const name = ref('')
const weight = ref('50')
const price = ref('1')
const enabled = ref(true)
const loading = ref(true)
const loadFailed = ref(false)
const saving = ref(false)
const saveFailed = ref(false)
const attempted = ref(false)
const discardRequested = ref(false)
const nameInput = ref<InstanceType<typeof AppTextField>>()
const weightInput = ref<InstanceType<typeof AppTextField>>()
const priceInput = ref<InstanceType<typeof AppTextField>>()
let controller = new AbortController()
const nameInvalid = computed(
  () =>
    !name.value.trim() ||
    new TextEncoder().encode(name.value.trim()).length > 255 ||
    /\p{Cc}/u.test(name.value.trim()),
)
const weightInvalid = computed(
  () =>
    !/^\d+$/u.test(weight.value) ||
    Number(weight.value) > 100 ||
    // 历史零权重可保持原值；新设置仍要求 1–100。
    (Number(weight.value) < 1 && Number(weight.value) !== saved.value?.weight),
)
const priceInvalid = computed(
  () => !/^\d+(?:\.\d{1,6})?$/u.test(price.value.trim()) || Number(price.value) > 1000,
)
const dirty = computed(
  () =>
    saved.value !== undefined &&
    (name.value !== saved.value.name ||
      weight.value !== String(saved.value.weight ?? 50) ||
      price.value !== saved.value.priceMultiplier ||
      enabled.value !== saved.value.enabled),
)

async function load(): Promise<void> {
  controller.abort()
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  loadFailed.value = false
  try {
    const data = await getGroupBasics(client, props.group.id, signal)
    if (signal.aborted) return
    saved.value = data
    name.value = data.name
    weight.value = String(data.weight ?? 50)
    price.value = data.priceMultiplier
    enabled.value = data.enabled
  } catch {
    if (!signal.aborted) loadFailed.value = true
  } finally {
    if (!signal.aborted) {
      loading.value = false
      await nextTick()
      nameInput.value?.focus()
    }
  }
}
function close(): void {
  if (saving.value) return
  if (dirty.value) discardRequested.value = true
  else emit('close')
}
function preventUnload(event: BeforeUnloadEvent): void {
  if (!dirty.value && !saving.value) return
  event.preventDefault()
  event.returnValue = ''
}
onBeforeRouteLeave(() => {
  if (saving.value) return false
  if (!dirty.value) return true
  discardRequested.value = true
  return false
})
onMounted(() => window.addEventListener('beforeunload', preventUnload))
onScopeDispose(() => {
  controller.abort()
  window.removeEventListener('beforeunload', preventUnload)
})
watch(
  () => props.group.id,
  () => {
    void load()
  },
  { immediate: true },
)

async function save(): Promise<void> {
  if (!saved.value || saving.value || loading.value) return
  attempted.value = true
  saveFailed.value = false
  if (nameInvalid.value || weightInvalid.value || priceInvalid.value) {
    await nextTick()
    const field = nameInvalid.value ? nameInput : weightInvalid.value ? weightInput : priceInput
    field.value?.focus()
    return
  }
  const patch: GroupBasicsPatch = {}
  if (name.value.trim() !== saved.value.name) patch.name = name.value.trim()
  if (Number(weight.value) !== (saved.value.weight ?? 50))
    patch.weight_manual = Number(weight.value)
  if (Number(price.value) !== Number(saved.value.priceMultiplier))
    patch.price_multiplier = price.value.trim()
  if (enabled.value !== saved.value.enabled) patch.enabled = enabled.value
  if (!Object.keys(patch).length) {
    emit('close')
    return
  }
  saving.value = true
  const signal = controller.signal
  try {
    const result = await updateGroupBasics(client, props.group.id, patch, signal)
    if (signal.aborted) return
    saved.value = result
    emit('saved', props.group.id, result)
    emit('close')
  } catch {
    if (!signal.aborted) saveFailed.value = true
  } finally {
    if (!signal.aborted) saving.value = false
  }
}
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (open) => {
        if (!open) close()
      }
    "
  >
    <AppDialogContent placement="editor" :title="t('groups.edit.title')" :description="group.name">
      <AppDialogHeader
        :title="t('groups.edit.title')"
        :description="`#${group.id} · ${group.name}`"
        :close-label="t('shell.close')"
        :close-disabled="saving"
        @close="close"
      />
      <AppCollectionState v-if="loading" :title="t('collection.loading')" loading />
      <AppCollectionState v-else-if="loadFailed" :title="t('groups.edit.loadFailed')" error>
        <AppButton @click="load">{{ t('collection.retry') }}</AppButton>
      </AppCollectionState>
      <form v-else class="modern-group-editor-form" novalidate @submit.prevent="save">
        <div class="modern-group-editor-fields">
          <AppTextField
            ref="nameInput"
            v-model="name"
            :label="t('groups.edit.name')"
            name="group-name"
            autocomplete="off"
            :disabled="saving"
            :error="attempted && nameInvalid ? t('groups.edit.nameError') : undefined"
          />
          <div class="modern-group-editor-columns">
            <AppTextField
              ref="weightInput"
              v-model="weight"
              :label="t('groups.edit.weight')"
              inputmode="numeric"
              :disabled="saving"
              :error="attempted && weightInvalid ? t('groups.edit.weightError') : undefined"
            />
            <AppTextField
              ref="priceInput"
              v-model="price"
              :label="t('groups.edit.price')"
              inputmode="decimal"
              :disabled="saving"
              :error="attempted && priceInvalid ? t('groups.edit.priceError') : undefined"
            />
          </div>
          <p class="modern-group-editor-hint">{{ t('groups.edit.weightHelp') }}</p>
          <p class="modern-group-editor-hint">{{ t('groups.edit.priceHelp') }}</p>
          <div class="modern-group-editor-enabled">
            <span>{{ t('groups.edit.enabled') }}</span
            ><AppSwitch v-model="enabled" :label="t('groups.edit.enabled')" :disabled="saving" />
          </div>
          <AppNotice v-if="saveFailed" tone="danger">{{ t('groups.edit.saveFailed') }}</AppNotice>
        </div>
        <footer class="modern-group-editor-footer">
          <template v-if="discardRequested">
            <AppNotice tone="warning">{{ t('groups.edit.unsaved') }}</AppNotice>
            <div class="modern-group-editor-actions">
              <AppButton @click="discardRequested = false">{{
                t('groups.edit.keepEditing')
              }}</AppButton>
              <AppButton variant="danger" @click="emit('close')">{{
                t('groups.edit.discard')
              }}</AppButton>
            </div>
          </template>
          <div v-else class="modern-group-editor-actions">
            <AppButton :disabled="saving" @click="close">{{ t('groups.edit.cancel') }}</AppButton>
            <AppButton
              type="submit"
              variant="primary"
              :loading="saving"
              :disabled="!dirty || saving"
              >{{ t('groups.edit.save') }}</AppButton
            >
          </div>
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-group-editor-form {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-group-editor-fields {
  display: grid;
  gap: var(--modern-space-5);
  padding: var(--modern-space-6);
  overflow-y: auto;
  overscroll-behavior: contain;
}
.modern-group-editor-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-4);
}
.modern-group-editor-hint {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-editor-enabled {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-top: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
  font-size: var(--modern-font-size-secondary);
}
.modern-group-editor-footer {
  display: grid;
  gap: var(--modern-space-4);
  margin-top: auto;
  padding: var(--modern-space-4) var(--modern-space-6);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
}
@media (max-width: 420px) {
  .modern-group-editor-columns {
    grid-template-columns: 1fr;
  }
}
</style>
