import type {
  PoolStatsResponse,
  RecoveryMetrics,
  RecoveryPlan,
  BatchRecoveryRequest,
  PoolConfiguration,
} from "@/types/models";
import http from "@/utils/http";

export const poolApi = {
  // 获取池统计信息
  async getPoolStats(groupId: number): Promise<PoolStatsResponse> {
    const res = await http.get(`/pool/stats/${groupId}`);
    return res.data;
  },

  // 获取恢复指标
  async getRecoveryMetrics(groupId: number): Promise<RecoveryMetrics> {
    const res = await http.get(`/pool/recovery/metrics/${groupId}`);
    return res.data;
  },

  // 获取池配置
  async getPoolConfiguration(groupId: number): Promise<PoolConfiguration> {
    const res = await http.get(`/pool/config/${groupId}`);
    return res.data;
  },

  // 更新池配置
  async updatePoolConfiguration(
    groupId: number,
    config: Partial<PoolConfiguration>
  ): Promise<PoolConfiguration> {
    const res = await http.put(`/pool/config/${groupId}`, config);
    return res.data;
  },

  // 触发手动恢复
  async triggerManualRecovery(groupId: number, keyIds: number[]): Promise<void> {
    await http.post(`/pool/recovery/manual/${groupId}`, {
      key_ids: keyIds,
    });
  },

  // 触发批量恢复
  async triggerBatchRecovery(
    groupId: number,
    request: BatchRecoveryRequest
  ): Promise<void> {
    await http.post(`/pool/recovery/batch/${groupId}`, request);
  },

  // 重填池
  async refillPools(groupId: number): Promise<void> {
    await http.post(`/pool/refill/${groupId}`);
  },

  // 获取恢复计划列表
  async getRecoveryPlans(groupId: number): Promise<RecoveryPlan[]> {
    const res = await http.get(`/pool/recovery/plans/${groupId}`);
    return res.data || [];
  },

  // 取消恢复计划
  async cancelRecoveryPlan(groupId: number, planId: string): Promise<void> {
    await http.delete(`/pool/recovery/plans/${groupId}/${planId}`);
  },

  // 获取池中的密钥列表
  async getPoolKeys(
    groupId: number,
    poolType: string,
    page: number = 1,
    pageSize: number = 20
  ): Promise<{
    items: any[];
    pagination: {
      total_items: number;
      total_pages: number;
    };
  }> {
    const res = await http.get(`/pool/keys/${groupId}/${poolType}`, {
      params: {
        page,
        page_size: pageSize,
      },
    });
    return res.data;
  },

  // 移动密钥到不同池
  async moveKeys(
    groupId: number,
    keyIds: number[],
    fromPool: string,
    toPool: string
  ): Promise<void> {
    await http.post(`/pool/move/${groupId}`, {
      key_ids: keyIds,
      from_pool: fromPool,
      to_pool: toPool,
    });
  },

  // 获取池性能指标
  async getPoolPerformance(
    groupId: number,
    timeRange: string = "24h"
  ): Promise<{
    throughput: number[];
    latency: number[];
    error_rate: number[];
    timestamps: string[];
  }> {
    const res = await http.get(`/pool/performance/${groupId}`, {
      params: { time_range: timeRange },
    });
    return res.data;
  },

  // 启动恢复服务
  async startRecoveryServices(groupId: number): Promise<void> {
    await http.post(`/pool/recovery/start/${groupId}`);
  },

  // 停止恢复服务
  async stopRecoveryServices(groupId: number): Promise<void> {
    await http.post(`/pool/recovery/stop/${groupId}`);
  },

  // 获取恢复历史
  async getRecoveryHistory(
    groupId: number,
    page: number = 1,
    pageSize: number = 20
  ): Promise<{
    items: any[];
    pagination: {
      total_items: number;
      total_pages: number;
    };
  }> {
    const res = await http.get(`/pool/recovery/history/${groupId}`, {
      params: {
        page,
        page_size: pageSize,
      },
    });
    return res.data;
  },

  // 获取池健康状态
  async getPoolHealth(groupId: number): Promise<{
    status: "healthy" | "warning" | "critical";
    issues: string[];
    recommendations: string[];
    last_check: string;
  }> {
    const res = await http.get(`/pool/health/${groupId}`);
    return res.data;
  },

  // 执行池诊断
  async runPoolDiagnostics(groupId: number): Promise<{
    validation_pool: {
      count: number;
      issues: string[];
    };
    ready_pool: {
      count: number;
      issues: string[];
    };
    active_pool: {
      count: number;
      issues: string[];
    };
    cooling_pool: {
      count: number;
      issues: string[];
    };
    overall_health: string;
    recommendations: string[];
  }> {
    const res = await http.post(`/pool/diagnostics/${groupId}`);
    return res.data;
  },

  // 优化池配置
  async optimizePoolConfiguration(groupId: number): Promise<{
    current_config: PoolConfiguration;
    recommended_config: PoolConfiguration;
    performance_improvement: number;
    changes: string[];
  }> {
    const res = await http.post(`/pool/optimize/${groupId}`);
    return res.data;
  },

  // 导出池数据
  exportPoolData(groupId: number, format: "json" | "csv" = "json") {
    const authKey = localStorage.getItem("authKey");
    if (!authKey) {
      window.$message.error("未找到认证信息，无法导出");
      return;
    }

    const params = new URLSearchParams({
      group_id: groupId.toString(),
      format,
      key: authKey,
    });

    const url = `${http.defaults.baseURL}/pool/export?${params.toString()}`;

    const link = document.createElement("a");
    link.href = url;
    link.setAttribute(
      "download",
      `pool-data-group_${groupId}-${Date.now()}.${format}`
    );
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  },

  // 获取池事件日志
  async getPoolEvents(
    groupId: number,
    eventType?: string,
    page: number = 1,
    pageSize: number = 50
  ): Promise<{
    items: any[];
    pagination: {
      total_items: number;
      total_pages: number;
    };
  }> {
    const res = await http.get(`/pool/events/${groupId}`, {
      params: {
        event_type: eventType,
        page,
        page_size: pageSize,
      },
    });
    return res.data;
  },

  // 清理过期数据
  async cleanupExpiredData(groupId: number): Promise<{
    cleaned_records: number;
    freed_space: string;
  }> {
    const res = await http.post(`/pool/cleanup/${groupId}`);
    return res.data;
  },
};
