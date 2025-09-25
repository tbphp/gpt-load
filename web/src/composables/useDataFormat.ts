/**
 * 数据格式化和验证相关的通用工具函数
 */
export function useDataFormat() {
  /**
   * 安全地将值转换为数字，处理 null/undefined/string 情况
   * @param value 要转换的值
   * @param fallback 当值无效时的默认值
   * @returns 转换后的数字
   */
  const safeNumber = (value: unknown, fallback = 0): number => {
    if (value == null || value === "") return fallback;
    
    if (typeof value === "number") {
      return Number.isFinite(value) ? value : fallback;
    }
    
    if (typeof value === "string") {
      // 处理特殊字符串值
      if (value === "--" || value === "N/A" || value.trim() === "") {
        return fallback;
      }
      
      const num = Number(value);
      return Number.isFinite(num) ? num : fallback;
    }
    
    return fallback;
  };

  /**
   * 安全地将值转换为数字，支持 null 返回值
   * @param value 要转换的值
   * @returns 转换后的数字或 null
   */
  const safeNumberOrNull = (value: unknown): number | null => {
    if (value == null || value === "" || value === "--" || value === "N/A") {
      return null;
    }
    
    if (typeof value === "number") {
      return Number.isFinite(value) ? value : null;
    }
    
    if (typeof value === "string") {
      if (value.trim() === "") return null;
      const num = Number(value);
      return Number.isFinite(num) ? num : null;
    }
    
    return null;
  };

  /**
   * 检查值是否为有效的数值数据
   * @param value 要检查的值
   * @returns 是否为有效数值
   */
  const isValidNumber = (value: unknown): boolean => {
    if (value == null) return false;
    if (typeof value === "number") return Number.isFinite(value);
    if (typeof value === "string") {
      if (value === "--" || value === "N/A" || value.trim() === "") return false;
      const num = Number(value);
      return Number.isFinite(num);
    }
    return false;
  };

  /**
   * 将数值限制在 0-1 之间
   * @param value 要限制的数值
   * @returns 限制在 0-1 之间的数值
   */
  const clamp01 = (value: number): number => {
    if (!Number.isFinite(value)) return 0;
    return Math.min(Math.max(value, 0), 1);
  };

  /**
   * 格式化数值显示
   * @param value 要格式化的值
   * @param type 数值类型：count（数量）或 rate（百分比）
   * @param defaultText 当数值无效时显示的文本
   * @returns 格式化后的字符串
   */
  const formatValue = (
    value: unknown, 
    type: "count" | "rate" = "count",
    defaultText = "--"
  ): string => {
    if (!isValidNumber(value)) {
      return defaultText;
    }
    
    const num = safeNumber(value);
    
    if (type === "rate") {
      const percent = num <= 1 ? num * 100 : num;
      return `${percent.toFixed(1)}%`;
    }
    
    // 使用 Intl.NumberFormat 进行本地化的紧凑格式化
    try {
      return new Intl.NumberFormat(undefined, {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(num);
    } catch {
      // Intl 不可用时的后备方案
      if (num >= 1_000_000) {
        return `${(num / 1_000_000).toFixed(1)}M`;
      }
      if (num >= 1_000) {
        return `${(num / 1_000).toFixed(1)}K`;
      }
      return num.toString();
    }
  };

  /**
   * 格式化趋势显示
   * @param trend 趋势数值
   * @param defaultText 当数值无效时显示的文本
   * @returns 格式化后的趋势字符串（带符号）
   */
  const formatTrend = (trend: unknown, defaultText = "--"): string => {
    if (!isValidNumber(trend)) {
      return defaultText;
    }
    
    const num = safeNumber(trend);
    const sign = num > 0 ? "+" : "";
    return `${sign}${num.toFixed(1)}%`;
  };

  /**
   * 格式化百分比显示
   * @param value 数值（0-1之间的小数或百分比数值）
   * @param defaultText 当数值无效时显示的文本
   * @returns 格式化后的百分比字符串
   */
  const formatPercentage = (value: unknown, defaultText = "--"): string => {
    if (!isValidNumber(value)) {
      return defaultText;
    }
    
    const num = safeNumber(value);
    
    // 如果数值小于等于1，认为是小数形式的百分比（如 0.15 = 15%）
    if (num <= 1) {
      return `${(num * 100).toFixed(1)}%`;
    }
    
    // 否则认为是已经是百分比数值（如 15 = 15%）
    return `${num.toFixed(1)}%`;
  };

  /**
   * 计算比率，安全处理除零情况
   * @param numerator 分子
   * @param denominator 分母
   * @returns 0-1 之间的比率
   */
  const safeRatio = (numerator: number, denominator: number): number => {
    if (!Number.isFinite(numerator) || !Number.isFinite(denominator) || denominator === 0) {
      return 0;
    }
    return clamp01(numerator / denominator);
  };

  /**
   * 基于趋势计算动画比率
   * @param trend 趋势值（百分比）
   * @param baseValue 基准值（默认 100）
   * @returns 0-1 之间的比率
   */
  const trendToRatio = (trend: unknown, baseValue = 100): number => {
    const trendNum = safeNumberOrNull(trend);
    if (trendNum === null) return 0;
    return clamp01((baseValue + trendNum) / baseValue);
  };

  /**
   * 基于错误率计算成功率比率（错误率越高，比率越低）
   * @param errorRate 错误率（可能是0-1小数或0-100百分比）
   * @param baseValue 基准值（默认 100）
   * @returns 0-1 之间的比率
   */
  const errorRateToRatio = (errorRate: unknown, baseValue = 100): number => {
    const errorNum = safeNumberOrNull(errorRate);
    if (errorNum === null) return 0;
    
    // 标准化错误率到与 baseValue 相同的单位
    const normalizedError = errorNum <= 1 ? errorNum * baseValue : errorNum;
    return clamp01((baseValue - normalizedError) / baseValue);
  };

  return {
    safeNumber,
    safeNumberOrNull,
    isValidNumber,
    clamp01,
    formatValue,
    formatTrend,
    formatPercentage,
    safeRatio,
    trendToRatio,
    errorRateToRatio,
  };
}