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

// Configuration item type
interface ConfigItem {
  key: string;
  value: number;
}

const props = withDefaults(defineProps<Props>(), {
  group: null,
});

const emit = defineEmits<Emits>();

const message = useMessage();
const loading = ref(false);
const formRef = ref();

// Form data interface
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

// Form data
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

// Form validation rules
const rules: FormRules = {
  name: [
    {
      required: true,
      message: "Please enter a group name",
      trigger: ["blur", "input"],
    },
    {
      pattern: /^[a-z0-9_-]{3,30}$/,
      message: "Can only contain lowercase letters, numbers, hyphens, or underscores, length 3-30 characters",
      trigger: ["blur", "input"],
    },
  ],
  channel_type: [
    {
      required: true,
      message: "Please select a channel type",
      trigger: ["blur", "change"],
    },
  ],
  test_model: [
    {
      required: true,
      message: "Please enter a test model",
      trigger: ["blur", "input"],
    },
  ],
  upstreams: [
    {
      type: "array",
      min: 1,
      message: "At least one upstream address is required",
      trigger: ["blur", "change"],
    },
  ],
};

// Monitor dialog display status
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

// Reset form
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

// Load group data (edit mode)
function loadGroupData() {
  if (!props.group) {
    return;
  }

  const configItems = Object.entries(props.group.config || {}).map(([key, value]) => ({
    key,
    value: Number(value) || 0,
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

// Add upstream address
function addUpstream() {
  formData.upstreams.push({
    url: "",
    weight: 1,
  });
}

// Remove upstream address
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

// Add config item
function addConfigItem() {
  formData.configItems.push({
    key: "",
    value: 0,
  });
}

// Remove config item
function removeConfigItem(index: number) {
  formData.configItems.splice(index, 1);
}

// When the config item key changes, set the default value
function handleConfigKeyChange(index: number, key: string) {
  const option = configOptions.value.find(opt => opt.key === key);
  if (option) {
    formData.configItems[index].value = option.default_value || 0;
  }
}

// Close dialog
function handleClose() {
  emit("update:show", false);
}

// Submit form
async function handleSubmit() {
  if (loading.value) {
    return;
  }

  try {
    await formRef.value?.validate();

    loading.value = true;

    // Validate JSON format
    let paramOverrides = {};
    if (formData.param_overrides) {
      try {
        paramOverrides = JSON.parse(formData.param_overrides);
      } catch {
        message.error("Parameter overrides must be valid JSON format");
        return;
      }
    }

    // Convert configItems to config object
    const config: Record<string, number> = {};
    formData.configItems.forEach((item: ConfigItem) => {
      if (item.key && item.key.trim()) {
        config[item.key] = item.value;
      }
    });

    // Build submission data
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
      // Edit mode
      res = await keysApi.updateGroup(props.group.id, submitData);
    } else {
      // Create mode
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
      :title="group ? 'Edit Group' : 'Create Group'"
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
        <!-- Basic Information -->
        <div class="form-section">
          <h4 class="section-title">Basic Information</h4>

          <n-form-item label="Group Name" path="name">
            <n-input
              v-model:value="formData.name"
              placeholder="Used as part of the route, e.g.: gemini-pro-group"
            />
          </n-form-item>

          <n-form-item label="Display Name" path="display_name">
            <n-input v-model:value="formData.display_name" placeholder="Optional, friendly name for display" />
          </n-form-item>

          <n-form-item label="Channel Type" path="channel_type">
            <n-select
              v-model:value="formData.channel_type"
              :options="channelTypeOptions"
              placeholder="Please select channel type"
            />
          </n-form-item>

          <n-form-item label="Test Model" path="test_model">
            <n-input v-model:value="formData.test_model" placeholder="e.g.: gpt-3.5-turbo" />
          </n-form-item>

          <n-form-item label="Sort Order" path="sort">
            <n-input-number
              v-model:value="formData.sort"
              :min="0"
              placeholder="Sort value, smaller numbers appear first"
            />
          </n-form-item>

          <n-form-item label="Description" path="description">
            <n-input
              v-model:value="formData.description"
              type="textarea"
              placeholder="Optional, group description"
              :rows="2"
              :autosize="{ minRows: 2, maxRows: 2 }"
              style="resize: none"
            />
          </n-form-item>
        </div>

        <!-- Upstream Addresses -->
        <div class="form-section" style="margin-top: 10px">
          <h4 class="section-title">Upstream Addresses</h4>

          <n-form-item
            v-for="(upstream, index) in formData.upstreams"
            :key="index"
            :label="`Upstream ${index + 1}`"
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
              <span class="form-label">Weight</span>
              <n-input-number
                v-model:value="upstream.weight"
                :min="1"
                placeholder="Weight"
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
              Add Upstream Address
            </n-button>
          </n-form-item>
        </div>

        <!-- Advanced Configuration -->
        <div class="form-section" style="margin-top: 10px">
          <n-collapse>
            <n-collapse-item title="Advanced Configuration" name="advanced">
              <div class="config-section">
                <h5 class="config-title">Group Configuration</h5>

                <div class="config-items">
                  <n-form-item
                    v-for="(configItem, index) in formData.configItems"
                    :key="index"
                    class="flex config-item"
                    :label="`Config ${index + 1}`"
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
                        placeholder="Please select config parameter"
                        style="min-width: 200px"
                        @update:value="value => handleConfigKeyChange(index, value)"
                        clearable
                      />
                      <n-input-number
                        v-model:value="configItem.value"
                        placeholder="Parameter value"
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
                    Add Config Parameter
                  </n-button>
                </div>
              </div>
              <div class="config-section">
                <h5 class="config-title">Parameter Overrides</h5>
                <div class="config-items">
                  <n-form-item path="param_overrides">
                    <n-input
                      v-model:value="formData.param_overrides"
                      type="textarea"
                      placeholder="JSON format parameter override configuration"
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
          <n-button @click="handleClose">Cancel</n-button>
          <n-button type="primary" @click="handleSubmit" :loading="loading">
            {{ group ? "Update" : "Create" }}
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
