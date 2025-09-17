<script setup lang="ts">
import { keysApi } from "@/api/keys";
import ProxyKeysInput from "@/components/common/ProxyKeysInput.vue";
import type { Group, SubGroupConfig, SubGroupInfo } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Add, Close, HelpCircleOutline, Remove } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NTag,
  NTooltip,
  useMessage,
  type FormRules,
} from "naive-ui";
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

interface Props {
  show: boolean;
  group?: Group | null;
  groups?: Group[];
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success", value: Group): void;
  (e: "switchToGroup", groupId: number): void;
}

// 子分组项类型
interface SubGroupItem {
  group_id: number | null;
  weight: number;
}

const props = withDefaults(defineProps<Props>(), {
  group: null,
  groups: () => [],
});

const emit = defineEmits<Emits>();

const { t } = useI18n();
const message = useMessage();
const loading = ref(false);
const formRef = ref();

// 所有分组列表
const allGroups = ref<Group[]>([]);

// 表单数据
const formData = reactive<{
  name: string;
  display_name: string;
  description: string;
  channel_type: "anthropic" | "gemini" | "openai";
  sort: number;
  proxy_keys: string;
  sub_groups: SubGroupItem[];
}>({
  name: "",
  display_name: "",
  description: "",
  channel_type: "openai",
  sort: 1,
  proxy_keys: "",
  sub_groups: [],
});

const channelTypeOptions = [
  { label: "OpenAI", value: "openai" },
  { label: "Gemini", value: "gemini" },
  { label: "Anthropic", value: "anthropic" },
];

// 为每个子分组计算可用选项（包括已选择的）
function getSubGroupOptions(
  currentGroupId: number | null
): Array<{ label: string; value: number }> {
  if (!formData.channel_type) {
    return [];
  }

  // 获取其他子分组已选择的ID（不包括当前子分组）
  const otherSelectedIds = formData.sub_groups
    .filter(sg => sg.group_id !== currentGroupId)
    .map(sg => sg.group_id)
    .filter((id): id is number => id !== null);

  return allGroups.value
    .filter(group => {
      // 必须是标准分组
      if (group.group_type === "aggregate") {
        return false;
      }
      // 必须是相同的渠道类型
      if (group.channel_type !== formData.channel_type) {
        return false;
      }
      // 不能是其他子分组已选择的（但可以是当前子分组已选择的）
      if (group.id && group.id !== currentGroupId && otherSelectedIds.includes(group.id)) {
        return false;
      }
      // 编辑模式下，不能选择自己
      if (props.group?.id && group.id === props.group.id) {
        return false;
      }
      return true;
    })
    .map(group => ({
      label: getGroupDisplayName(group),
      value: group.id || 0,
    }));
}

// 计算所有可用的标准分组（用于判断是否有可用分组）
const allStandardGroups = computed(() => {
  if (!formData.channel_type) {
    return [];
  }

  return allGroups.value.filter(group => {
    // 必须是标准分组
    if (group.group_type === "aggregate") {
      return false;
    }
    // 必须是相同的渠道类型
    if (group.channel_type !== formData.channel_type) {
      return false;
    }
    // 编辑模式下，不能选择自己
    if (props.group?.id && group.id === props.group.id) {
      return false;
    }
    return true;
  });
});

// 是否可以添加子分组
const canAddSubGroup = computed(() => {
  if (!formData.channel_type) {
    return false;
  }

  // 如果没有任何可用的标准分组
  if (allStandardGroups.value.length === 0) {
    return false;
  }

  // 获取已选择的分组ID（不包括空值）
  const selectedIds = formData.sub_groups
    .map(sg => sg.group_id)
    .filter((id): id is number => id !== null);

  // 如果已选择的分组数量达到了所有可用分组的数量
  return selectedIds.length < allStandardGroups.value.length;
});

const activeWeightTotal = computed(() =>
  formData.sub_groups.reduce((total, sg) => {
    if (sg.group_id !== null && sg.weight > 0) {
      return total + sg.weight;
    }
    return total;
  }, 0)
);

const weightRatios = computed(() =>
  formData.sub_groups.map(sg => {
    if (sg.group_id !== null && sg.weight > 0 && activeWeightTotal.value > 0) {
      return sg.weight / activeWeightTotal.value;
    }
    return 0;
  })
);

