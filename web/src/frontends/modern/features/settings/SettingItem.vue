<script setup lang="ts">
import { Info, LockKeyhole, RotateCcw, Undo2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppBadge, AppIcon, AppIconButton } from '@modern/components/ui'

withDefaults(
  defineProps<{
    label: string
    controlId?: string
    hint?: string
    overridden?: boolean
    changed?: boolean
    resetting?: boolean
    locked?: boolean
    disabled?: boolean
    stacked?: boolean
  }>(),
  { controlId: undefined, hint: undefined },
)
defineEmits<{ reset: []; undo: [] }>()
const { t } = useI18n()
</script>

<template>
  <div class="modern-setting-item" :class="{ 'is-stacked': stacked }">
    <div class="modern-setting-heading">
      <div class="modern-setting-label">
        <label :for="controlId">{{ label }}</label>
        <AppIcon v-if="hint" :icon="Info" size="xs" :label="hint" class="modern-setting-hint" />
        <AppIcon
          v-if="locked"
          :icon="LockKeyhole"
          size="xs"
          :label="t('settingsForm.lockedHelp')"
        />
      </div>
      <div class="modern-setting-source">
        <AppBadge variant="plain" size="xs" :tone="resetting ? 'warning' : 'neutral'">
          {{
            t(
              locked
                ? 'settingsForm.locked'
                : resetting
                  ? 'settingsForm.pendingDefault'
                  : changed
                    ? 'settingsForm.modified'
                    : overridden
                      ? 'settingsForm.overridden'
                      : 'settingsForm.default',
            )
          }}
        </AppBadge>
        <AppIconButton
          v-if="!locked && (overridden || changed || resetting)"
          :icon="resetting ? Undo2 : RotateCcw"
          :label="
            t(
              resetting
                ? 'settingsForm.undoDefault'
                : overridden
                  ? 'settingsForm.restoreDefault'
                  : 'settingsForm.revertField',
            )
          "
          size="xxs"
          :disabled="disabled"
          @click="resetting ? $emit('undo') : $emit('reset')"
        />
      </div>
    </div>
    <p v-if="resetting" class="modern-setting-reset">{{ t('settingsForm.restoreAfterSave') }}</p>
    <div v-else class="modern-setting-control"><slot /></div>
  </div>
</template>

<style scoped>
.modern-setting-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-3) var(--modern-space-5);
  min-width: 0;
  padding-block: var(--modern-space-2);
}
.modern-setting-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
  min-height: var(--modern-control-xs);
  gap: var(--modern-space-1) var(--modern-space-3);
}
.modern-setting-label {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-1-5);
}
.modern-setting-label label {
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-setting-hint {
  color: var(--modern-muted);
}
.modern-setting-source {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
}
.modern-setting-control {
  min-width: 0;
}
.modern-setting-reset {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-setting-item.is-stacked {
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: var(--modern-space-1-5);
}
.is-stacked .modern-setting-heading {
  justify-content: space-between;
}
@media (max-width: 760px) {
  .modern-setting-item {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--modern-space-2);
  }
  .modern-setting-heading {
    justify-content: space-between;
  }
}
</style>
