package embedded

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// StartModelCatalogUpdater 将 CPA 在线目录更新绑定到应用生命周期。
// SDK 使用环境变量代理，启动时拉取并每三小时刷新；失败时保留当前内存目录。
// SDK 的更新器为进程单例，不持久化目录，也不修改 GPT-Load 分组配置。
func StartModelCatalogUpdater(ctx context.Context) {
	registry.StartModelsUpdater(ctx)
}
