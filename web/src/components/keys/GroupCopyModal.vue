<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, GroupCopyStats } from "@/types/models";
import { appState } from "@/utils/app-state";
import { getGroupDisplayName } from "@/utils/display";
import { CopyOutline } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NCheckbox,
  NForm,
  NFormItem,
  NIcon,
  NModal,
  NRadio,
  NRadioGroup,
  useMessage,
} from "naive-ui";
import { computed, ref, watchEffect } from "vue";

interface Props {
  show: boolean;
  sourceGroup: Group | null;
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success", group: Group, stats: GroupCopyStats): void;
}

interface CopyFormData {
  copyConfig: boolean;
  copyAdvancedConfig: boolean;
  copyKeys: "all" | "valid_only" | "none";
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

const message = useMessage();
const loading = ref(false);

const formData = ref<CopyFormData>({
  copyConfig: true,
  copyAdvancedConfig: true,
  copyKeys: "all",
});

const modalVisible = computed({
  get: () => props.show,
  set: (value: boolean) => emit("update:show", value),
});

// Watch for show prop changes to reset form
watchEffect(() => {
  if (props.show) {
    resetForm();
  }
});

function resetForm() {
  formData.value = {
    copyConfig: true,
    copyAdvancedConfig: true,
    copyKeys: "all",
  };
}

// 生成新分组名称
function generateNewGroupName(): string {
  if (!props.sourceGroup) {
    return "";
  }

  const baseName = props.sourceGroup.name;
  return `${baseName}_copy`;
}

async function handleCopy() {
  if (!props.sourceGroup) {
    return;
  }

  loading.value = true;
  try {
    const newName = generateNewGroupName();
    const copyData = {
      new_name: newName,
      display_name: props.sourceGroup.display_name ? `${props.sourceGroup.display_name}_copy` : "",
      description: props.sourceGroup.description || "",
      copy_config: formData.value.copyConfig,
      copy_advanced_config: formData.value.copyAdvancedConfig,
      copy_keys: formData.value.copyKeys,
    };

    if (!props.sourceGroup?.id) {
      message.error("源分组不存在");
      return;
    }
    const result = await keysApi.copyGroup(props.sourceGroup.id, copyData);

    message.success(
      `复制成功！已创建新分组 "${result.group.display_name || result.group.name}"${result.stats.copied_keys_count > 0 ? `，密钥正在后台导入，请稍后查看进度` : ""}`
    );

    // Trigger task polling to show import progress (same as KeyCreateDialog)
    if (result.stats.copied_keys_count > 0) {
      appState.taskPollingTrigger++;
    }

    emit("success", result.group, result.stats);
    modalVisible.value = false;
  } catch (error) {
    console.error(error);
    message.error("复制分组失败，请稍后重试");
  } finally {
    loading.value = false;
  }
}

function handleCancel() {
  modalVisible.value = false;
}

// 复制选项说明
const copyOptionsText = computed(() => {
  const options: string[] = [];

  if (formData.value.copyConfig) {
    options.push("分组配置");
  }

  if (formData.value.copyAdvancedConfig) {
    options.push("高级配置");
  }

  switch (formData.value.copyKeys) {
    case "all":
      options.push("所有密钥");
      break;
    case "valid_only":
      options.push("仅有效密钥");
      break;
    case "none":
      break;
  }

  if (options.length === 0) {
    return "仅复制基础信息";
  }

  return `将复制: ${options.join("、")}`;
});
</script>

<template>
  <n-modal :show="modalVisible" @update:show="handleCancel" class="group-copy-modal">
    <n-card
      class="group-copy-card"
      :title="`复制分组 - ${sourceGroup ? getGroupDisplayName(sourceGroup) : ''}`"
      :bordered="false"
      size="huge"
      role="dialog"
      aria-modal="true"
    >
      <template #header-extra>
        <n-button quaternary circle @click="handleCancel">
          <template #icon>
            <n-icon :component="CopyOutline" />
          </template>
        </n-button>
      </template>

      <div class="modal-content">
        <div class="copy-preview">
          <div class="preview-item">
            <span class="preview-label">新分组名称:</span>
            <code class="preview-value">{{ generateNewGroupName() }}</code>
          </div>
        </div>