// 获取添加按钮的tooltip信息
const addButtonTooltip = computed(() => {
  if (!formData.channel_type) {
    return t("keys.selectChannelTypeFirst");
  }
  if (allStandardGroups.value.length === 0) {
    return t("keys.noAvailableSubGroups");
  }
  if (!canAddSubGroup.value) {
    return t("keys.allSubGroupsSelected");
  }
  return "";
});

// 表单验证规则
const rules: FormRules = {
  name: [
    {
      required: true,
      message: t("keys.enterGroupName"),
      trigger: ["blur", "input"],
    },
    {
      pattern: /^[a-z0-9_-]{1,100}$/,
      message: t("keys.groupNamePattern"),
      trigger: ["blur", "input"],
    },
  ],
  channel_type: [
    {
      required: true,
      message: t("keys.selectChannelType"),
      trigger: ["blur", "change"],
    },
  ],
};

// 监听弹窗显示状态
watch(
  () => props.show,
  show => {
    if (show) {
      // 如果父组件传入了groups，使用它，否则加载
      if (props.groups && props.groups.length > 0) {
        allGroups.value = props.groups;
      } else if (allGroups.value.length === 0) {
        loadAllGroups();
      }
      // 新建模式重置表单，编辑模式加载数据
      if (props.group) {
        loadGroupData();
      } else {
        resetForm();
        // 新建模式默认添加一个空的子分组
        if (formData.sub_groups.length === 0) {
          formData.sub_groups.push({
            group_id: null,
            weight: 1,
          });
        }
      }
    }
  }
);

// 监听groups prop的变化
watch(
  () => props.groups,
  newGroups => {
    if (newGroups && newGroups.length > 0) {
      allGroups.value = newGroups;
    }
  },
  { immediate: true }
);

// 监听渠道类型变化
watch(
  () => formData.channel_type,
  (newType, oldType) => {
    if (oldType && newType !== oldType && !props.group) {
      // 清空子分组配置并添加一个新的空子分组
      formData.sub_groups = [
        {
          group_id: null,
          weight: 1,
        },
      ];
    }
  }
);

// 加载所有分组
async function loadAllGroups() {
  try {
    const groups = await keysApi.getGroups();
    allGroups.value = groups || [];
  } catch (error) {
    console.error("Failed to load groups:", error);
    allGroups.value = [];
  }
}

// 重置表单
function resetForm() {
  Object.assign(formData, {
    name: "",
    display_name: "",
    description: "",
    channel_type: "openai",
    sort: 1,
    proxy_keys: "",
    sub_groups: [
      {
        group_id: null,
        weight: 1,
      },
    ],
  });
}

// 加载分组数据（编辑模式）
function loadGroupData() {
  if (!props.group) {
    return;
  }

  Object.assign(formData, {
    name: props.group.name || "",
    display_name: props.group.display_name || "",
    description: props.group.description || "",
    channel_type: props.group.channel_type || "openai",
    sort: props.group.sort || 1,
    proxy_keys: props.group.proxy_keys || "",
    sub_groups: (props.group.sub_groups || []).map((sg: SubGroupInfo) => ({
      group_id: sg.group_id,
      weight: sg.weight,
    })),
  });
}

// 添加子分组
function addSubGroup() {
  if (!canAddSubGroup.value) {
    return;
  }

  formData.sub_groups.push({
    group_id: null,
    weight: 1,
  });
}

// 删除子分组
function removeSubGroup(index: number) {
  formData.sub_groups.splice(index, 1);
}

// 验证子分组配置
function validateSubGroups(): boolean {
  // 过滤出所有已选择的子分组
  const selectedSubGroups = formData.sub_groups.filter(sg => sg.group_id !== null);

  // 检查是否至少有一个子分组被选择
  if (selectedSubGroups.length === 0) {
    message.error(t("keys.atLeastOneSubGroup"));
    return false;
  }

  // 检查是否至少有一个有效的子分组（权重大于0）
  const activeSubGroups = selectedSubGroups.filter(sg => sg.weight > 0);
  if (activeSubGroups.length === 0) {
    message.error(t("keys.needAtLeastOneActiveSubGroup"));
    return false;
  }

  // 检查是否有重复的子分组
  const groupIds = selectedSubGroups.map(sg => sg.group_id as number);
  const uniqueIds = new Set(groupIds);
  if (uniqueIds.size !== groupIds.length) {
    message.error(t("keys.duplicateSubGroup"));
    return false;
  }

  return true;
}

