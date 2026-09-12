<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { AppConfirmDialog } from '@modern/components/ui'

const props = defineProps<{ dirty: boolean; pending?: boolean }>()
const { t } = useI18n()
const open = ref(false)
let resolve: ((value: boolean) => void) | undefined
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
onBeforeRouteLeave(confirm)
onBeforeRouteUpdate((to, from) => to.params.id === from.params.id || confirm())
onMounted(() => window.addEventListener('beforeunload', unload))
onScopeDispose(() => {
  finish(false)
  window.removeEventListener('beforeunload', unload)
})
defineExpose({ confirm })
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
