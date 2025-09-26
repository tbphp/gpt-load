<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, SubGroupInfo } from "@/types/models";
import { Close } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NForm,
  NFormItem,
  NIcon,
  NInputNumber,
  NModal,
  useMessage,
  type FormRules,
} from "naive-ui";
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

interface Props {
  show: boolean;
  subGroup: SubGroupInfo | null;
  aggregateGroup: Group | null;
  subGroups: SubGroupInfo[]; // 当前的子分组列表
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success"): void;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

const { t } = useI18n();
const message = useMessage();
const loading = ref(false);
const formRef = ref();

// 表单数据
const formData = reactive<{
  weight: number;
}>({
  weight: 0,
});

// 预览新的权重百分比（假设其他子分组权重不变）
const previewPercentage = computed(() => {
  if (!props.subGroups || !props.subGroup) {
    return 0;
  }

  // 计算总权重（用新权重替换当前子分组的权重）
  const totalWeight = props.subGroups.reduce((sum, sg) => {
    if (sg.group_id === props.subGroup?.group_id) {
      return sum + formData.weight;
    }
    return sum + sg.weight;
  }, 0);

  return totalWeight > 0 ? Math.round((formData.weight / totalWeight) * 100) : 0;
});

// 表单验证规则
const rules: FormRules = {
  weight: [
    {
      validator: (_rule, value) => {
        if (value === null || value === undefined || value === "") {
          return new Error(t("keys.enterWeight"));
        }
        if (value < 0) {
          return new Error(t("keys.weightCannotBeNegative"));
        }
        if (value > 1000) {
          return new Error(t("keys.weightMaxExceeded"));
        }
        return true;
      },
      trigger: ["blur", "input"],
    },
  ],
};

// 监听弹窗显示状态和子分组变化
watch(
  () => [props.show, props.subGroup] as const,
  ([show, subGroup]) => {
    if (show && subGroup) {
      formData.weight = subGroup.weight;
    }
  },
  { immediate: true }
);

// 关闭弹窗
function handleClose() {
  emit("update:show", false);
}

// 提交表单
async function handleSubmit() {
  if (loading.value || !props.subGroup || !props.aggregateGroup) {
    return;
  }

  try {
    await formRef.value?.validate();

    loading.value = true;

    if (!props.aggregateGroup?.id) {
      message.error(t("keys.invalidAggregateGroup"));
      return;
    }

    await keysApi.updateSubGroupWeight(
      props.aggregateGroup.id,
      props.subGroup.group_id,
      formData.weight // 保持原始数值，不进行取整
    );

    // 后端已经通过API响应显示成功消息，这里不需要重复显示
    emit("success");
    handleClose();
  } finally {
    loading.value = false;
  }
}

// 快速调整权重
function adjustWeight(delta: number) {
  const newWeight = Math.max(0, Math.min(1000, formData.weight + delta));
  formData.weight = newWeight;
}
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="edit-weight-modal">
    <n-card
      class="edit-weight-card"
      :title="t('keys.editWeight')"
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
      >
        <div class="form-section">
          <div class="sub-group-info">
            <h4 class="section-title">
              {{ t("keys.editingSubGroup") }}:
              <span class="group-name">{{ subGroup?.display_name || subGroup?.name }}</span>
            </h4>
            <div class="group-details">
              <span class="detail-item">
                <strong>{{ t("keys.groupId") }}:</strong>
                {{ subGroup?.group_id }}
              </span>
              <span class="detail-item">
                <strong>{{ t("keys.currentWeight") }}:</strong>
                {{ subGroup?.weight }}
              </span>
            </div>
          </div>

          <n-form-item :label="t('keys.newWeight')" path="weight">
            <div class="weight-input-section">
              <n-input-number
                v-model:value="formData.weight"
                :min="0"
                :max="1000"
                :precision="0"
                :placeholder="t('keys.enterWeight')"
                style="flex: 1"
              />
              <div class="quick-adjust">
                <n-button size="small" @click="adjustWeight(-10)" :disabled="formData.weight <= 0">
                  -10
                </n-button>
                <n-button size="small" @click="adjustWeight(-1)" :disabled="formData.weight <= 0">
                  -1
                </n-button>
                <n-button size="small" @click="adjustWeight(1)" :disabled="formData.weight >= 1000">
                  +1
                </n-button>
                <n-button
                  size="small"
                  @click="adjustWeight(10)"
                  :disabled="formData.weight >= 1000"
                >
                  +10
                </n-button>
              </div>
            </div>
          </n-form-item>

          <div class="preview-section">
            <div class="preview-item">
              <span class="preview-label">{{ t("keys.previewPercentage") }}:</span>
              <span class="preview-value">{{ previewPercentage }}%</span>
            </div>
            <div class="preview-note">
              {{ t("keys.weightPreviewNote") }}
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
.edit-weight-modal {
  width: 500px;
}

.form-section {
  margin-top: 0;
}

.sub-group-info {
  margin-bottom: 24px;
  padding: 16px;
  background: var(--bg-secondary);
  border-radius: var(--border-radius-md);
  border: 1px solid var(--border-color);
}

.section-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 12px;
}

.group-name {
  color: var(--primary-color);
  font-weight: 600;
}

.group-details {
  display: flex;
  flex-direction: row;
  gap: 24px;
  align-items: center;
}

.detail-item {
  font-size: 0.9rem;
  color: var(--text-secondary);
}

.detail-item strong {
  color: var(--text-primary);
}

.weight-input-section {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.quick-adjust {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.preview-section {
  margin-top: 16px;
  padding: 16px;
  background: var(--bg-tertiary);
  border-radius: var(--border-radius-sm);
  border: 1px solid var(--border-color);
}

.preview-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.preview-label {
  font-weight: 600;
  color: var(--text-primary);
}

.preview-value {
  font-size: 1.1rem;
  font-weight: 700;
  color: var(--primary-color);
}

.preview-note {
  font-size: 0.85rem;
  color: var(--text-tertiary);
  font-style: italic;
}

/* 响应式适配 */
@media (max-width: 768px) {
  .edit-weight-modal {
    width: 90vw;
  }

  .weight-input-section {
    flex-direction: column;
    align-items: stretch;
  }

  .quick-adjust {
    justify-content: center;
  }

  .preview-item {
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
  }
}

/* 暗黑模式适配 */
:root.dark .sub-group-info {
  background: var(--bg-tertiary);
  border-color: var(--border-color);
}

:root.dark .preview-section {
  background: var(--bg-secondary);
  border-color: var(--border-color);
}
</style>
