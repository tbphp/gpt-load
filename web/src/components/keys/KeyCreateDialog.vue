<script setup lang="ts">
import { keysApi } from "@/api/keys";
import { appState } from "@/utils/app-state";
import { Close, CloudUploadOutline } from "@vicons/ionicons5";
import { NButton, NCard, NInput, NModal, NUpload, type UploadFileInfo } from "naive-ui";
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";

interface Props {
  show: boolean;
  groupId: number;
  groupName?: string;
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success"): void;
}

const props = defineProps<Props>();

const emit = defineEmits<Emits>();

const { t } = useI18n();

const loading = ref(false);
const keysText = ref("");
const inputMode = ref<"text" | "file">("text");
const fileList = ref<UploadFileInfo[]>([]);

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
  keysText.value = "";
  inputMode.value = "text";
  fileList.value = [];
}

// 关闭弹窗
function handleClose() {
  emit("update:show", false);
}

// 切换输入模式
function toggleInputMode() {
  if (inputMode.value === "text") {
    inputMode.value = "file";
    keysText.value = "";
  } else {
    inputMode.value = "text";
    fileList.value = [];
  }
}

// 文件上传前的检查
function beforeUpload(data: { file: UploadFileInfo; fileList: UploadFileInfo[] }) {
  if (!data.file.name?.endsWith(".txt")) {
    window.$message.error(t("keys.onlyTxtFileSupported"));
    return false;
  }
  return true;
}

// 文件变化处理
function handleFileChange(options: { fileList: UploadFileInfo[] }) {
  fileList.value = options.fileList;
}

// 提交表单
async function handleSubmit() {
  if (loading.value) {
    return;
  }

  if (inputMode.value === "text") {
    if (!keysText.value.trim()) {
      return;
    }
  } else {
    if (fileList.value.length === 0) {
      return;
    }
  }

  try {
    loading.value = true;

    if (inputMode.value === "text") {
      await keysApi.addKeysAsync(props.groupId, keysText.value);
    } else {
      const file = fileList.value[0].file as File;
      await keysApi.addKeysAsync(props.groupId, undefined, file);
    }

    resetForm();
    handleClose();
    window.$message.success(t("keys.importTaskStarted"));
    appState.taskPollingTrigger++;
  } finally {
    loading.value = false;
  }
}

// 计算提交按钮是否可用
function isSubmitDisabled() {
  if (inputMode.value === "text") {
    return !keysText.value.trim();
  } else {
    return fileList.value.length === 0;
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="form-modal">
    <n-card
      style="width: 800px"
      :title="t('keys.addKeysToGroup', { group: groupName || t('keys.currentGroup') })"
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

      <!-- 文本输入模式 -->
      <n-input
        v-if="inputMode === 'text'"
        v-model:value="keysText"
        type="textarea"
        :placeholder="t('keys.enterKeysPlaceholder')"
        :rows="8"
        style="margin-top: 20px"
      />

      <!-- 文件上传模式 -->
      <n-upload
        v-else
        v-model:file-list="fileList"
        :max="1"
        accept=".txt"
        :before-upload="beforeUpload"
        @change="handleFileChange"
        style="margin-top: 20px"
      >
        <div class="upload-area">
          <n-icon size="48" :component="CloudUploadOutline" style="color: #18a058" />
          <div class="upload-text">{{ t("keys.clickOrDragFile") }}</div>
          <div class="upload-hint">{{ t("keys.onlyTxtFileSupported") }}</div>
        </div>
      </n-upload>

      <template #footer>
        <div style="display: flex; justify-content: space-between; align-items: center">
          <n-button @click="toggleInputMode" secondary>
            {{ inputMode === "text" ? t("keys.uploadFile") : t("keys.manualInput") }}
          </n-button>
          <div style="display: flex; gap: 12px">
            <n-button @click="handleClose">{{ t("common.cancel") }}</n-button>
            <n-button
              type="primary"
              @click="handleSubmit"
              :loading="loading"
              :disabled="isSubmitDisabled()"
            >
              {{ t("common.add") }}
            </n-button>
          </div>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.form-modal {
  --n-color: rgba(255, 255, 255, 0.95);
}

:deep(.n-input) {
  --n-border-radius: 6px;
}

:deep(.n-card-header) {
  border-bottom: 1px solid rgba(239, 239, 245, 0.8);
  padding: 10px 20px;
}

:deep(.n-card__content) {
  max-height: calc(100vh - 68px - 61px - 50px);
  overflow-y: auto;
}

:deep(.n-card__footer) {
  border-top: 1px solid rgba(239, 239, 245, 0.8);
  padding: 10px 15px;
}

.upload-area {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
  border: 2px dashed #d9d9d9;
  border-radius: 6px;
  background-color: #fafafa;
  cursor: pointer;
  transition: all 0.3s;
}

.upload-area:hover {
  border-color: #18a058;
  background-color: #f0f9f4;
}

.upload-text {
  margin-top: 12px;
  font-size: 16px;
  color: #333;
}

.upload-hint {
  margin-top: 8px;
  font-size: 14px;
  color: #999;
}
</style>
