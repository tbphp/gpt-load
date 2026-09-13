<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { AppConfirmDialog } from '@modern/components/ui'

const props = withDefaults(
  defineProps<{ dirty: boolean; pending?: boolean; queryScope?: readonly string[] }>(),
  { queryScope: () => [] },
)
const { t } = useI18n()
const open = ref(false)
let resolve: ((value: boolean) => void) | undefined
let approved = false
function finish(value: boolean): void {
  open.value = false
  resolve?.(value)
  resolve = undefined
}
function confirm(): boolean | Promise<boolean> {
  if (props.pending) return false
  if (!props.dirty) return true
  resolve?.(false)
  open.value = true
  return new Promise<boolean>((done) => {
    resolve = done
  })
}
function unload(event: BeforeUnloadEvent): void {
  if (!props.dirty && !props.pending) return
  event.preventDefault()
  event.returnValue = ''
}
function routeGuard(): boolean | Promise<boolean> {
  if (approved) {
    approved = false
    return true
  }
  return confirm()
}
onBeforeRouteLeave(routeGuard)
onBeforeRouteUpdate(
  (to, from) =>
    (to.params.id === from.params.id &&
      props.queryScope.every((key) => to.query[key] === from.query[key])) ||
    routeGuard(),
)
onMounted(() => window.addEventListener('beforeunload', unload))
onScopeDispose(() => {
  finish(false)
  window.removeEventListener('beforeunload', unload)
})
defineExpose({
  confirm: async () => {
    const allowed = await confirm()
    if (allowed) approved = true
    return allowed
  },
})
</script>

<template>
  <AppConfirmDialog
    :open="open"
    :title="t('groups.edit.unsaved')"
    :cancel-label="t('groups.edit.keepEditing')"
    :confirm-label="t('groups.edit.discard')"
    tone="danger"
    @cancel="finish(false)"
    @confirm="finish(true)"
  />
</template>
