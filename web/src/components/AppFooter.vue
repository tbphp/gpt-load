<script setup lang="ts">
import { versionService, type VersionInfo } from "@/services/version";
import {
  BugOutline,
  ChatbubbleOutline,
  CheckmarkCircleOutline,
  DocumentTextOutline,
  LogoGithub,
  PeopleOutline,
  TimeOutline,
  WarningOutline,
} from "@vicons/ionicons5";
import { NIcon, NTooltip } from "naive-ui";
import { onMounted, ref } from "vue";

const versionInfo = ref<VersionInfo>({
  currentVersion: "0.1.0",
  latestVersion: null,
  isLatest: false,
  hasUpdate: false,
  releaseUrl: null,
  lastCheckTime: 0,
  status: "checking",
});

const isChecking = ref(false);

// 版本状态配置
const statusConfig = {
  checking: {
    color: "#0066cc",
    icon: TimeOutline,
    text: "检查中...",
  },
  latest: {
    color: "#18a058",
    icon: CheckmarkCircleOutline,
    text: "最新版本",
  },
  "update-available": {
    color: "#f0a020",
    icon: WarningOutline,
    text: "有更新",
  },
  error: {
    color: "#d03050",
    icon: WarningOutline,
    text: "检查失败",
  },
};

const formatVersion = (version: string): string => {
  return version.startsWith("v") ? version : `v${version}`;
};

const checkVersion = async () => {
  if (isChecking.value) {
    return;
  }

  isChecking.value = true;
  try {
    const result = await versionService.checkForUpdates();
    versionInfo.value = result;
  } catch (error) {
    console.warn("Version check failed:", error);
  } finally {
    isChecking.value = false;
  }
};

const handleVersionClick = () => {
  if (
    (versionInfo.value.status === "update-available" || versionInfo.value.status === "latest") &&
    versionInfo.value.releaseUrl
  ) {
    window.open(versionInfo.value.releaseUrl, "_blank", "noopener,noreferrer");
  }
};

onMounted(() => {
  checkVersion();
});
</script>

<template>
  <footer class="app-footer">
    <div class="footer-container">
      <!-- 主要信息区 -->
      <div class="footer-main">
        <span class="project-info">
          <a href="https://github.com/tbphp/gpt-load" target="_blank" rel="noopener noreferrer">
            <b>GPT-Load</b>
          </a>
        </span>

        <n-divider vertical />

        <!-- 版本信息 -->
        <div
          class="version-container"
          :class="{
            'version-clickable':
              versionInfo.status === 'update-available' || versionInfo.status === 'latest',
            'version-checking': isChecking,
          }"
          @click="handleVersionClick"
        >
          <n-icon
            v-if="statusConfig[versionInfo.status].icon"
            :component="statusConfig[versionInfo.status].icon"
            :color="statusConfig[versionInfo.status].color"
            :size="14"
            class="version-icon"
          />
          <span class="version-text">
            {{ formatVersion(versionInfo.currentVersion) }}
            -
            <span :style="{ color: statusConfig[versionInfo.status].color }">
              {{ statusConfig[versionInfo.status].text }}
              <template v-if="versionInfo.status === 'update-available'">
                [{{ formatVersion(versionInfo.latestVersion || "") }}]
              </template>
            </span>
          </span>
        </div>

        <n-divider vertical />

        <!-- 链接区 -->
        <div class="links-container">
          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <a
                href="https://www.gpt-load.com/docs"
                target="_blank"
                rel="noopener noreferrer"
                class="footer-link"
              >
                <n-icon :component="DocumentTextOutline" :size="14" class="link-icon" />
                <span>文档</span>
              </a>
            </template>
            官方文档
          </n-tooltip>

          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <a
                href="https://github.com/tbphp/gpt-load"
                target="_blank"
                rel="noopener noreferrer"
                class="footer-link"
              >
                <n-icon :component="LogoGithub" :size="14" class="link-icon" />
                <span>GitHub</span>
              </a>
            </template>
            查看源码
          </n-tooltip>

          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <a
                href="https://github.com/tbphp/gpt-load/issues"
                target="_blank"
                rel="noopener noreferrer"
                class="footer-link"
              >
                <n-icon :component="BugOutline" :size="14" class="link-icon" />
                <span>反馈</span>
              </a>
            </template>
            问题反馈
          </n-tooltip>

          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <a
                href="https://github.com/tbphp/gpt-load/graphs/contributors"
                target="_blank"
                rel="noopener noreferrer"
                class="footer-link"
              >
                <n-icon :component="PeopleOutline" :size="14" class="link-icon" />
                <span>贡献者</span>
              </a>
            </template>
            查看贡献者
          </n-tooltip>

          <n-tooltip trigger="hover" placement="top">
            <template #trigger>
              <a
                href="https://t.me/+GHpy5SwEllg3MTUx"
                target="_blank"
                rel="noopener noreferrer"
                class="footer-link"
              >
                <n-icon :component="ChatbubbleOutline" :size="14" class="link-icon" />
                <span>Telegram</span>
              </a>
            </template>
            加入群组
          </n-tooltip>
        </div>

        <n-divider vertical />

        <!-- 版权信息 -->
        <div class="copyright-container">
          <span class="copyright-text">
            © 2025 by
            <a
              href="https://github.com/tbphp"
              target="_blank"
              rel="noopener noreferrer"
              class="author-link"
            >
              tbphp
            </a>
          </span>
          <span class="license-text">MIT License</span>
        </div>
      </div>
    </div>
  </footer>
