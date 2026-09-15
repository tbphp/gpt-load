<script setup lang="ts">
import { Info, LockKeyhole, RotateCcw, Undo2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppBadge, AppIcon, AppIconButton } from '@modern/components/ui'

withDefaults(
  defineProps<{
    label: string
    controlId?: string
    hint?: string
    description?: string
    overridden?: boolean
    changed?: boolean
    resetting?: boolean
    locked?: boolean
    disabled?: boolean
    stacked?: boolean
    wrapControl?: boolean
  }>(),
  { controlId: undefined, hint: undefined, description: undefined },
)
defineEmits<{ reset: []; undo: [] }>()
const { t } = useI18n()
</script>

<template>
  <div
    class="modern-setting-item"
    :class="{ 'is-stacked': stacked, 'is-wrap-control': wrapControl }"
  >
    <div class="modern-setting-heading">
      <div class="modern-setting-label">
        <label :for="controlId">{{ label }}</label>
        <AppIcon
          v-if="hint && !description"
          :icon="Info"
          size="xs"
          :label="hint"
          class="modern-setting-hint"
        />
        <AppIcon
          v-if="locked"
          :icon="LockKeyhole"
          size="xs"
          :label="t('settingsForm.lockedHelp')"
        />
      </div>
      <div v-if="locked || resetting || changed || overridden" class="modern-setting-source">
        <AppBadge
          variant="plain"
          size="xs"
          :tone="resetting ? 'warning' : changed ? 'brand' : 'neutral'"
        >
          {{
            t(
              locked
                ? 'settingsForm.locked'
                : resetting
                  ? 'settingsForm.pendingDefault'
                  : changed
                    ? 'settingsForm.modified'
                    : 'settingsForm.overridden',
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
      <p v-if="description" class="modern-setting-description">{{ description }}</p>
    </div>
    <p v-if="resetting" class="modern-setting-reset">{{ t('settingsForm.restoreAfterSave') }}</p>
    <div v-else class="modern-setting-control"><slot /></div>
    <div v-if="$slots.details && !resetting" class="modern-setting-details">
      <slot name="details" />
    </div>
  </div>
</template>

<style scoped>
.modern-setting-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-3) var(--modern-space-4);
  min-width: 0;
}
.modern-setting-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
  min-height: var(--modern-control-xxs);
  gap: var(--modern-space-1) var(--modern-space-3);
}
.modern-setting-label {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-1-5);
}
.modern-setting-label label {
  overflow-wrap: anywhere;
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
  flex-wrap: wrap;
  gap: var(--modern-space-1);
}
.modern-setting-control {
  display: grid;
  min-width: 0;
  justify-items: end;
}
.modern-setting-description,
.modern-setting-reset {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  overflow-wrap: anywhere;
}
.modern-setting-description {
  flex-basis: 100%;
}
.modern-setting-reset {
  min-height: var(--modern-control-md);
  display: flex;
  align-items: center;
}
.modern-setting-details {
  grid-column: 1 / -1;
  min-width: 0;
}
.modern-setting-item.is-stacked {
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: var(--modern-space-2);
}
.is-stacked .modern-setting-heading {
  justify-content: space-between;
}
.is-stacked .modern-setting-control {
  justify-items: stretch;
}
@container modern-settings-content (max-width: 620px) {
  .modern-setting-item.is-wrap-control {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--modern-space-2);
  }
  .is-wrap-control .modern-setting-heading {
    justify-content: space-between;
  }
  .is-wrap-control .modern-setting-control {
    justify-items: start;
  }
}
</style>
