<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus } from '@lucide/vue'
import { AppButton, AppNotice } from '@modern/components/ui'
import {
  duplicateRule,
  emptyDocument,
  hasFatalIssue,
  moveRule,
  newRule,
  newRuleId,
  policyLimits,
  readVisualDocument,
  serializeDocument,
  type PolicyDomain,
  type PolicyIssue,
  type PolicyIssueCode,
  type VisualDocument,
  type VisualDocumentResult,
  type VisualRule,
} from './policy-model'
import { providePolicyDraftCollector } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'
import PolicyRuleCard from './PolicyRuleCard.vue'

const props = withDefaults(
  defineProps<{
    // 顶层配置正文；空白/非法时只报错，绝不当作清空。
    modelValue: string
    defaultDomain?: PolicyDomain
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  { defaultDomain: 'scheduling', disabled: false, quotaWindows: () => [] },
)
const emit = defineEmits<{
  'update:modelValue': [value: string]
  draftStatus: [status: { valid: boolean; pending: boolean }]
}>()
const { t } = usePolicyMessages()
const { summary: draftSummary } = providePolicyDraftCollector()

const document = ref<VisualDocument>()
const advancedText = ref<string>()
const issues = ref<PolicyIssue[]>([])
let lastEmitted: string | undefined

function applyResult(result: VisualDocumentResult): void {
  document.value = result.document
  advancedText.value = result.advancedText
  issues.value = result.issues
}

watch(
  () => props.modelValue,
  (text) => {
    // 自主提交产生的回传不再重新解析，避免覆盖正在编辑的草稿；初始空白不会被误判为已提交。
    if (text === lastEmitted) return
    lastEmitted = undefined
    applyResult(readVisualDocument(text))
  },
  { immediate: true },
)

const fatalIssue = computed(() => issues.value.find((issue) => issue.fatal))
// 空白正文视为待创建的初始配置：仍展示“新增规则”入口，首次新增时补全根对象。
const isBlank = computed(() => !document.value && issues.value.some((issue) => issue.code === 'empty'))
const advanced = computed(() => advancedText.value !== undefined)

const rules = computed(() => document.value?.rules ?? [])
const totalRules = computed(() => rules.value.length)
const ruleIds = computed(() => rules.value.map((rule) => rule.id).filter(Boolean))
// 以规则 ID 作为稳定键，让重排/移除时的本地草稿跟随规则；重复与缺失 ID 用序号消歧。
const ruleKeys = computed(() => {
  const seen = new Map<string, number>()
  return rules.value.map((rule, index) => {
    const base = rule.id || `invalid-${index}`
    const occurrence = (seen.get(base) ?? 0) + 1
    seen.set(base, occurrence)
    return occurrence > 1 ? `${base}#${occurrence}` : base
  })
})

const maxByCode: Partial<Record<PolicyIssueCode, number>> = {
  ruleCount: policyLimits.maxRulesPerConfig,
}

function issueText(issue: PolicyIssue): string {
  return t(`policyEditor.errors.${issue.code}`, { max: maxByCode[issue.code] ?? 0 })
}

function commit(next: VisualDocument): void {
  if (props.disabled) return
  document.value = next
  const text = serializeDocument(next)
  lastEmitted = text
  emit('update:modelValue', text)
}

function updateRule(index: number, rule: VisualRule): void {
  commit({ ...(document.value ?? emptyDocument()), rules: rules.value.map((item, at) => (at === index ? rule : item)) })
}

function addRule(): void {
  if (props.disabled || totalRules.value >= policyLimits.maxRulesPerConfig) return
  const base = document.value ?? emptyDocument()
  commit({
    ...base,
    rules: [...base.rules, newRule(newRuleId(ruleIds.value), props.defaultDomain)],
  })
}

function moveRuleTo(index: number, delta: number): void {
  commit({
    ...(document.value ?? emptyDocument()),
    rules: moveRule(rules.value, index, index + delta),
  })
}

function duplicateRuleAt(index: number): void {
  if (props.disabled || totalRules.value >= policyLimits.maxRulesPerConfig) return
  const rule = rules.value[index]
  if (!rule) return
  const next = [...rules.value]
  next.splice(index + 1, 0, duplicateRule(rule, ruleIds.value))
  commit({ ...(document.value ?? emptyDocument()), rules: next })
}

function removeRule(index: number): void {
  commit({
    ...(document.value ?? emptyDocument()),
    rules: rules.value.filter((_, at) => at !== index),
  })
}

const limitActive = computed(() => hasFatalIssue(issues.value))

watch(
  [() => draftSummary.value.valid, () => draftSummary.value.pending],
  ([valid, pending]) => {
    emit('draftStatus', { valid, pending })
  },
  { immediate: true },
)
</script>

<template>
  <div class="policy-editor">
    <div v-if="!disabled && !advanced" class="policy-editor-toolbar">
      <AppButton
        variant="outline"
        size="xs"
        :icon="Plus"
        :disabled="(limitActive && !isBlank) || totalRules >= policyLimits.maxRulesPerConfig"
        @click="addRule"
      >
        {{ t('policyEditor.addRule') }}
      </AppButton>
    </div>

    <template v-if="advanced">
      <AppNotice tone="warning" class="policy-editor-notice">
        <strong>{{ t('policyEditor.advancedTitle') }}</strong>
        <span>{{ t('policyEditor.advancedHint') }}</span>
      </AppNotice>
      <pre class="policy-editor-json font-mono">{{ advancedText }}</pre>
    </template>

    <AppNotice
      v-else-if="fatalIssue && !isBlank"
      tone="danger"
      class="policy-editor-notice"
    >
      <strong>{{ t('policyEditor.limitsTitle') }}</strong>
      <span>{{ issueText(fatalIssue) }}</span>
    </AppNotice>

    <template v-else>
      <p v-if="totalRules" class="policy-editor-count">
        {{ t('policyEditor.ruleCount', { count: totalRules }) }}
      </p>

      <div v-if="rules.length" class="policy-editor-rules">
        <PolicyRuleCard
          v-for="(rule, position) in rules"
          :key="ruleKeys[position]"
          :rule="rule"
          :index="position"
          :total="totalRules"
          :disabled="disabled"
          :quota-windows="quotaWindows"
          @update="(next) => updateRule(position, next)"
          @move="(delta) => moveRuleTo(position, delta)"
          @duplicate="duplicateRuleAt(position)"
          @remove="removeRule(position)"
        />
      </div>
    </template>
  </div>
</template>

<style scoped>
.policy-editor {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.policy-editor-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--modern-space-2);
}
.policy-editor-notice {
  flex-direction: column;
  align-items: flex-start;
}
.policy-editor-count {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.policy-editor-rules {
  display: grid;
  gap: var(--modern-space-3);
}
.policy-editor-json {
  overflow-x: auto;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-2);
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
}
</style>