        <n-form :model="formData" label-placement="left" label-width="80px" class="group-copy-form">
          <!-- 复制选项 -->
          <div class="copy-options">
            <n-form-item label="分组配置">
              <div class="checkbox-with-description">
                <n-checkbox v-model:checked="formData.copyConfig">复制分组配置</n-checkbox>
                <span class="option-description">包含: 上游地址、渠道类型、测试模型等</span>
              </div>
            </n-form-item>

            <n-form-item label="高级配置">
              <div class="checkbox-with-description">
                <n-checkbox v-model:checked="formData.copyAdvancedConfig">复制高级配置</n-checkbox>
                <span class="option-description">包含: config配置、参数覆盖、代理密钥</span>
              </div>
            </n-form-item>

            <n-form-item label="密钥复制">
              <n-radio-group v-model:value="formData.copyKeys" name="copyKeys">
                <div class="radio-options">
                  <n-radio value="all" class="radio-option">复制所有密钥</n-radio>
                  <n-radio value="valid_only" class="radio-option">仅复制有效密钥</n-radio>
                  <n-radio value="none" class="radio-option">不复制密钥</n-radio>
                </div>
              </n-radio-group>
            </n-form-item>
          </div>

          <!-- 复制摘要 -->
          <div class="copy-summary">
            <n-card size="small" class="summary-card">
              <div class="summary-content">
                <span class="summary-label">复制摘要:</span>
                <span class="summary-text">{{ copyOptionsText }}</span>
              </div>
            </n-card>
          </div>
        </n-form>
      </div>

      <template #footer>
        <div class="modal-actions">
          <n-button @click="handleCancel" :disabled="loading">取消</n-button>
          <n-button type="primary" @click="handleCopy" :loading="loading">
            <template #icon>
              <n-icon :component="CopyOutline" />
            </template>
            确认复制
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.group-copy-modal {
  width: 450px;
  max-width: 90vw;
  --n-color: rgba(255, 255, 255, 0.95);
}

.modal-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.header-icon {
  color: #18a058;
}

.modal-content {
  padding: 0;
}

.copy-preview {
  background: rgba(24, 160, 88, 0.05);
  border: 1px solid rgba(24, 160, 88, 0.2);
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 16px;
}

.preview-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.preview-label {
  font-weight: 500;
  color: #18a058;
}

.preview-value {
  background: rgba(24, 160, 88, 0.1);
  color: #18a058;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 13px;
}

.copy-options {
  margin-bottom: 16px;
}

.checkbox-with-description {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.option-description {
  color: #888;
  font-size: 12px;
  margin-left: 20px;
}

.radio-options {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.radio-option {
  margin: 0;
}

.copy-summary {
  margin-top: 16px;
}

.summary-card {
  background: rgba(24, 160, 88, 0.05);
  border: 1px solid rgba(24, 160, 88, 0.2);
}

.summary-content {
  display: flex;
  align-items: center;
  gap: 8px;
}

.summary-label {
  font-weight: 600;
  color: #18a058;
}

.summary-text {
  color: #333;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

/* 增强表单样式 - 与GroupFormModal保持一致 */
:deep(.n-form-item-label) {
  font-weight: 500;
  color: #374151;
}

:deep(.n-input) {
  --n-border-radius: 8px;
  --n-border: 1px solid #e5e7eb;
  --n-border-hover: 1px solid #667eea;
  --n-border-focus: 1px solid #667eea;
  --n-box-shadow-focus: 0 0 0 2px rgba(102, 126, 234, 0.1);
}

:deep(.n-select) {
  --n-border-radius: 8px;
}

:deep(.n-button) {
  --n-border-radius: 8px;
}

:deep(.n-card-header) {
  border-bottom: 1px solid rgba(239, 239, 245, 0.8);
  padding: 10px 20px;
}

:deep(.n-card__content) {
  padding: 16px 20px;
}

:deep(.n-card__footer) {
  border-top: 1px solid rgba(239, 239, 245, 0.8);
  padding: 10px 15px;
}

:deep(.n-form-item-feedback-wrapper) {
  min-height: 10px;
}

/* 深色模式适配 */
@media (prefers-color-scheme: dark) {
  .summary-text {
    color: #e0e0e0;
  }

  .option-description {
    color: #999;
  }
}
</style>