</template>

<style scoped>
.app-footer {
  background: var(--bg-color);
  backdrop-filter: blur(20px);
  border-top: 1px solid rgba(0, 0, 0, 0.08);
  padding: 12px 24px;
  font-size: 14px;
  min-height: 52px;
}

.footer-container {
  max-width: 1200px;
  margin: 0 auto;
}

.footer-main {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
  line-height: 1.4;
}

.project-info {
  color: #666;
  font-weight: 500;
}

.project-info a {
  color: #667eea;
  text-decoration: none;
  font-weight: 600;
}

.project-info a:hover {
  text-decoration: underline;
}

/* 版本信息区域 */
.version-container {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border-radius: 6px;
  transition: all 0.2s ease;
}

.version-icon {
  display: flex;
  align-items: center;
}

.version-text {
  font-weight: 500;
  font-size: 13px;
  color: #666;
  white-space: nowrap;
}

.version-clickable {
  cursor: pointer;
}

.version-clickable:hover {
  background: rgba(240, 160, 32, 0.1);
  transform: translateY(-1px);
}

.version-checking {
  opacity: 0.7;
}

/* 链接区域 */
.links-container {
  display: flex;
  align-items: center;
  gap: 12px;
}

.footer-link {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #666;
  text-decoration: none;
  padding: 4px 6px;
  border-radius: 4px;
  transition: all 0.2s ease;
  font-size: 13px;
  white-space: nowrap;
}

.footer-link:hover {
  color: var(--primary-color, #18a058);
  background: rgba(24, 160, 88, 0.1);
  transform: translateY(-1px);
}

.link-icon {
  display: flex;
  align-items: center;
}

/* 版权信息区域 */
.copyright-container {
  display: flex;
  align-items: center;
  gap: 8px;
}

.copyright-text {
  color: #888;
  font-size: 12px;
}

.license-text {
  color: #888;
  font-size: 12px;
}

.author-link {
  font-weight: 600;
  color: #667eea;
  text-decoration: none;
}

.author-link:hover {
  text-decoration: underline !important;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .app-footer {
    padding: 10px 16px;
    height: auto;
  }

  .footer-main {
    flex-direction: column;
    gap: 8px;
    text-align: center;
  }

  .footer-main :deep(.n-divider) {
    display: none;
  }

  .links-container {
    gap: 16px;
  }
}

@media (max-width: 480px) {
  .footer-main {
    gap: 6px;
  }

  .links-container {
    flex-wrap: wrap;
    justify-content: center;
    gap: 12px;
  }

  .project-info {
    font-size: 12px;
  }

  .footer-link {
    font-size: 12px;
  }
}
</style>
