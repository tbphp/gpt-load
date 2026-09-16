<script setup lang="ts">
import { useLoadingActivity } from '@modern/components/ui/loading'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton } from '@modern/components/ui'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import GroupEditorSurface from './GroupEditorSurface.vue'

const props = defineProps<{
  title: string
  description: string
  dirty: boolean
  pending?: boolean
  loading?: boolean
  saveDisabled?: boolean
  saveLabel?: string
  wide?: boolean
  fill?: boolean
}>()
const emit = defineEmits<{ close: []; save: [] }>()
const { t } = useI18n()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
async function close(): Promise<void> {
  if (!props.pending && (await guard.value?.confirm())) emit('close')
}
useLoadingActivity(() => Boolean(props.loading || props.pending))
</script>

<template>
  <GroupEditorSurface
    :pending="pending"
    :size="wide ? 'sheet' : 'default'"
    :title="title"
    :description="description"
    @close="close"
  >
    <form class="modern-workspace-panel-form" novalidate @submit.prevent="emit('save')">
      <div class="modern-workspace-panel-body" :class="{ 'is-filled': fill }"><slot /></div>
      <footer class="modern-workspace-panel-footer">
        <div class="modern-workspace-panel-feedback"><slot name="feedback" /></div>
        <div class="modern-workspace-panel-actions">
          <AppButton size="sm" :disabled="pending" @click="close">{{ t('ui.cancel') }}</AppButton>
          <AppButton
            type="submit"
            variant="primary"
            size="sm"
            :loading="pending"
            :disabled="saveDisabled || pending || !dirty"
            >{{ saveLabel ?? t('groups.edit.save') }}</AppButton
          >
        </div>
      </footer>
    </form>
  </GroupEditorSurface>
  <AppDraftGuard
    ref="guard"
    :dirty="dirty"
    :pending="pending"
    :query-scope="['panel', 'credential', 'credential_view']"
  />
</template>

<style scoped>
.modern-workspace-panel-form {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-workspace-panel-body {
  container: modern-workspace-panel / inline-size;
  display: grid;
  align-content: start;
  gap: var(--modern-space-5);
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-5);
  overscroll-behavior: contain;
}
.modern-workspace-panel-body.is-filled {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  scrollbar-gutter: auto;
  padding-bottom: 0;
}
.modern-workspace-panel-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-workspace-panel-feedback {
  flex: 1;
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-workspace-panel-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
  margin-left: auto;
}
</style>
