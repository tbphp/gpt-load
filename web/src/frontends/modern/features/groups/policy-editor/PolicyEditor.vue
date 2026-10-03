<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus } from '@lucide/vue'
import { AppButton, AppNotice } from '@modern/components/ui'
import {
  arrayItems,
  arrayNode,
  duplicateRule,
  getField,
  hasFatalIssue,
  insertRule,
  insertRuleAfter,
  literalString,
  moveRule,
  newRule,
  newRuleId,
  numberLiteral,
  objectNode,
  policyLimits,
  readVisualDocument,
  removeRuleAt,
  replaceRuleAt,
  serializeJson,
  unsupportedPaths,
  type JsonNode,
  type JsonObjectNode,
  type PolicyDomain,
  type PolicyIssue,
  type PolicyIssueCode,
  type VisualDocumentResult,
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
  draftStatus: [status: { valid: boolean; pending: boolean; hasInvalid: boolean }]
}>()
const { t } = usePolicyMessages()
const { summary: draftSummary } = providePolicyDraftCollector()

const root = ref<JsonObjectNode>()
const rules = ref<VisualDocumentResult['rules']>([])
const issues = ref<PolicyIssue[]>([])
const editIssue = ref<PolicyIssue>()
let lastEmitted: string | undefined

function applyResult(result: VisualDocumentResult): void {
  root.value = result.root
  rules.value = result.rules
  issues.value = result.issues
}

watch(
  () => props.modelValue,
  (text) => {
    // 自主提交产生的回传不再重新解析，避免覆盖正在编辑的草稿；初始空白不会被误判为已提交。
    if (text === lastEmitted) return
    lastEmitted = undefined
    editIssue.value = undefined
    applyResult(readVisualDocument(text))
  },
  { immediate: true },
)

const fatalIssue = computed(() => issues.value.find((issue) => issue.fatal))
// 空白正文视为待创建的初始配置：仍展示“新增规则”入口，首次新增时补全根对象。
const isBlank = computed(() => !root.value && issues.value.some((issue) => issue.code === 'empty'))

function emptyDocument(): JsonObjectNode {
  return objectNode([
    { key: 'schema_version', value: numberLiteral('1')! },
    { key: 'rules', value: arrayNode([]) },
  ])
}
const unsupported = computed(() => unsupportedPaths(issues.value))
// 以原始数组长度与索引为准，避免读取失败的规则导致可视化索引错位。
const rootRules = computed(() => {
  const current = root.value
  return current ? (arrayItems(getField(current, 'rules')) ?? []) : []
})
const totalRules = computed(() => rootRules.value.length)
const ruleIds = computed(() =>
  rootRules.value.map((item) => literalString(getField(item, 'id')) ?? '').filter(Boolean),
)
// 以规则 ID 作为稳定键，让重排/移除时的本地草稿跟随规则；重复与缺失 ID 用序号消歧。
const ruleKeys = computed(() => {
  const seen = new Map<string, number>()
  return rules.value.map((rule) => {
    const base = literalString(getField(rule.node, 'id')) || `invalid-${rule.index}`
    const occurrence = (seen.get(base) ?? 0) + 1
    seen.set(base, occurrence)
    return occurrence > 1 ? `${base}#${occurrence}` : base
  })
})

const maxByCode: Partial<Record<PolicyIssueCode, number>> = {
  ruleCount: policyLimits.maxRulesPerConfig,
  conditionDepth: policyLimits.maxConditionDepth,
  nodePerRule: policyLimits.maxNodesPerRule,
  nodePerConfig: policyLimits.maxNodesPerConfig,
}

function issueText(issue: PolicyIssue): string {
  return t(`policyEditor.errors.${issue.code}`, { max: maxByCode[issue.code] ?? 0 })
}

function commit(next: JsonObjectNode): void {
  if (props.disabled) return
  const text = serializeJson(next)
  const candidate = readVisualDocument(text)
  const candidateFatal = candidate.issues.find((issue) => issue.fatal)
  if (candidateFatal && !isBlank.value) {
    // 预算超限候选拒绝提交，保留当前视觉树，避免101条规则等预算致命错误导致规则树被隐藏无法视觉撤销
    editIssue.value = candidateFatal
    return
  }
  editIssue.value = undefined
  lastEmitted = text
  applyResult(candidate)
  emit('update:modelValue', text)
}

