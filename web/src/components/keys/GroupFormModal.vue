<script setup lang="ts">
import { keysApi } from "@/api/keys";
import { settingsApi } from "@/api/settings";
import type { Group, GroupConfigOption, UpstreamInfo } from "@/types/models";
import { Add, Close, Remove } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NCollapse,
  NCollapseItem,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  useMessage,
  type FormRules,
} from "naive-ui";
import { reactive, ref, watch } from "vue";

interface Props {
  show: boolean;
  group?: Group | null;
}

interface Emits {
  (e: "update:show", value: boolean): void;
  (e: "success", value: Group): void;
}

// 配置项类型
interface ConfigItem {
  key: string;
  value: number | string;
}

const props = withDefaults(defineProps<Props>(), {
  group: null,
});

const emit = defineEmits<Emits>();

const message = useMessage();
const loading = ref(false);
const formRef = ref();

// 表单数据接口
interface GroupFormData {
  name: string;
  display_name: string;
  description: string;
  upstreams: UpstreamInfo[];
  channel_type: "openai" | "gemini";
  sort: number;
  test_model: string;
  param_overrides: string;
  config: Record<string, number>;
  configItems: ConfigItem[];
}

// 表单数据
const formData = reactive<GroupFormData>({
  name: "",
  display_name: "",
  description: "",
  upstreams: [
    {
      url: "",
      weight: 1,
    },
  ] as UpstreamInfo[],
  channel_type: "openai",
  sort: 1,
  test_model: "",
  param_overrides: "",
  config: {},
  configItems: [] as ConfigItem[],
});

const channelTypeOptions = ref<{ label: string; value: string }[]>([]);
const configOptions = ref<GroupConfigOption[]>([]);
const channelTypesFetched = ref(false);
const configOptionsFetched = ref(false);

// 表单验证规则
const rules: FormRules = {
  name: [
    {
      required: true,
      message: "请输入分组名称",
      trigger: ["blur", "input"],
    },
    {
      pattern: /^[a-z0-9_-]{3,30}$/,
      message: "只能包含小写字母、数字、中划线或下划线，长度3-30位",
      trigger: ["blur", "input"],
    },
  ],
  channel_type: [
    {
      required: true,
      message: "请选择渠道类型",
      trigger: ["blur", "change"],
    },
  ],
  test_model: [
    {
      required: true,
      message: "请输入测试模型",
      trigger: ["blur", "input"],
    },
  ],
  upstreams: [
    {
      type: "array",
      min: 1,
      message: "至少需要一个上游地址",
      trigger: ["blur", "change"],
    },
  ],
};

// 监听弹窗显示状态
watch(
  () => props.show,
  show => {
    if (show) {
      if (!channelTypesFetched.value) {
        fetchChannelTypes();
      }
      if (!configOptionsFetched.value) {
        fetchGroupConfigOptions();
      }
      resetForm();
      if (props.group) {
        loadGroupData();
      }
    }
  }
);

// 重置表单
function resetForm() {
  Object.assign(formData, {
    name: "",
    display_name: "",
    description: "",
    upstreams: [{ url: "", weight: 1 }],
    channel_type: "openai",
    sort: 1,
    test_model: "",
    param_overrides: "",
    config: {},
    configItems: [],
  });
}

// 加载分组数据（编辑模式）
function loadGroupData() {
  if (!props.group) {
    return;
  }

  const configItems = Object.entries(props.group.config || {}).map(([key, value]) => ({
    key,
    value,
  }));
  Object.assign(formData, {
    name: props.group.name || "",
    display_name: props.group.display_name || "",
    description: props.group.description || "",
    upstreams: props.group.upstreams?.length
      ? [...props.group.upstreams]
      : [{ url: "", weight: 1 }],
    channel_type: props.group.channel_type || "openai",
    sort: props.group.sort || 1,
    test_model: props.group.test_model || "",
    param_overrides: JSON.stringify(props.group.param_overrides || {}, null, 2),
    config: {},
    configItems,
  });
}

async function fetchChannelTypes() {
  const options = (await settingsApi.getChannelTypes()) || [];
  channelTypeOptions.value =
    options?.map((type: string) => ({
      label: type,
      value: type,
    })) || [];
  channelTypesFetched.value = true;
}

// 添加上游地址
function addUpstream() {
  formData.upstreams.push({
    url: "",
    weight: 1,
  });
}