function formatWeightRatio(value: number): string {
  if (value <= 0) {
    return "0%";
  }
  return `${(value * 100).toFixed(1)}%`;
}

// 关闭弹窗
function handleClose() {
  emit("update:show", false);
}

// 提交表单
async function handleSubmit() {
  if (loading.value) {
    return;
  }

  try {
    await formRef.value?.validate();

    // 验证子分组配置
    if (!validateSubGroups()) {
      return;
    }

    loading.value = true;

    // 构建提交数据，过滤掉未选择的子分组
    const submitData = {
      name: formData.name.trim(),
      display_name: formData.display_name.trim(),
      description: formData.description.trim(),
      channel_type: formData.channel_type,
      group_type: "aggregate" as const,
      sort: formData.sort,
      proxy_keys: formData.proxy_keys.trim(),
      sub_groups: formData.sub_groups
        .filter(sg => sg.group_id !== null)
        .map(
          sg =>
            ({
              group_id: sg.group_id,
              weight: sg.weight,
            }) as SubGroupConfig
        ),
    };

    let res: Group;
    if (props.group?.id) {
      // 编辑模式
      res = await keysApi.updateGroup(props.group.id, submitData as Partial<Group>);
    } else {
      // 新建模式
      res = await keysApi.createGroup(submitData as Partial<Group>);
    }

    emit("success", res);
    // 如果是新建模式，发出切换到新分组的事件
    if (!props.group?.id && res.id) {
      emit("switchToGroup", res.id);
    }
    handleClose();
  } catch (error) {
    console.error("Submit failed:", error);
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="aggregate-group-modal">
    <n-card
      class="aggregate-group-card"
      :title="group ? t('keys.editAggregateGroup') : t('keys.createAggregateGroup')"
      :bordered="false"
      size="huge"
      role="dialog"
      aria-modal="true"
    >
      <template #header-extra>
        <n-button quaternary circle @click="handleClose">
          <template #icon>
            <n-icon :component="Close" />
          </template>
        </n-button>
      </template>

      <n-form
        ref="formRef"
        :model="formData"
        :rules="rules"
        label-placement="left"
        label-width="120px"
        require-mark-placement="right-hanging"
        class="aggregate-form"
      >
        <!-- 基础信息 -->
        <div class="form-section">
          <h4 class="section-title">{{ t("keys.basicInfo") }}</h4>

          <!-- 分组名称和显示名称 -->
          <div class="form-row">
            <n-form-item :label="t('keys.groupName')" path="name" class="form-item-half">
              <template #label>
                <div class="form-label-with-tooltip">
                  {{ t("keys.groupName") }}
                  <n-tooltip trigger="hover" placement="top">
                    <template #trigger>
                      <n-icon :component="HelpCircleOutline" class="help-icon" />
                    </template>
                    {{ t("keys.groupNameTooltip") }}
                  </n-tooltip>
                </div>
              </template>
              <n-input
                v-model:value="formData.name"
                :placeholder="t('keys.groupNamePlaceholder')"
              />
            </n-form-item>

            <n-form-item :label="t('keys.displayName')" path="display_name" class="form-item-half">
              <template #label>
                <div class="form-label-with-tooltip">
                  {{ t("keys.displayName") }}
                  <n-tooltip trigger="hover" placement="top">
                    <template #trigger>
                      <n-icon :component="HelpCircleOutline" class="help-icon" />
                    </template>
                    {{ t("keys.displayNameTooltip") }}
                  </n-tooltip>
                </div>
              </template>
              <n-input
                v-model:value="formData.display_name"
                :placeholder="t('keys.displayNamePlaceholder')"
              />
            </n-form-item>
          </div>

          <!-- 渠道类型和排序 -->
          <div class="form-row">
            <n-form-item :label="t('keys.channelType')" path="channel_type" class="form-item-half">
              <template #label>
                <div class="form-label-with-tooltip">
                  {{ t("keys.channelType") }}
                  <n-tooltip trigger="hover" placement="top">
                    <template #trigger>
                      <n-icon :component="HelpCircleOutline" class="help-icon" />
                    </template>
                    {{ t("keys.aggregateChannelTypeTooltip") }}
                  </n-tooltip>
                </div>
              </template>
              <n-select
                v-model:value="formData.channel_type"
                :options="channelTypeOptions"
                :placeholder="t('keys.selectChannelType')"
                :disabled="!!props.group"
              />
            </n-form-item>

            <n-form-item :label="t('keys.sortOrder')" path="sort" class="form-item-half">
              <template #label>
                <div class="form-label-with-tooltip">
                  {{ t("keys.sortOrder") }}
                  <n-tooltip trigger="hover" placement="top">
                    <template #trigger>
                      <n-icon :component="HelpCircleOutline" class="help-icon" />
                    </template>
                    {{ t("keys.sortOrderTooltip") }}
                  </n-tooltip>
                </div>
              </template>
              <n-input-number
                v-model:value="formData.sort"
                :min="0"
                :placeholder="t('keys.sortValue')"
                style="width: 100%"
              />
            </n-form-item>
          </div>

          <!-- 代理密钥 -->
          <n-form-item :label="t('keys.proxyKeys')" path="proxy_keys">
            <template #label>
              <div class="form-label-with-tooltip">
                {{ t("keys.proxyKeys") }}
                <n-tooltip trigger="hover" placement="top">
                  <template #trigger>
                    <n-icon :component="HelpCircleOutline" class="help-icon" />
                  </template>
                  {{ t("keys.aggregateProxyKeysTooltip") }}
                </n-tooltip>
              </div>
            </template>
            <proxy-keys-input
              v-model="formData.proxy_keys"
              :placeholder="t('keys.multiKeysPlaceholder')"
              size="medium"
            />
          </n-form-item>

          <!-- 描述 -->
          <n-form-item :label="t('common.description')" path="description">
            <template #label>
              <div class="form-label-with-tooltip">
                {{ t("common.description") }}
                <n-tooltip trigger="hover" placement="top">
                  <template #trigger>
                    <n-icon :component="HelpCircleOutline" class="help-icon" />
                  </template>
                  {{ t("keys.descriptionTooltip") }}
                </n-tooltip>
              </div>
            </template>
            <n-input
              v-model:value="formData.description"
              type="textarea"
              :placeholder="t('keys.descriptionPlaceholder')"
              :rows="1"
              :autosize="{ minRows: 1, maxRows: 5 }"
              style="resize: none"
            />
          </n-form-item>
        </div>

        <!-- 子分组配置 -->
        <div class="form-section">
          <h4 class="section-title">{{ t("keys.subGroupsConfig") }}</h4>

          <template v-if="formData.channel_type">
            <n-form-item
              v-for="(subGroup, index) in formData.sub_groups"
              :key="index"
              :label="`${t('keys.subGroup')} ${index + 1}`"
            >
              <template #label>
                <div class="form-label-with-tooltip">
                  {{ t("keys.subGroup") }} {{ index + 1 }}
                  <n-tooltip trigger="hover" placement="top">
                    <template #trigger>
                      <n-icon :component="HelpCircleOutline" class="help-icon" />
                    </template>
                    {{ t("keys.subGroupTooltip") }}
                  </n-tooltip>
                </div>
              </template>
              <div class="sub-group-row">
                <div class="sub-group-select">
                  <n-select
                    v-model:value="subGroup.group_id"
                    :options="getSubGroupOptions(subGroup.group_id)"
                    :placeholder="t('keys.selectSubGroup')"
                  />
                </div>
                <div class="sub-group-weight">
                  <span class="weight-label">{{ t("keys.weight") }}</span>
                  <n-tooltip trigger="hover" placement="top" style="width: 100%">
                    <template #trigger>
                      <n-input-number
                        v-model:value="subGroup.weight"
                        :min="0"
                        :max="1000"
                        :placeholder="t('keys.weight')"
                        style="width: 100%"
                      />
                    </template>
                    {{ t("keys.weightTooltip") }}
                  </n-tooltip>
                </div>
                <div class="sub-group-status">
                  <template v-if="subGroup.group_id !== null">
                    <n-tag v-if="subGroup.weight === 0" type="warning" size="small">
                      {{ t("keys.disabled") }}
                    </n-tag>
                    <n-tag v-else-if="weightRatios[index] > 0" type="success" size="small">
                      {{ formatWeightRatio(weightRatios[index]) }}
                    </n-tag>
                  </template>
                </div>
                <div class="sub-group-actions">
                  <n-button
                    v-if="formData.sub_groups.length > 1"
                    @click="removeSubGroup(index)"
                    type="error"
                    quaternary
                    circle
                    size="small"
                  >
                    <template #icon>
                      <n-icon :component="Remove" />
                    </template>
                  </n-button>
                </div>
              </div>
            </n-form-item>

            <n-form-item>
              <n-tooltip v-if="addButtonTooltip" trigger="hover" placement="top">
                <template #trigger>
                  <n-button
                    @click="addSubGroup"
                    dashed
                    style="width: 100%"
                    :disabled="!canAddSubGroup"
                  >
                    <template #icon>
                      <n-icon :component="Add" />
                    </template>
                    {{ t("keys.addSubGroup") }}
                  </n-button>
                </template>
                {{ addButtonTooltip }}
              </n-tooltip>
              <n-button
                v-else
                @click="addSubGroup"
                dashed
                style="width: 100%"
                :disabled="!canAddSubGroup"
              >
                <template #icon>
                  <n-icon :component="Add" />
                </template>
                {{ t("keys.addSubGroup") }}
              </n-button>
            </n-form-item>
          </template>

          <template v-else>
            <div class="tip-message">
              <n-icon :component="HelpCircleOutline" />
              {{ t("keys.selectChannelTypeFirst") }}
            </div>
          </template>
        </div>
      </n-form>

      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 12px">
          <n-button @click="handleClose">{{ t("common.cancel") }}</n-button>
          <n-button type="primary" @click="handleSubmit" :loading="loading">
            {{ group ? t("common.update") : t("common.create") }}
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.aggregate-group-modal {
  width: 800px;
}

.form-section {
  margin-top: 20px;
}

.form-section:first-child {
  margin-top: 0;
}

.section-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 16px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid var(--border-color);
}

:deep(.n-form-item-label) {
  font-weight: 500;
}

:deep(.n-form-item-blank) {
  flex-grow: 1;
}

:deep(.n-input) {
  --n-border-radius: 6px;
}

:deep(.n-select) {
  --n-border-radius: 6px;
}

:deep(.n-input-number) {
  --n-border-radius: 6px;
}

:deep(.n-card-header) {
  border-bottom: 1px solid var(--border-color);
  padding: 10px 20px;
}

:deep(.n-card__content) {
  max-height: calc(100vh - 68px - 61px - 50px);
  overflow-y: auto;
}

:deep(.n-card__footer) {
  border-top: 1px solid var(--border-color);
  padding: 10px 15px;
}

:deep(.n-form-item-feedback-wrapper) {
  min-height: 10px;
}

/* Tooltip相关样式 */
.form-label-with-tooltip {
  display: flex;
  align-items: center;
  gap: 6px;
}

.help-icon {
  color: var(--text-tertiary);
  font-size: 14px;
  cursor: help;
  transition: color 0.2s ease;
}

.help-icon:hover {
  color: var(--primary-color);
}

/* 表单行布局 */
.form-row {
  display: flex;
  gap: 20px;
  align-items: flex-start;
}

.form-item-half {
  flex: 1;
  width: 50%;
}

/* 子分组行布局 */
.sub-group-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.sub-group-select {
  flex: 1;
}

.sub-group-weight {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 150px;
}

.weight-label {
  font-weight: 500;
  color: var(--text-primary);
  white-space: nowrap;
}

.sub-group-status {
  flex: 0 0 80px;
  display: flex;
  align-items: center;
}

.sub-group-actions {
  flex: 0 0 32px;
  display: flex;
  justify-content: center;
}

/* 提示信息 */
.tip-message {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  background-color: var(--n-color-info-light);
  border-radius: 6px;
  color: var(--text-secondary);
  font-size: 14px;
  margin-bottom: 16px;
}

.tip-message.warning {
  background-color: var(--n-color-warning-light);
  color: var(--n-color-warning);
}

.tip-text {
  color: var(--text-tertiary);
  font-size: 12px;
  margin-top: 4px;
  text-align: center;
}

@media (max-width: 768px) {
  .aggregate-group-card {
    width: 100vw !important;
  }

  .aggregate-form {
    width: auto !important;
  }

  .form-row {
    flex-direction: column;
    gap: 0;
  }

  .form-item-half {
    width: 100%;
  }

  .section-title {
    font-size: 0.9rem;
  }

  .sub-group-row {
    flex-direction: column;
    gap: 8px;
    align-items: stretch;
  }

  .sub-group-weight {
    flex: 1;
    flex-direction: column;
    align-items: flex-start;
  }

  .sub-group-status,
  .sub-group-actions {
    justify-content: flex-end;
  }
}
</style>
