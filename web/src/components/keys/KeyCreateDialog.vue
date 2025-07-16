<script setup lang="ts">
import { keysApi } from "@/api/keys";
import { Close } from "@vicons/ionicons5";
import { NButton, NCard, NInput, NModal } from "naive-ui";
import { ref, watch } from "vue";

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

const loading = ref(false);
const keysText = ref("");

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
}

// 关闭弹窗
function handleClose() {
  emit("update:show", false);
}

// 提交表单
async function handleSubmit() {
  if (loading.value || !keysText.value.trim()) {
    return;
  }

  try {
    loading.value = true;

    await keysApi.addMultipleKeys(props.groupId, keysText.value);

    emit("success");
    handleClose();
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="form-modal">
    <n-card
      style="width: 800px"
      :title="`为 ${groupName || '当前分组'} 添加密钥`"
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

      <div style="margin-top: 20px">
        <n-input
          v-model:value="keysText"
          type="textarea"
          placeholder="输入密钥，每行一个&#10;支持指定上游：上游ID@@密钥值&#10;例如：1@@sk-xxx 或 Default@@sk-xxx"
          :rows="8"
        />
        <div style="margin-top: 8px; font-size: 12px; color: #999; line-height: 1.4">
          <div>💡 <strong>使用提示：</strong></div>
          <div>• 直接输入密钥：将使用默认上游</div>
          <div>• 指定上游：使用格式 <code>上游ID@@密钥值</code></div>
          <div>• 示例：<code>1@@sk-xxx</code> 或 <code>Default@@sk-xxx</code></div>
        </div>
      </div>

      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 12px">
          <n-button @click="handleClose">取消</n-button>
          <n-button type="primary" @click="handleSubmit" :loading="loading" :disabled="!keysText">
            创建
          </n-button>
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
</style>
