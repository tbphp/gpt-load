<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import GroupDraftGuard from './GroupDraftGuard.vue'
import {
  updateGroupBasics,
  type GroupBasics,
  type GroupBasicsPatch,
  type GroupRow,
} from '@modern/api/groups'
import {
  AppButton,
  AppCollectionState,
  AppNotice,
  AppSwitch,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { getGroupSettings, groupSettingsKey, type GroupSettings } from '@modern/api/group-detail'

const props = defineProps<{ group: GroupRow }>()
const emit = defineEmits<{
  saved: [id: number, settings: GroupBasics]
  pending: [value: boolean]
  updatedAt: [value: number]
}>()
const client = useApiClient()
const cache = useQueryClient()
const { t } = useI18n()
const saved = ref<GroupBasics>()
const name = ref('')
const weight = ref('50')
const price = ref('1')
const enabled = ref(true)
const saving = ref(false)
const saveFailed = ref(false)
const attempted = ref(false)
const savedFeedback = ref(false)
const nameInput = ref<InstanceType<typeof AppTextField>>()
const weightInput = ref<InstanceType<typeof AppTextField>>()
const priceInput = ref<InstanceType<typeof AppTextField>>()
const controller = new AbortController()
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

function accept(data: GroupBasics): void {
  saved.value = data
  name.value = data.name
  weight.value = String(data.weight ?? 50)
  price.value = data.priceMultiplier
  enabled.value = data.enabled
  attempted.value = false
}
const query = useQuery(
  computed(() => ({
    queryKey: groupSettingsKey(props.group.id),
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getGroupSettings(client, props.group.id, signal),
  })),
)
const loading = computed(() => query.isPending.value)
const loadFailed = computed(() => query.isError.value)
watch(
  query.data,
  (data) => {
    if (data && !dirty.value && !saving.value) accept(data)
  },
  { immediate: true },
)
watch(
  () => query.isFetching.value || saving.value,
  (value) => emit('pending', value),
  { immediate: true },
)
watch(
  query.dataUpdatedAt,
  (value) => {
    if (value) emit('updatedAt', value)
  },
  { immediate: true },
)
watch([name, weight, price, enabled], () => {
  savedFeedback.value = false
})
async function load(): Promise<void> {
  await query.refetch()
}
function discard(): void {
  if (query.data.value) accept(query.data.value)
  saveFailed.value = false
}
onScopeDispose(() => controller.abort())
defineExpose({ refresh: load })

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
    accept(saved.value)
    return
  }
  saving.value = true
  const signal = controller.signal
  try {
    await cache.cancelQueries({ queryKey: groupSettingsKey(props.group.id) })
    const result = await updateGroupBasics(client, props.group.id, patch, signal)
    if (signal.aborted) return
    accept(result)
    cache.setQueryData<GroupSettings>(
      groupSettingsKey(props.group.id),
      (previous) => previous && { ...previous, ...result },
    )
    emit('saved', props.group.id, result)
    await nextTick()
    savedFeedback.value = true
  } catch {
    if (!signal.aborted) saveFailed.value = true
  } finally {
    if (!signal.aborted) saving.value = false
  }
}
</script>

<template>
  <div class="modern-group-settings-form">
    <AppCollectionState v-if="loading" :title="t('collection.loading')" loading />
    <AppCollectionState v-else-if="loadFailed && !saved" :title="t('groups.edit.loadFailed')" error
      ><AppButton @click="load">{{ t('collection.retry') }}</AppButton></AppCollectionState
    >
    <form v-else novalidate @submit.prevent="save">
      <div class="modern-group-settings-fields">
        <slot name="overview" />
        <header class="modern-group-settings-heading">
          <h2>{{ t('groupDetail.scheduling') }}</h2>
        </header>
        <AppTextField
          ref="nameInput"
          v-model="name"
          :label="t('groups.edit.name')"
          size="sm"
          autocomplete="off"
          :disabled="saving"
          :error="attempted && nameInvalid ? t('groups.edit.nameError') : undefined"
        />
        <div class="modern-group-settings-columns">
          <AppTextField
            ref="weightInput"
            v-model="weight"
            :label="t('groups.edit.weight')"
            size="sm"
            inputmode="numeric"
            :disabled="saving"
            :error="attempted && weightInvalid ? t('groups.edit.weightError') : undefined"
          />
          <AppTextField
            ref="priceInput"
            v-model="price"
            :label="t('groups.edit.price')"
            size="sm"
            inputmode="decimal"
            :disabled="saving"
            :error="attempted && priceInvalid ? t('groups.edit.priceError') : undefined"
          />
        </div>
        <div class="modern-group-settings-enabled">
          <span>{{ t('groups.edit.enabled') }}</span
          ><AppSwitch v-model="enabled" :label="t('groups.edit.enabled')" :disabled="saving" />
        </div>
        <slot />
        <AppNotice v-if="saveFailed" tone="danger">{{ t('groups.edit.saveFailed') }}</AppNotice>
        <AppNotice v-else-if="loadFailed" tone="warning">{{
          t('groupDetail.refreshFailed')
        }}</AppNotice>
      </div>
      <footer class="modern-group-settings-footer">
        <span class="modern-group-settings-feedback" role="status">{{
          savedFeedback ? t('groupDetail.saved') : dirty ? t('credentialCards.unsaved') : ''
        }}</span>
        <AppButton v-if="dirty" size="sm" variant="ghost" :disabled="saving" @click="discard">{{
          t('groupDetail.revert')
        }}</AppButton>
        <AppButton
          type="submit"
          variant="outline"
          size="sm"
          :loading="saving"
          :disabled="!dirty || saving"
          >{{ t('groups.edit.save') }}</AppButton
        >
      </footer>
    </form>
  </div>
  <GroupDraftGuard :dirty="dirty" :pending="saving" />
</template>

<style scoped>
.modern-group-settings-form,
.modern-group-settings-form > form {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}
.modern-group-settings-fields {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--modern-space-3);
  overflow-y: auto;
  padding: var(--modern-space-5) var(--modern-space-4);
  overscroll-behavior: contain;
}
.modern-group-settings-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--modern-space-1);
  gap: var(--modern-space-3);
}
.modern-group-settings-heading h2 {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-settings-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-group-settings-footer {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  flex: none;
  padding: var(--modern-space-3) var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-settings-feedback {
  flex: 1;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-settings-enabled {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@container modern-group-workspace (max-width: 980px) {
  .modern-group-settings-fields {
    overflow: visible;
  }
}
</style>