function updateRule(index: number, node: JsonNode): void {
  if (props.disabled) return
  if (root.value) commit(replaceRuleAt(root.value, index, node))
}

function addRule(): void {
  if (props.disabled) return
  if (totalRules.value >= policyLimits.maxRulesPerConfig) return
  const base = root.value ?? emptyDocument()
  commit(insertRule(base, newRule(newRuleId(ruleIds.value), props.defaultDomain)))
}

function moveRuleTo(index: number, delta: number): void {
  if (props.disabled) return
  if (root.value) commit(moveRule(root.value, index, index + delta))
}

function duplicateRuleAt(index: number): void {
  if (props.disabled) return
  if (totalRules.value >= policyLimits.maxRulesPerConfig) return
  // 可视化列表已过滤掉非对象项，必须按原始 JSON 索引定位，不能按下标取。
  const rule = rules.value.find((item) => item.index === index)
  if (!root.value || !rule) return
  commit(insertRuleAfter(root.value, index, duplicateRule(rule, ruleIds.value)))
}

function removeRule(index: number): void {
  if (props.disabled) return
  if (root.value) commit(removeRuleAt(root.value, index))
}

const limitActive = computed(() => hasFatalIssue(issues.value))

watch(
  [
    () => draftSummary.value.valid,
    () => draftSummary.value.pending,
    () => draftSummary.value.hasInvalid,
  ],
  ([valid, pending, hasInvalid]) => {
    emit('draftStatus', {
      valid,
      pending,
      hasInvalid,
    })
  },
  { immediate: true },
)
</script>

<template>
  <div class="policy-editor">
    <div v-if="!disabled" class="policy-editor-toolbar">
      <AppButton
        variant="outline"
        size="xs"
        :icon="Plus"
        :disabled="
          disabled || (limitActive && !isBlank) || totalRules >= policyLimits.maxRulesPerConfig
        "
        @click="addRule"
      >
        {{ t('policyEditor.addRule') }}
      </AppButton>
    </div>

    <AppNotice v-if="editIssue" tone="danger" class="policy-editor-notice">
      <span>{{ issueText(editIssue) }}</span>
    </AppNotice>

    <AppNotice v-if="fatalIssue && !isBlank" tone="danger" class="policy-editor-notice">
      <strong>{{ t('policyEditor.limitsTitle') }}</strong>
      <span>{{ issueText(fatalIssue) }}</span>
    </AppNotice>

    <template v-else>
      <AppNotice v-if="unsupported.length" tone="warning" class="policy-editor-notice">
        <strong>{{ t('policyEditor.unsupportedTitle') }}</strong>
        <span>{{ t('policyEditor.unsupportedHint') }}</span>
        <span class="policy-editor-paths">
          <span v-for="path in unsupported" :key="path" class="policy-editor-path font-mono">{{
            path
          }}</span>
        </span>
      </AppNotice>

      <p v-if="totalRules" class="policy-editor-count">
        {{ t('policyEditor.ruleCount', { count: totalRules }) }}
      </p>

      <div v-if="rules.length" class="policy-editor-rules">
        <PolicyRuleCard
          v-for="(rule, position) in rules"
          :key="ruleKeys[position]"
          :rule="rule"
          :index="rule.index"
          :total="totalRules"
          :disabled="disabled"
          :quota-windows="quotaWindows"
          @update="(node) => updateRule(rule.index, node)"
          @move="(delta) => moveRuleTo(rule.index, delta)"
          @duplicate="duplicateRuleAt(rule.index)"
          @remove="removeRule(rule.index)"
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
.policy-editor-paths {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-editor-path {
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  padding: 0 var(--modern-space-1);
  font-size: var(--modern-font-size-caption);
}
.policy-editor-count {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.policy-editor-rules {
  display: grid;
  gap: var(--modern-space-3);
}
</style>
