<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus, X } from '@lucide/vue'
import {
  AppButton,
  AppCheckbox,
  AppIconButton,
  AppNotice,
  AppTextField,
} from '@modern/components/ui'
import { isTimeOfDay, type VisualTimeWindowCondition } from './policy-model'
import { usePolicyDraftStatus } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{ condition: VisualTimeWindowCondition; disabled?: boolean }>(),
  { disabled: false },
)
const emit = defineEmits<{ update: [condition: VisualTimeWindowCondition] }>()
const { t } = usePolicyMessages()

const weekdays = computed(() => props.condition.weekdays.filter((day) => day >= 0 && day <= 6))
const weekdayError = ref(false)
const weekdayLabels = computed(() =>
  [0, 1, 2, 3, 4, 5, 6].map((day) => ({
    day,
    label: t(`policyEditor.timeWindow.weekday.${day}`),
  })),
)

function setWeekdays(days: number[]): void {
  if (props.disabled) return
  const unique = Array.from(new Set(days)).sort((a, b) => a - b)
  if (unique.length === 0) {
    weekdayError.value = true
    return
  }
  emit('update', { ...props.condition, weekdays: unique })
}

function toggleWeekday(day: number, checked: boolean): void {
  if (props.disabled) return
  const current = weekdays.value
  if (checked) {
    if (!current.includes(day)) setWeekdays([...current, day])
    weekdayError.value = false
    return
  }
  const remaining = current.filter((item) => item !== day)
  if (remaining.length === 0) {
    weekdayError.value = true
    return
  }
  weekdayError.value = false
  setWeekdays(remaining)
}

interface RangeDraft {
  start: string
  end: string
}

const storedRanges = computed(() =>
  props.condition.ranges.map(([start, end]) => ({ start, end })),
)
const ranges = storedRanges
const rangeDrafts = ref<RangeDraft[]>([])
let removedRangeIndex: number | undefined
// 仅在存储值本身变化时更新对应草稿；无关重渲染/星期切换保留未提交的本地输入。
watch(
  storedRanges,
  (next, prev) => {
    const previous = [...(prev ?? [])]
    if (removedRangeIndex !== undefined) {
      previous.splice(removedRangeIndex, 1)
      removedRangeIndex = undefined
    }
    rangeDrafts.value = next.map((range, index) => {
      const before = previous[index]
      if (!before || before.start !== range.start || before.end !== range.end) return { ...range }
      return rangeDrafts.value[index] ?? { ...range }
    })
  },
  { immediate: true },
)
usePolicyDraftStatus('time-window', () => ({
  active: !props.disabled,
  valid:
    weekdays.value.length > 0 && !rangeDrafts.value.some((draft) => Boolean(rangeError(draft))),
  pending: rangeDrafts.value.some((draft, index) => {
    const stored = storedRanges.value[index]
    return !stored || draft.start !== stored.start || draft.end !== stored.end
  }),
}))

function rangeError(draft: RangeDraft | undefined): string | undefined {
  if (!draft) return undefined
  if (!isTimeOfDay(draft.start) || !isTimeOfDay(draft.end)) {
    return t('policyEditor.timeWindow.errors.timeInvalid')
  }
  if (draft.start === draft.end) return t('policyEditor.timeWindow.errors.rangeEqual')
  return undefined
}

function commitRanges(next: Array<[string, string]>): void {
  emit('update', { ...props.condition, ranges: next })
}

function commitRange(index: number): void {
  if (props.disabled) return
  const draft = rangeDrafts.value[index]
  if (!draft || rangeError(draft)) return
  const next = ranges.value.map(
    (range, at): [string, string] =>
      at === index ? [draft.start.trim(), draft.end.trim()] : [range.start, range.end],
  )
  commitRanges(next)
}

function addRange(): void {
  if (props.disabled) return
  commitRanges([...ranges.value.map((r): [string, string] => [r.start, r.end]), ['09:00', '18:00']])
}

const rangeRequired = ref(false)
function removeRange(index: number): void {
  if (props.disabled) return
  if (ranges.value.length <= 1) {
    rangeRequired.value = true
    return
  }
  rangeRequired.value = false
  removedRangeIndex = index
  rangeDrafts.value.splice(index, 1)
  commitRanges(
    ranges.value.filter((_, at) => at !== index).map((r): [string, string] => [r.start, r.end]),
  )
}
</script>

<template>
  <div class="policy-time-window">
    <div class="policy-time-weekdays">
      <span class="policy-time-label">{{ t('policyEditor.timeWindow.weekdays') }}</span>
      <div class="policy-time-weekday-list">
        <AppCheckbox
          v-for="item in weekdayLabels"
          :key="item.day"
          :model-value="weekdays.includes(item.day)"
          :label="item.label"
          :disabled="disabled"
          @update:model-value="(checked) => toggleWeekday(item.day, checked === true)"
        />
      </div>
      <AppNotice v-if="weekdayError" tone="warning" compact>
        {{ t('policyEditor.timeWindow.errors.weekdayRequired') }}
      </AppNotice>
    </div>

    <div class="policy-time-ranges">
      <span class="policy-time-label">{{ t('policyEditor.timeWindow.ranges') }}</span>
      <div v-for="(range, index) in rangeDrafts" :key="index" class="policy-time-range-row">
        <AppTextField
          v-model="range.start"
          :label="t('policyEditor.timeWindow.from')"
          placeholder="09:00"
          size="sm"
          :error="rangeError(range)"
          :disabled="disabled"
          @change="commitRange(index)"
          @blur="commitRange(index)"
        />
        <AppTextField
          v-model="range.end"
          :label="t('policyEditor.timeWindow.to')"
          placeholder="18:00"
          size="sm"
          :disabled="disabled"
          @change="commitRange(index)"
          @blur="commitRange(index)"
        />
        <div class="policy-time-range-action">
          <span class="policy-time-range-spacer" aria-hidden="true" />
          <AppIconButton
            :icon="X"
            :label="t('policyEditor.timeWindow.removeRange')"
            size="xs"
            variant="ghost"
            :disabled="disabled"
            @click="removeRange(index)"
          />
        </div>
      </div>
      <AppNotice v-if="rangeRequired" tone="warning" compact>
        {{ t('policyEditor.timeWindow.errors.rangeRequired') }}
      </AppNotice>
      <AppButton size="xs" variant="outline" :icon="Plus" :disabled="disabled" @click="addRange">
        {{ t('policyEditor.timeWindow.addRange') }}
      </AppButton>
    </div>

    <p class="modern-hint">{{ t('policyEditor.timeWindow.hint') }}</p>
  </div>
</template>

<style scoped>
.policy-time-window {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.policy-time-weekdays,
.policy-time-ranges {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-time-label {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.policy-time-weekday-list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-3);
}
.policy-time-range-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--modern-space-2);
}
.policy-time-range-row > :deep(*) {
  min-width: 0;
}
.policy-time-range-action {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--modern-space-1-5);
}
.policy-time-range-spacer {
  display: block;
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-compact);
  visibility: hidden;
  user-select: none;
}
.policy-time-range-spacer::before {
  content: '\00a0';
}
.policy-time-range-action :deep(.modern-button) {
  margin: calc((var(--modern-control-sm) - var(--modern-control-xs)) / 2) 0;
}
</style>
