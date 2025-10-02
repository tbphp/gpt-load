import { reactive } from "vue";
import { useMessage, createDiscreteApi } from "naive-ui";
import { useI18n } from "vue-i18n";

/**
 * 错误处理状态
 */
interface ErrorState {
  hasError: boolean;
  message: string;
  type: "network" | "data" | "permission" | "server" | "unknown";
  timestamp: Date;
  retryCount: number;
}

/**
 * 错误处理相关的工具函数
 */
export function useErrorHandling() {
  const injected = (() => { try { return useMessage(); } catch { return null; } })();
  const discrete = typeof window !== "undefined" ? createDiscreteApi(["message"]) : null;
  // unify; when neither is available (SSR/tests), use a no-op
  const message = injected ?? discrete?.message ?? { error: (_msg: string) => {} };
  // i18n may be unavailable in SSR/tests; guard and provide fallback
  const i18n = (() => { try { return useI18n(); } catch { return null; } })();
  const tt = (key: string, fallback: string): string => {
    try {
      const s = i18n?.t ? (i18n.t as any)(key) as string : key;
      return s && s !== key ? s : fallback;
    } catch {
      return fallback;
    }
  };
  const errorState = reactive<ErrorState>({
    hasError: false,
    message: "",
    type: "unknown",
    timestamp: new Date(),
    retryCount: 0,
  });

  /**
   * 设置错误状态
   * @param error 错误对象或消息
   * @param type 错误类型
   */
  const setError = (
    error: Error | string,
    type: ErrorState["type"] = "unknown"
  ) => {
    errorState.hasError = true;
    errorState.message =
      typeof error === "string"
        ? error
        : (error && (error as any).message) || String(error);
    errorState.type = type;
    errorState.timestamp = new Date();
  };

  /**
   * 清除错误状态
   */
  const clearError = () => {
    errorState.hasError = false;
    errorState.message = "";
    errorState.retryCount = 0;
    errorState.type = "unknown";
    errorState.timestamp = new Date();
  };

  /**
   * 增加重试次数
   */
  const incrementRetry = () => {
    errorState.retryCount++;
  };

  /**
   * 错误类型判断
   */
  const isNetworkError = (error: any): boolean => {
    if (!error) return false;
    
    // 常见网络错误检查
    return (
      error.code === "NETWORK_ERROR" ||
      error.code === "ERR_NETWORK" ||
      error.code === "ECONNABORTED" || // axios 超时
      /timeout/i.test(error.message ?? "") ||
      error.name === "AbortError" || // fetch 中止
      error.message?.includes("Network Error") ||
      error.message?.includes("Failed to fetch") ||
      error.message?.includes("fetch") ||
      (typeof navigator !== "undefined" && !navigator.onLine)
    );
  };

  const isPermissionError = (error: any): boolean => {
    if (!error) return false;
    
    const status = error.response?.status || error.status;
    return status === 401 || status === 403;
  };

  const isDataError = (error: any): boolean => {
    if (!error) return false;
    
    const status = error.response?.status || error.status;
    return status >= 400 && status < 500 && status !== 401 && status !== 403;
  };

  const isServerError = (error: any): boolean => {
    if (!error) return false;
    const status = error.response?.status || error.status;
    return status >= 500;
  };

  /**
   * 统一的错误处理函数
   * @param error 错误对象
   * @param options 处理选项
   */
  const handleError = (
    error: any,
    options: {
      showMessage?: boolean;
      logError?: boolean;
      retryable?: boolean;
      fallbackMessage?: string;
    } = {}
  ) => {
    const {
      showMessage = true,
      logError = true,
      retryable = false,
      fallbackMessage = tt("error.generic", "操作失败，请稍后重试"),
    } = options;

    if (logError) {
      console.error("Error handled:", error);
    }

    let errorType: ErrorState["type"] = "unknown";
    let userMessage = fallbackMessage;

    if (isNetworkError(error)) {
      errorType = "network";
      userMessage = tt("error.network", "网络连接失败，请检查网络连接");
    } else if (isPermissionError(error)) {
      errorType = "permission";
      userMessage = tt("error.permission", "权限不足或登录已过期");
    } else if (isDataError(error)) {
      errorType = "data";
      userMessage = error.response?.data?.message || tt("error.data", "数据请求失败");
    } else if (isServerError(error)) {
      errorType = "server";
      userMessage = tt("error.server", "服务异常，请稍后重试");
    }

    setError(userMessage, errorType);

    if (showMessage) {
      message.error(userMessage);
    }

    return {
      type: errorType,
      message: userMessage,
      retryable,
    };
  };

  /**
   * 带重试机制的异步操作包装器
   * @param fn 异步函数
   * @param maxRetries 最大重试次数
   * @param retryDelay 重试延迟（毫秒）
   * @param options 重试选项
   */
  const withRetry = async <T>(
    fn: () => Promise<T>,
    maxRetries = 2,
    retryDelay = 1000,
    options: {
      shouldRetry?: (err: any) => boolean;
      factor?: number;
      jitter?: boolean;
    } = {}
  ): Promise<T> => {
    const {
      shouldRetry = (err: any) => isNetworkError(err),
      factor = 2,
      jitter = true,
    } = options;
    
    // 重置重试计数
    errorState.retryCount = 0;
    let lastError: any;

    for (let i = 0; i <= maxRetries; i++) {
      try {
        const result = await fn();
        // 一旦调用成功，无论是否经历重试都应清除之前的错误状态
        clearError();
        return result;
      } catch (error) {
        lastError = error;
        
        if (i < maxRetries) {
          // 检查是否应该重试
          if (shouldRetry(error)) {
            // 即将发起重试时再增加计数
            incrementRetry();
            // 计算退避延迟时间（指数退避 + 可选抖动）
            const base = retryDelay * Math.pow(factor, i);
            const wait = jitter ? base * (0.5 + Math.random() * 0.5) : base;
            await new Promise((resolve) => setTimeout(resolve, wait));
            continue;
          }
        }
        
        break;
      }
    }

    handleError(lastError, { retryable: maxRetries > 0 });
    throw lastError;
  };

  /**
   * 安全的异步操作包装器（不抛出异常）
   * @param fn 异步函数
   * @param fallback 失败时的默认值
   */
  const safeAsync = async <T>(
    fn: () => Promise<T>,
    fallback: T
  ): Promise<T> => {
    try {
      const result = await fn();
      clearError();
      return result;
    } catch (error) {
      handleError(error, { showMessage: false });
      return fallback;
    }
  };

  /**
   * 获取用户友好的错误消息
   * @param error 错误对象
   */
  const getFriendlyErrorMessage = (error: any): string => {
    if (isNetworkError(error)) {
      return tt("error.networkRetry", "网络连接失败，请检查网络连接后重试");
    }
    
    if (isPermissionError(error)) {
      return tt("error.permissionReauth", "权限不足或登录已过期，请重新登录");
    }
    
    if (error?.response?.data?.message) {
      return error.response.data.message;
    }
    
    if (error?.message) {
      return error.message;
    }
    
    return tt("error.generic", "操作失败，请稍后重试");
  };

  return {
    errorState,
    setError,
    clearError,
    incrementRetry,
    handleError,
    withRetry,
    safeAsync,
    getFriendlyErrorMessage,
    isNetworkError,
    isPermissionError,
    isDataError,
    isServerError,
  };
}