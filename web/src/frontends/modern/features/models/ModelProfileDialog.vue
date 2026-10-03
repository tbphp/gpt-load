<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import {
  AppButton,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppNotice,
} from '@modern/components/ui'
import { useModelProfileEditor } from './use-model-profile-editor'
import ModelProfileForm from './ModelProfileForm.vue'

const props = defineProps<{ model: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const { query, base, draft, dirty, customCount, saving, saveError, fieldErrors, resetAll, save } =
  useModelProfileEditor(() => props.model)
async function close(): Promise<void> {
  if (!saving.value && (await guard.value?.confirm())) emit('close')
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      placement="editor"
      size="sheet"
      :title="t('modelManager.profile.title')"
      :description="model"
      @escape-key-down="saving && $event.preventDefault()"
      @interact-outside="saving && $event.preventDefault()"
    >
      <AppDialogHeader
        :title="t('modelManager.profile.title')"
        :description="model"
        :close-label="t('ui.close')"
        :close-disabled="saving"
        @close="close"
      />
      <AppCollectionState
        v-if="!base"
        :loading="query.isPending.value"
        :error="query.isError.value"
        :title="
          t(query.isPending.value ? 'modelManager.profile.loading' : 'modelManager.profile.failed')
        "
      >
        <AppButton v-if="query.isError.value" @click="query.refetch()">{{
          t('ui.retry')
        }}</AppButton>
      </AppCollectionState>
      <form v-else-if="draft" class="modern-model-profile-form" novalidate @submit.prevent="save">
        <div class="modern-model-profile-body">
          <AppNotice v-if="query.isError.value" tone="warning">{{
            t('modelManager.profile.stale')
          }}</AppNotice>
          <AppNotice v-if="saveError" tone="danger">{{ saveError }}</AppNotice>
          <ModelProfileForm
            v-model="draft"
            :profile="base"
            :disabled="saving"
            :errors="fieldErrors"
          />
        </div>
        <footer class="modern-model-profile-footer">
          <AppButton
            variant="text"
            size="sm"
            :icon="RotateCcw"
            :disabled="saving || !customCount"
            @click="resetAll"
            >{{ t('modelManager.profile.resetAll') }}</AppButton
          >
          <div class="modern-model-profile-actions">
            <AppButton size="sm" :disabled="saving" @click="close">{{ t('ui.cancel') }}</AppButton>
            <AppButton
              type="submit"
              variant="primary"
              size="sm"
              :loading="saving"
              :disabled="saving || !dirty"
              >{{ t('modelManager.profile.save') }}</AppButton
            >
          </div>
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
  <AppDraftGuard ref="guard" :dirty="dirty" :pending="saving" />
</template>

<style scoped>
.modern-model-profile-form {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}
.modern-model-profile-body {
  display: grid;
  gap: var(--modern-space-5);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--modern-space-5);
}
.modern-model-profile-body :deep(.modern-model-profile-fields) {
  grid-template-columns: minmax(0, 1fr);
}
.modern-model-profile-footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-3);
  margin-top: auto;
  padding: var(--modern-space-4) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-profile-actions {
  display: flex;
  gap: var(--modern-space-2);
}
</style>
