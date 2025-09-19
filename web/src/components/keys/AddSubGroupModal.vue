<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, SubGroupInfo } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Add, Close } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NForm,
  NFormItem,
  NIcon,
  NInputNumber,
  NModal,
  NSelect,
  useMessage,
  type FormRules,
} from "naive-ui";
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

interface Props {
  show: boolean;
  aggregateGroup: Group | null;
  existingSubGroups: SubGroupInfo[];
  groups: Group[];
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success"): void;
}

interface SubGroupItem {
  group_id: number | null;
  weight: number;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

const { t } = useI18n();
const message = useMessage();
const loading = ref(false);
const formRef = ref();

// 表单数据
const formData = reactive<{
  sub_groups: SubGroupItem[];
}>({
  sub_groups: [{ group_id: null, weight: 1 }],
});

// 计算可用的分组选项（排除已添加的）
const getAvailableOptions = computed(() => {
  if (!props.aggregateGroup?.channel_type) {
    return [];
  }

  // 获取已存在的子分组ID
  const existingIds = props.existingSubGroups.map(sg => sg.group_id);

  return props.groups
    .filter(group => {
      // 必须是标准分组
      if (group.group_type === "aggregate") {
        return false;
      }

      // 必须是相同的渠道类型
      if (group.channel_type !== props.aggregateGroup?.channel_type) {
        return false;
      }

      // 不能是当前聚合分组自己
      if (props.aggregateGroup?.id && group.id === props.aggregateGroup.id) {
        return false;
      }

      // 不能是已存在的子分组
      if (group.id && existingIds.includes(group.id)) {
        return false;
      }

      return true;
    })
    .map(group => ({
      label: getGroupDisplayName(group),
      value: group?.id,
    }));
});

// 为每个子分组项计算可用选项
const getOptionsForItems = computed(() => {
  const selectedIds = formData.sub_groups.map(sg => sg.group_id).filter(Boolean);

  return formData.sub_groups.map((currentItem, currentIndex) => {
    const otherSelectedIds = selectedIds.filter((_id, index) => index !== currentIndex);

    return getAvailableOptions.value.filter(option => {
      // 如果是当前项已选择的值，允许显示
      if (option.value === currentItem.group_id) {
        return true;
      }
      // 否则只显示未被其他项选择的
      return !otherSelectedIds.includes(option?.value || 0);
    });
  });
});

// 获取指定索引的选项
function getOptionsForItem(index: number) {
  return getOptionsForItems.value[index] || [];
}

// 表单验证规则
const rules: FormRules = {
  sub_groups: {
    type: "array",
    required: true,
    validator: (_rule, value: SubGroupItem[]) => {
      // 检查是否至少有一个有效的子分组
      const validItems = value.filter(item => item.group_id !== null);
      if (validItems.length === 0) {
        return new Error(t("keys.atLeastOneSubGroup"));
      }

      // 检查权重是否合法
      for (const item of validItems) {
        if (item.weight < 0) {
          return new Error(t("keys.weightCannotBeNegative"));
        }
      }

      // 检查是否有重复的子分组
      const groupIds = validItems.map(item => item.group_id);
      const uniqueIds = new Set(groupIds);
      if (uniqueIds.size !== groupIds.length) {
        return new Error(t("keys.duplicateSubGroup"));
      }

      return true;
    },
    trigger: ["blur", "change"],
  },
};

// 监听弹窗显示状态
watch(
  () => props.show,
  show => {
    if (show) {
      resetForm();
    }
  }
);

// 重置表单
function resetForm() {
  formData.sub_groups = [{ group_id: null, weight: 1 }];
}

// 添加子分组项
function addSubGroupItem() {
  formData.sub_groups.push({ group_id: null, weight: 1 });
}

// 删除子分组项
function removeSubGroupItem(index: number) {
  if (formData.sub_groups.length > 1) {
    formData.sub_groups.splice(index, 1);
  }
}

// 关闭弹窗
function handleClose() {
  emit("update:show", false);
}

// 提交表单
async function handleSubmit() {
  if (loading.value || !props.aggregateGroup?.id) {
    return;
  }

  try {
    await formRef.value?.validate();
    loading.value = true;

    // 过滤出有效的子分组
    const validSubGroups = formData.sub_groups.filter(sg => sg.group_id !== null);

    if (validSubGroups.length === 0) {
      message.error(t("keys.atLeastOneSubGroup"));
      return;
    }

    await keysApi.addSubGroups(
      props.aggregateGroup.id,
      validSubGroups as { group_id: number; weight: number }[]
    );

    message.success(t("keys.addSubGroupSuccess"));
    emit("success");
    handleClose();
  } catch (error: unknown) {
    console.error("Add sub groups failed:", error);
    const errorMessage = getErrorMessage(error) || t("keys.addSubGroupFailed");
    message.error(errorMessage);
  } finally {
    loading.value = false;
  }
}

// 提取错误消息的工具函数
function getErrorMessage(error: unknown): string | null {
  if (error && typeof error === "object" && "response" in error) {
    const axiosError = error as { response?: { data?: { message?: string } } };
    return axiosError.response?.data?.message || null;
  }
  return null;
}

// 是否可以添加更多子分组项
const canAddMore = computed(() => {
  return formData.sub_groups.length < getAvailableOptions.value.length;
});
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="add-sub-group-modal">
    <n-card
      class="add-sub-group-card"
      :title="t('keys.addSubGroup')"
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
        label-width="100px"
      >
        <div class="form-section">
          <h4 class="section-title">
            {{ t("keys.selectSubGroups") }}
            <span class="section-subtitle">
              ({{ t("keys.channelType") }}: {{ aggregateGroup?.channel_type?.toUpperCase() }})
            </span>
          </h4>

