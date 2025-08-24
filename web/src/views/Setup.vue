<script setup lang="ts">
import http from "@/utils/http";
import { useMessage } from "naive-ui";
import { computed, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const message = useMessage();
const formRef = ref();

const formData = reactive({
  password: "",
  confirmPassword: "",
});

const isSubmitting = ref(false);
const strengthResult = ref<any>(null);
const strengthCheckTimeout = ref<number>();

// 密码强度条
const strengthBars = computed(() => {
  const bars = [
    { active: false, level: "weak" },
    { active: false, level: "weak" },
    { active: false, level: "medium" },
    { active: false, level: "strong" },
  ];

  if (!strengthResult.value) {
    return bars;
  }

  const strength = strengthResult.value.strength;
  switch (strength) {
    case 0: // 弱
      bars[0].active = true;
      break;
    case 1: // 中等
      bars[0].active = true;
      bars[1].active = true;
      break;
    case 2: // 强
      bars[0].active = true;
      bars[1].active = true;
      bars[2].active = true;
      break;
    case 3: // 非常强
      bars.forEach(bar => (bar.active = true));
      break;
  }

  return bars;
});

// 表单验证规则
const formRules = {
  password: [
    { required: true, message: "请输入密码", trigger: "blur" },
    { min: 8, message: "密码长度至少8个字符", trigger: "blur" },
  ],
  confirmPassword: [
    { required: true, message: "请确认密码", trigger: "blur" },
    {
      validator: (_rule: unknown, value: string) => {
        return value === formData.password;
      },
      message: "两次输入的密码不一致",
      trigger: "blur",
    },
  ],
};

// 是否可以提交
const canSubmit = computed(() => {
  return (
    formData.password.length >= 8 &&
    formData.password === formData.confirmPassword &&
    strengthResult.value?.is_valid
  );
});

// 检查密码强度
const checkPasswordStrength = () => {
  if (!formData.password) {
    strengthResult.value = null;
    return;
  }

  // 防抖处理
  if (strengthCheckTimeout.value) {
    clearTimeout(strengthCheckTimeout.value);
  }

  strengthCheckTimeout.value = window.setTimeout(async () => {
    try {
      const response = await http.post("/setup/password-strength", {
        password: formData.password,
      });
      strengthResult.value = response; // http拦截器已经返回了response.data
    } catch (error) {
      console.error("密码强度检查失败:", error);
      // 显示错误信息
      strengthResult.value = {
        strength: 0,
        score: 0,
        is_valid: false,
        message: "无法连接到服务器，请检查网络连接",
        suggestions: [],
      };
    }
  }, 300);
};

// 提交表单
const handleSubmit = async () => {
  try {
    await formRef.value?.validate();

    isSubmitting.value = true;

    await http.post("/setup/initial-password", {
      password: formData.password,
      confirm_password: formData.confirmPassword,
    });

    message.success("管理员密码设置成功！");

    // 跳转到登录页面
    setTimeout(() => {
      router.push("/login");
    }, 1000);
  } catch (error: any) {
    console.error("设置密码失败:", error);
    const errorMessage = error?.response?.data?.message || "设置密码失败，请重试";
    message.error(errorMessage);
  } finally {
    isSubmitting.value = false;
  }
};

// 监听密码变化，重置强度检查
watch(
  () => formData.password,
  () => {
    strengthResult.value = null;
  }
);
</script>

<template>
  <div class="setup-container">
    <div class="setup-card">
      <div class="setup-header">
        <div class="logo">
          <h1>GPT-Load</h1>
        </div>
        <h2>初始化设置</h2>
        <p class="setup-description">
          欢迎使用 GPT-Load！首次启动需要设置管理员密码来保护您的系统。
        </p>
      </div>

      <div class="setup-form">
        <n-form
          ref="formRef"
          :model="formData"
          :rules="formRules"
          label-placement="top"
          size="large"
        >
          <n-form-item label="管理员密码" path="password">
            <n-input
              v-model:value="formData.password"
              type="password"
              placeholder="请输入管理员密码"
              show-password-on="click"
              @input="checkPasswordStrength"
            />
          </n-form-item>

          <n-form-item label="确认密码" path="confirmPassword">
            <n-input
              v-model:value="formData.confirmPassword"
              type="password"
              placeholder="请再次输入密码"
              show-password-on="click"
            />
          </n-form-item>

          <n-form-item>
            <n-button
              type="primary"
              size="large"
              block
              :loading="isSubmitting"
              :disabled="!canSubmit"
              @click="handleSubmit"
            >
              设置密码并完成初始化
            </n-button>
          </n-form-item>
        </n-form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.setup-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 20px;
}

.setup-card {
  background: white;
  border-radius: 12px;
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.2);
  padding: 40px;
  width: 100%;
  max-width: 480px;
}

.setup-header {
  text-align: center;
  margin-bottom: 32px;
}

.logo h1 {
  color: #667eea;
  font-size: 28px;
  font-weight: bold;
  margin: 0 0 8px 0;
}

.setup-header h2 {
  color: #333;
  font-size: 24px;
  margin: 0 0 12px 0;
}

.setup-description {
  color: #666;
  font-size: 14px;
  line-height: 1.5;
  margin: 0;
}

.setup-form {
  margin-top: 20px;
}

.password-strength {
  margin: 12px 0 20px 0;
  padding: 16px;
  background: #f8f9fa;
  border-radius: 8px;
}

.strength-label {
  font-size: 14px;
  color: #333;
  margin-bottom: 8px;
  font-weight: 500;
}

.strength-bars {
  display: flex;
  gap: 4px;
  margin-bottom: 8px;
}

.strength-bar {
  height: 4px;
  flex: 1;
  background: #e0e0e0;
  border-radius: 2px;
  transition: all 0.3s ease;
}

.strength-bar.active.weak {
  background: #f56565;
}

.strength-bar.active.medium {
  background: #ed8936;
}

.strength-bar.active.strong {
  background: #38a169;
}

.strength-text {
  font-size: 13px;
  font-weight: 500;
}

.strength-text.passwordweak {
  color: #f56565;
}

.strength-text.passwordmedium {
  color: #ed8936;
}

.strength-text.passwordstrong,
.strength-text.passwordverystrong {
  color: #38a169;
}

.password-suggestions {
  margin-top: 12px;
}

.suggestions-title {
  font-size: 13px;
  color: #666;
  font-weight: 500;
  margin-bottom: 4px;
}

.password-suggestions ul {
  margin: 0;
  padding-left: 16px;
}

.password-suggestions li {
  font-size: 12px;
  color: #666;
  line-height: 1.4;
  margin-bottom: 2px;
}
</style>