// 删除上游地址
function removeUpstream(index: number) {
  if (formData.upstreams.length > 1) {
    formData.upstreams.splice(index, 1);
  }
}

async function fetchGroupConfigOptions() {
  const options = await keysApi.getGroupConfigOptions();
  configOptions.value = options || [];
  configOptionsFetched.value = true;
}

// 添加配置项
function addConfigItem() {
  formData.configItems.push({
    key: "",
    value: 0,
  });
}

// 删除配置项
function removeConfigItem(index: number) {
  formData.configItems.splice(index, 1);
}

// 当配置项的key改变时，设置默认值
function handleConfigKeyChange(index: number, key: string) {
  const option = configOptions.value.find(opt => opt.key === key);
  if (option) {
    formData.configItems[index].value = option.default_value;
  }
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

    loading.value = true;

    // 验证 JSON 格式
    let paramOverrides = {};
    if (formData.param_overrides) {
      try {
        paramOverrides = JSON.parse(formData.param_overrides);
      } catch {
        message.error("参数覆盖必须是有效的 JSON 格式");
        return;
      }
    }

    // 将configItems转换为config对象
    const config: Record<string, number | string> = {};
    formData.configItems.forEach((item: ConfigItem) => {
      if (item.key && item.key.trim()) {
        config[item.key] = item.value;
      }
    });

    // 构建提交数据
    const submitData = {
      name: formData.name,
      display_name: formData.display_name,
      description: formData.description,
      upstreams: formData.upstreams.filter((upstream: UpstreamInfo) => upstream.url.trim()),
      channel_type: formData.channel_type,
      sort: formData.sort,
      test_model: formData.test_model,
      param_overrides: formData.param_overrides ? paramOverrides : undefined,
      config,
    };

    let res: Group;
    if (props.group?.id) {
      // 编辑模式
      res = await keysApi.updateGroup(props.group.id, submitData);
    } else {
      // 新建模式
      res = await keysApi.createGroup(submitData);
    }

    emit("success", res);
    handleClose();
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="handleClose" class="group-form-modal">
    <n-card
      style="width: 800px"
      :title="group ? '编辑分组' : '创建分组'"
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
      >
        <!-- 基础信息 -->
        <div class="form-section">
          <h4 class="section-title">基础信息</h4>

          <n-form-item label="分组名称" path="name">
            <n-input
              v-model:value="formData.name"
              placeholder="作为路由的一部分，如：gemini-pro-group"
            />
          </n-form-item>

          <n-form-item label="显示名称" path="display_name">
            <n-input v-model:value="formData.display_name" placeholder="可选，用于显示的友好名称" />
          </n-form-item>

          <n-form-item label="渠道类型" path="channel_type">
            <n-select
              v-model:value="formData.channel_type"
              :options="channelTypeOptions"
              placeholder="请选择渠道类型"
            />
          </n-form-item>

          <n-form-item label="测试模型" path="test_model">
            <n-input v-model:value="formData.test_model" placeholder="如：gpt-3.5-turbo" />
          </n-form-item>

          <n-form-item label="排序" path="sort">
            <n-input-number
              v-model:value="formData.sort"
              :min="0"
              placeholder="排序值，数字越小越靠前"
            />
          </n-form-item>

          <n-form-item label="描述" path="description">
            <n-input
              v-model:value="formData.description"
              type="textarea"
              placeholder="可选，分组描述信息"
              :rows="2"
              :autosize="{ minRows: 2, maxRows: 2 }"
              style="resize: none"
            />
          </n-form-item>
        </div>

        <!-- 上游地址 -->
        <div class="form-section" style="margin-top: 10px">
          <h4 class="section-title">上游地址</h4>

          <n-form-item
            v-for="(upstream, index) in formData.upstreams"
            :key="index"
            :label="`上游 ${index + 1}`"
            :path="`upstreams[${index}].url`"
            :rule="{
              required: true,
              message: '',
              trigger: ['blur', 'input'],
            }"
          >
            <div class="flex items-center gap-2" style="width: 100%">
              <n-input
                v-model:value="upstream.url"
                placeholder="https://api.openai.com"
                style="flex: 1"
              />
              <span class="form-label">权重</span>
              <n-input-number
                v-model:value="upstream.weight"
                :min="1"
                placeholder="权重"
                style="width: 100px"
              />
              <div style="width: 40px">
                <n-button
                  v-if="formData.upstreams.length > 1"
                  @click="removeUpstream(index)"
                  type="error"
                  quaternary
                  circle
                  style="margin-left: 10px"
                >
                  <template #icon>
                    <n-icon :component="Remove" />
                  </template>
                </n-button>
              </div>
            </div>
          </n-form-item>

          <n-form-item>
            <n-button @click="addUpstream" dashed style="width: 100%">
              <template #icon>
                <n-icon :component="Add" />
              </template>
              添加上游地址
            </n-button>
          </n-form-item>
        </div>

        <!-- 高级配置 -->
        <div class="form-section" style="margin-top: 10px">
          <n-collapse>
            <n-collapse-item title="高级配置" name="advanced">
              <div class="config-section">
                <h5 class="config-title">分组配置</h5>

                <div class="config-items">
                  <n-form-item
                    v-for="(configItem, index) in formData.configItems"
                    :key="index"
                    class="flex config-item"
                    :label="`配置 ${index + 1}`"
                    :path="`configItems[${index}].key`"
                    :rule="{
                      required: true,
                      message: '',
                      trigger: ['blur', 'change'],
                    }"
                  >
                    <div class="flex items-center" style="width: 100%">
                      <n-select
                        v-model:value="configItem.key"
                        :options="
                          configOptions.map(opt => ({
                            label: opt.name,
                            value: opt.key,
                            disabled:
                              formData.configItems
                                .map((item: ConfigItem) => item.key)
                                ?.includes(opt.key) && opt.key !== configItem.key,
                          }))
                        "
                        placeholder="请选择配置参数"
                        style="min-width: 200px"
                        @update:value="value => handleConfigKeyChange(index, value)"
                        clearable
                      />
                      <n-input
                        v-if="
                          typeof configOptions.find(opt => opt.key === configItem.key)
                            ?.default_value === 'string'
                        "
                        v-model:value="configItem.value as string"
                        placeholder="参数值"
                        style="width: 180px; margin-left: 15px"
                      />
                      <n-input-number
                        v-else
                        v-model:value="configItem.value as number"
                        placeholder="参数值"
                        style="width: 180px; margin-left: 15px"
                        :precision="0"
                      />
                      <n-button
                        @click="removeConfigItem(index)"
                        type="error"
                        quaternary
                        circle
                        size="small"
                        style="margin-left: 10px"
                      >
                        <template #icon>
                          <n-icon :component="Remove" />
                        </template>
                      </n-button>
                    </div>
                  </n-form-item>
                </div>

                <div style="margin-top: 12px; padding-left: 120px">
                  <n-button
                    @click="addConfigItem"
                    dashed
                    style="width: 100%"
                    :disabled="formData.configItems.length >= configOptions.length"
                  >
                    <template #icon>
                      <n-icon :component="Add" />
                    </template>
                    添加配置参数
                  </n-button>
                </div>
              </div>
              <div class="config-section">
                <h5 class="config-title">参数覆盖</h5>
                <div class="config-items">
                  <n-form-item path="param_overrides">
                    <n-input
                      v-model:value="formData.param_overrides"
                      type="textarea"
                      placeholder="JSON 格式的参数覆盖配置"
                      :rows="4"
                    />
                  </n-form-item>
                </div>
              </div>
            </n-collapse-item>
          </n-collapse>
        </div>
      </n-form>

      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 12px">
          <n-button @click="handleClose">取消</n-button>
          <n-button type="primary" @click="handleSubmit" :loading="loading">
            {{ group ? "更新" : "创建" }}
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.group-form-modal {
  --n-color: rgba(255, 255, 255, 0.95);
}

.form-section {
  margin-top: 20px;
}

.section-title {
  font-size: 1rem;
  font-weight: 600;
  color: #374151;
  margin: 0 0 16px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid rgba(102, 126, 234, 0.1);
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

:deep(.n-form-item-feedback-wrapper) {
  min-height: 10px;
}

.config-section {
  margin-top: 16px;
}

.config-title {
  font-size: 0.9rem;
  font-weight: 600;
  color: #374151;
  margin: 0 0 12px 0;
}

.form-label {
  margin-left: 25px;
  margin-right: 10px;
  height: 34px;
  line-height: 34px;
  font-weight: 500;
}

.config-item {
  margin-bottom: 12px;
}
:deep(.n-base-selection-label) {
  height: 40px;
}
</style>
