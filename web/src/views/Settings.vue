<script setup lang="ts">
import { settingsApi, type Setting, type SettingCategory } from "@/api/settings";
import ProxyKeysInput from "@/components/common/ProxyKeysInput.vue";
import { HelpCircle, Save } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NForm,
  NFormItem,
  NGrid,
  NGridItem,
  NIcon,
  NInput,
  NInputNumber,
  NSpace,
  NTooltip,
  useMessage,
  type FormItemRule,
} from "naive-ui";
import { ref } from "vue";

const settingList = ref<SettingCategory[]>([]);
const formRef = ref();
const form = ref<Record<string, string | number>>({});
const isSaving = ref(false);
const message = useMessage();

fetchSettings();

async function fetchSettings() {
  try {
    const data = await settingsApi.getSettings();
    settingList.value = data || [];
    initForm();
  } catch (_error) {
    message.error("获取设置失败");
  }
}

function initForm() {
  form.value = settingList.value.reduce((acc: Record<string, string | number>, category) => {
    category.settings?.forEach(setting => {
      acc[setting.key] = setting.value;
    });
    return acc;
  }, {});
}

async function handleSubmit() {
  if (isSaving.value) {
    return;
  }

  try {
    await formRef.value.validate();
    isSaving.value = true;
    await settingsApi.updateSettings(form.value);
    await fetchSettings();
  } finally {
    isSaving.value = false;
  }
}

function generateValidationRules(item: Setting): FormItemRule[] {
  const rules: FormItemRule[] = [];
  if (item.required) {
    const rule: FormItemRule = {
      required: true,
      message: `请输入 ${item.name}`,
      trigger: ["input", "blur"],
    };
    if (item.type === "int") {
      rule.type = "number";
    }
    rules.push(rule);
  }
  if (item.type === "int" && item.min_value !== undefined && item.min_value !== null) {
    rules.push({
      validator: (_rule: FormItemRule, value: number) => {
        if (value === null || value === undefined) {
          return true;
        }
        if (item.min_value !== undefined && item.min_value !== null && value < item.min_value) {
          return new Error(`值不能小于 ${item.min_value}`);
        }
        return true;
      },
      trigger: ["input", "blur"],
    });
  }
  return rules;
}
</script>

<template>
  <n-space vertical>
    <n-form ref="formRef" :model="form" label-placement="top">
      <n-space vertical>
        <n-card
          size="small"
          v-for="category in settingList"
          :key="category.category_name"
          :title="category.category_name"
          hoverable
          bordered
        >
          <n-grid :x-gap="36" :y-gap="0" responsive="screen" cols="1 s:2 m:2 l:3 xl:3">
            <n-grid-item
              v-for="item in category.settings"
              :key="item.key"
              :span="item.key === 'proxy_keys' ? 3 : 1"
            >
              <n-form-item :path="item.key" :rule="generateValidationRules(item)">
                <template #label>
                  <n-space align="center" :size="4" :wrap-item="false">
                    <n-tooltip trigger="hover" placement="top">
                      <template #trigger>
                        <n-icon
                          :component="HelpCircle"
                          :size="16"
                          style="cursor: help; color: #9ca3af"
                        />
                      </template>
                      {{ item.description }}
                    </n-tooltip>
                    <span>{{ item.name }}</span>
                  </n-space>
                </template>

                <n-input-number
                  v-if="item.type === 'int'"
                  v-model:value="form[item.key] as number"
                  :min="
                    item.min_value !== undefined && item.min_value >= 0 ? item.min_value : undefined
                  "
                  placeholder="请输入数值"
                  clearable
                  style="width: 100%"
                  size="small"
                />
                <proxy-keys-input
                  v-else-if="item.key === 'proxy_keys'"
                  v-model="form[item.key] as string"
                  placeholder="请输入内容"
                  size="small"
                />
                <n-input
                  v-else
                  v-model:value="form[item.key] as string"
                  placeholder="请输入内容"
                  clearable
                  size="small"
                />
              </n-form-item>
            </n-grid-item>
          </n-grid>
        </n-card>
      </n-space>
    </n-form>

    <div
      v-if="settingList.length > 0"
      style="display: flex; justify-content: center; padding-top: 12px"
    >
      <n-button
        type="primary"
        size="large"
        :loading="isSaving"
        :disabled="isSaving"
        @click="handleSubmit"
        style="min-width: 200px"
      >
        <template #icon>
          <n-icon :component="Save" />
        </template>
        {{ isSaving ? "保存中..." : "保存设置" }}
      </n-button>
    </div>
  </n-space>
</template>
