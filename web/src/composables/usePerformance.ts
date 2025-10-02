import { ref, watch, nextTick, onBeforeUnmount } from "vue";
import type { WatchSource, WatchStopHandle, WatchCallback } from "vue";

/**
 * 性能优化相关的通用工具函数
 */
export function usePerformance() {
  // 存储待执行的优化更新定时器
  const pendingOptimized = new Map<() => void, ReturnType<typeof setTimeout>>();

  // 组件卸载时清理所有待执行的定时器
  onBeforeUnmount(() => {
    pendingOptimized.forEach((timeoutId) => {
      clearTimeout(timeoutId);
    });
    pendingOptimized.clear();
  });
  /**
   * 防抖函数
   * @param fn 要防抖的函数
   * @param delay 延迟时间（毫秒）
   * @returns 防抖后的函数和取消函数
   */
  const debounce = <T extends (...args: any[]) => any>(
    fn: T,
    delay: number
  ): {
    debouncedFn: (...args: Parameters<T>) => void;
    cancel: () => void;
  } => {
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    
    const debouncedFn = (...args: Parameters<T>) => {
      if (timeoutId) {
        clearTimeout(timeoutId);
      }
      
      timeoutId = setTimeout(() => {
        fn(...args);
        timeoutId = null;
      }, delay);
    };

    const cancel = () => {
      if (timeoutId) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    };

    // 组件卸载时自动取消
    onBeforeUnmount(cancel);

    return { debouncedFn, cancel };
  };

  /**
   * 节流函数
   * @param fn 要节流的函数
   * @param delay 延迟时间（毫秒）
   * @returns 节流后的函数
   */
  const throttle = <T extends (...args: any[]) => any>(
    fn: T,
    delay: number
  ): T => {
    let lastCall = 0;
    
    return ((...args: Parameters<T>) => {
      const now = Date.now();
      if (now - lastCall >= delay) {
        lastCall = now;
        return fn(...args);
      }
    }) as T;
  };

  /**
   * 防抖的数据更新监听器
   * @param source 监听的数据源
   * @param callback 回调函数
   * @param delay 防抖延迟（默认 100ms）
   * @param options 监听选项
   * @returns 停止监听的函数
   */
  const debouncedWatch = <T>(
    source: WatchSource<T>,
    callback: WatchCallback<T, T | undefined>,
    delay = 100,
    options: { immediate?: boolean; deep?: boolean } = {}
  ): WatchStopHandle => {
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    return watch(
      source,
      (value, oldValue, onCleanup) => {
        // 清除之前的定时器
        if (timeoutId) {
          clearTimeout(timeoutId);
        }

        // 注册清理函数
        onCleanup(() => {
          if (timeoutId) {
            clearTimeout(timeoutId);
            timeoutId = null;
          }
        });

        // 设置新的延迟执行
        timeoutId = setTimeout(() => {
          callback(value, oldValue, onCleanup);
          timeoutId = null;
        }, delay);
      },
      options
    );
  };

  /**
   * 批量更新优化的 nextTick 封装
   * @param callback 更新回调
   * @param priority 优先级（high: 立即执行，normal: 正常防抖，low: 更长延迟）
   */
  const optimizedUpdate = (
    callback: () => void,
    priority: "high" | "normal" | "low" = "normal"
  ): void => {
    const delays = {
      high: 0,
      normal: 100,
      low: 300,
    };

    if (priority === "high") {
      // 清除现有的待执行更新，避免重复执行
      const existing = pendingOptimized.get(callback);
      if (existing) {
        clearTimeout(existing);
        pendingOptimized.delete(callback);
      }
      nextTick(callback);
      return;
    } else {
      // 清除现有的待执行更新
      const existing = pendingOptimized.get(callback);
      if (existing) {
        clearTimeout(existing);
      }
      
      // 设置新的延迟更新
      const timeoutId = setTimeout(() => {
        pendingOptimized.delete(callback);
        nextTick(callback);
      }, delays[priority]);
      
      pendingOptimized.set(callback, timeoutId);
    }
  };

  /**
   * 创建响应式的加载状态管理
   */
  const useLoadingState = () => {
    const isLoading = ref(false);
    const loadingCount = ref(0);
    
    const startLoading = () => {
      loadingCount.value++;
      isLoading.value = true;
    };
    
    const stopLoading = () => {
      loadingCount.value = Math.max(0, loadingCount.value - 1);
      if (loadingCount.value === 0) {
        isLoading.value = false;
      }
    };
    
    const resetLoading = () => {
      loadingCount.value = 0;
      isLoading.value = false;
    };
    
    return {
      isLoading,
      startLoading,
      stopLoading,
      resetLoading,
    };
  };

  return {
    debounce,
    throttle,
    debouncedWatch,
    optimizedUpdate,
    useLoadingState,
  };
}