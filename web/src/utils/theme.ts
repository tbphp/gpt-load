import { computed, ref, watch } from "vue";

// 主题模式类型
export type ThemeMode = "auto" | "light" | "dark";
export type ActualTheme = "light" | "dark";

// 存储键名
const THEME_KEY = "gpt-load-theme-mode";

// 获取初始主题模式
function getInitialThemeMode(): ThemeMode {
  const stored = localStorage.getItem(THEME_KEY);
  if (stored && ["auto", "light", "dark"].includes(stored)) {
    return stored as ThemeMode;
  }
  return "auto"; // 默认使用自动模式
}

// 检测系统主题偏好
function getSystemTheme(): ActualTheme {
  if (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) {
    return "dark";
  }
  return "light";
}

// 主题模式（用户选择）
export const themeMode = ref<ThemeMode>(getInitialThemeMode());

// 系统主题（自动检测）
const systemTheme = ref<ActualTheme>(getSystemTheme());

// 实际使用的主题
export const actualTheme = computed<ActualTheme>(() => {
  if (themeMode.value === "auto") {
    return systemTheme.value;
  }
  return themeMode.value as ActualTheme;
});

// 是否为暗黑模式
export const isDark = computed(() => actualTheme.value === "dark");

// 切换主题模式
export function setThemeMode(mode: ThemeMode) {
  themeMode.value = mode;
  localStorage.setItem(THEME_KEY, mode);
}

// 循环切换主题（用于按钮）
export function toggleTheme() {
  const modes: ThemeMode[] = ["auto", "light", "dark"];
  const currentIndex = modes.indexOf(themeMode.value);
  const nextIndex = (currentIndex + 1) % modes.length;
  setThemeMode(modes[nextIndex]);
}

// 监听系统主题变化
if (window.matchMedia) {
  const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");

  // 更新系统主题
  const updateSystemTheme = (e: MediaQueryListEvent | MediaQueryList) => {
    systemTheme.value = e.matches ? "dark" : "light";
  };

  // 添加监听器
  if (mediaQuery.addEventListener) {
    mediaQuery.addEventListener("change", updateSystemTheme);
  } else if (mediaQuery.addListener) {
    // 兼容旧版浏览器
    mediaQuery.addListener(updateSystemTheme as (event: MediaQueryListEvent) => void);
  }
}

// 更新 HTML 根元素的 class（用于 CSS 变量切换）
watch(
  actualTheme,
  theme => {
    const html = document.documentElement;
    if (theme === "dark") {
      html.classList.add("dark");
      html.classList.remove("light");
    } else {
      html.classList.add("light");
      html.classList.remove("dark");
    }
  },
  { immediate: true }
);
