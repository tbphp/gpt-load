import axios from "axios";
import { createI18n } from "vue-i18n";
import enUS from "./en-US";
import jaJP from "./ja-JP";
import zhCN from "./zh-CN";

// 支持的语言列表
export const SUPPORTED_LOCALES = [
  { key: "zh-CN", label: "中文" },
  { key: "en-US", label: "English" },
  { key: "ja-JP", label: "日本語" },
] as const;

export type Locale = (typeof SUPPORTED_LOCALES)[number]["key"];

// 获取默认语言
function getDefaultLocale(): Locale {
  // 1. 优先使用 localStorage 中保存的语言
  const savedLocale = localStorage.getItem("locale");
  if (savedLocale && SUPPORTED_LOCALES.some(l => l.key === savedLocale)) {
    return savedLocale as Locale;
  }

  // 2. 自动检测浏览器语言
  const browserLang = navigator.language;

  // 精确匹配
  if (SUPPORTED_LOCALES.some(l => l.key === browserLang)) {
    return browserLang as Locale;
  }

  // 模糊匹配（如 zh 匹配 zh-CN）
  const shortLang = browserLang.split("-")[0];
  const matched = SUPPORTED_LOCALES.find(l => l.key.startsWith(shortLang));
  if (matched) {
    return matched.key;
  }

  // 3. 默认中文
  return "zh-CN";
}

// 创建 i18n 实例
const defaultLocale = getDefaultLocale();
const i18n = createI18n({
  legacy: false, // 使用 Composition API 模式
  locale: defaultLocale,
  fallbackLocale: "zh-CN",
  messages: {
    "zh-CN": zhCN,
    "en-US": enUS,
    "ja-JP": jaJP,
  },
});

// 初始化时设置 axios 默认语言
if (axios.defaults.headers) {
  axios.defaults.headers.common["Accept-Language"] = defaultLocale;
}

// 切换语言的辅助函数
export function setLocale(locale: Locale) {
  // 保存到 localStorage
  localStorage.setItem("locale", locale);

  // 更新 axios 的默认 headers
  if (axios.defaults.headers) {
    axios.defaults.headers.common["Accept-Language"] = locale;
  }

  // 刷新页面以确保所有内容（包括后端数据）都使用新语言
  window.location.reload();
}

// 获取当前语言
export function getLocale(): Locale {
  return i18n.global.locale.value as Locale;
}

// 获取当前语言的标签
export function getCurrentLocaleLabel(): string {
  const current = getLocale();
  return SUPPORTED_LOCALES.find(l => l.key === current)?.label || "中文";
}

export default i18n;
