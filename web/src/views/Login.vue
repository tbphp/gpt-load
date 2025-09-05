<script setup lang="ts">
import AppFooter from "@/components/AppFooter.vue";
import { useAuthService } from "@/services/auth";
import { LockClosedSharp } from "@vicons/ionicons5";
import { NButton, NCard, NInput, NSpace, useMessage } from "naive-ui";
import { ref } from "vue";
import { useRouter } from "vue-router";

const authKey = ref("");
const loading = ref(false);
const router = useRouter();
const message = useMessage();
const { login } = useAuthService();

const handleLogin = async () => {
  if (!authKey.value) {
    message.error("请输入授权密钥");
    return;
  }
  loading.value = true;
  const success = await login(authKey.value);
  loading.value = false;
  if (success) {
    router.push("/");
  }
};
</script>

<template>
  <div class="login-container">
    <div class="login-background">
      <div class="login-decoration" />
      <div class="login-decoration-2" />
    </div>

    <div class="login-content">
      <div class="login-header">
        <h1 class="login-title">GPT Load</h1>
        <p class="login-subtitle">智能负载均衡管理平台</p>
      </div>

      <n-card class="login-card modern-card" :bordered="false">
        <template #header>
          <div class="card-header">
            <h2 class="card-title">欢迎回来</h2>
            <p class="card-subtitle">请输入您的授权密钥以继续</p>
          </div>
        </template>

        <n-space vertical size="large">
          <n-input
            v-model:value="authKey"
            type="password"
            size="large"
            placeholder="请输入授权密钥"
            class="modern-input"
            @keyup.enter="handleLogin"
          >
            <template #prefix>
              <n-icon :component="LockClosedSharp" />
            </template>
          </n-input>

          <n-button
            class="login-btn modern-button"
            type="primary"
            size="large"
            block
            @click="handleLogin"
            :loading="loading"
            :disabled="loading"
          >
            <template v-if="!loading">
              <span>立即登录</span>
            </template>
          </n-button>
        </n-space>
      </n-card>
    </div>
  </div>
  <app-footer />
</template>

<style scoped>
.login-container {
  min-height: calc(100vh - 52px);
  display: flex;
  justify-content: center;
  align-items: center;
  position: relative;
  overflow: hidden;
  padding: 24px;
}

.login-background {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 0;
}

.login-decoration {
  position: absolute;
  top: -50%;
  right: -20%;
  width: 800px;
  height: 800px;
  background: var(--primary-gradient);
  border-radius: 50%;
  opacity: 0.1;
  animation: float 6s ease-in-out infinite;
}

.login-decoration-2 {
  position: absolute;
  bottom: -50%;
  left: -20%;
  width: 600px;
  height: 600px;
  background: var(--secondary-gradient);
  border-radius: 50%;
  opacity: 0.08;
  animation: float 8s ease-in-out infinite reverse;
}

@keyframes float {
  0%,
  100% {
    transform: translateY(0px) rotate(0deg);
  }
  50% {
    transform: translateY(-20px) rotate(5deg);
  }
}

.login-content {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  padding: 0 20px;
}

.login-header {
  text-align: center;
  margin-bottom: 40px;
}

.login-title {
  font-size: 2.5rem;
  font-weight: 700;
  background: var(--primary-gradient);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  margin-bottom: 8px;
  letter-spacing: -0.5px;
}

.login-subtitle {
  font-size: 1.1rem;
  color: var(--text-secondary);
  margin: 0;
  font-weight: 500;
}

.login-card {
  backdrop-filter: blur(20px);
  border: 1px solid var(--border-color-light);
}

.card-header {
  text-align: center;
  padding-bottom: 8px;
}

.card-title {
  font-size: 1.5rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 8px 0;
}

.card-subtitle {
  font-size: 0.95rem;
  color: var(--text-secondary);
  margin: 0;
}

.login-btn {
  background: var(--primary-gradient);
  border: none;
  font-weight: 600;
  letter-spacing: 0.5px;
  height: 48px;
  font-size: 1rem;
}

.login-btn:hover {
  background: linear-gradient(135deg, #5a6fd8 0%, #6a4190 100%);
  transform: translateY(-1px);
  box-shadow: 0 8px 25px rgba(102, 126, 234, 0.3);
}

:deep(.n-input) {
  --n-border-radius: 12px;
  --n-height: 48px;
}

:deep(.n-input__input-el) {
  font-size: 1rem;
}

:deep(.n-input__prefix) {
  color: var(--text-secondary);
}

:deep(.n-card-header) {
  padding-bottom: 16px;
}

:deep(.n-card__content) {
  padding-top: 0;
}

/* 暗黑模式适配 */
:root.dark .login-decoration {
  opacity: 0.05;
}

:root.dark .login-decoration-2 {
  opacity: 0.03;
}

:root.dark .login-card {
  background: var(--card-bg-solid);
  border: 1px solid rgba(255, 255, 255, 0.08);
}

:root.dark .login-btn:hover {
  background: linear-gradient(135deg, #7c8aac 0%, #8b94c0 100%);
  box-shadow: 0 8px 25px rgba(139, 157, 245, 0.2);
}
</style>