          <div class="sub-groups-list">
            <div v-for="(item, index) in formData.sub_groups" :key="index" class="sub-group-item">
              <span class="item-label">{{ t("keys.subGroup") }} {{ index + 1 }}</span>

              <n-form-item
                class="item-select"
                :path="`sub_groups[${index}].group_id`"
                :show-feedback="false"
              >
                <n-select
                  v-model:value="item.group_id"
                  :options="getOptionsForItem(index)"
                  :placeholder="t('keys.selectSubGroup')"
                  clearable
                />
              </n-form-item>

              <n-form-item
                class="item-weight"
                :path="`sub_groups[${index}].weight`"
                :show-feedback="false"
              >
                <n-input-number
                  v-model:value="item.weight"
                  :min="0"
                  :max="1000"
                  :placeholder="t('keys.enterWeight')"
                  style="width: 100%"
                />
              </n-form-item>

              <n-button
                @click="removeSubGroupItem(index)"
                type="error"
                quaternary
                circle
                size="small"
                class="item-delete"
                :style="{ visibility: formData.sub_groups.length > 1 ? 'visible' : 'hidden' }"
              >
                <template #icon>
                  <n-icon :component="Close" />
                </template>
              </n-button>
            </div>
          </div>

          <div class="add-item-section">
            <n-button v-if="canAddMore" @click="addSubGroupItem" dashed style="width: 100%">
              <template #icon>
                <n-icon :component="Add" />
              </template>
              {{ t("keys.addMoreSubGroup") }}
            </n-button>
            <div v-else class="no-more-tip">
              {{ t("keys.noMoreAvailableGroups") }}
            </div>
          </div>
        </div>
      </n-form>

      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 12px">
          <n-button @click="handleClose">{{ t("common.cancel") }}</n-button>
          <n-button type="primary" @click="handleSubmit" :loading="loading">
            {{ t("common.confirm") }}
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.add-sub-group-modal {
  width: 700px;
}

.form-section {
  margin-top: 0;
}

.section-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border-color);
}

.section-subtitle {
  font-size: 0.85rem;
  font-weight: 400;
  color: var(--text-secondary);
  margin-left: 8px;
}

.sub-groups-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-bottom: 20px;
}

.sub-group-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-radius: var(--border-radius-md);
}

.item-label {
  flex-shrink: 0;
  min-width: 80px;
  font-weight: 500;
  color: var(--text-primary);
  font-size: 0.9rem;
}

.item-select {
  flex: 1;
  min-width: 200px;
}

.item-weight {
  width: 100px;
  flex-shrink: 0;
}

.item-delete {
  flex-shrink: 0;
}

.add-item-section {
  margin-top: 16px;
}

.no-more-tip {
  text-align: center;
  color: var(--text-tertiary);
  font-size: 0.9rem;
  padding: 12px;
  background: var(--bg-secondary);
  border-radius: var(--border-radius-sm);
}

/* 响应式适配 */
@media (max-width: 768px) {
  .add-sub-group-modal {
    width: 90vw;
  }

  .sub-group-item {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .item-label {
    min-width: auto;
    text-align: center;
  }

  .item-select,
  .item-weight {
    width: 100%;
    min-width: auto;
  }

  .item-delete {
    align-self: center;
  }
}

/* 暗黑模式适配 */
:root.dark .sub-group-item {
  background: var(--bg-tertiary);
  border-color: var(--border-color);
}

:root.dark .no-more-tip {
  background: var(--bg-tertiary);
}
</style>
