package providers

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
)

// ModelCatalogUpdater 复用 CPA 的目录刷新、校验和失败兜底。
type ModelCatalogUpdater struct{}

func NewModelCatalogUpdater() *ModelCatalogUpdater { return &ModelCatalogUpdater{} }

func (*ModelCatalogUpdater) Run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	cpaembedded.StartModelCatalogUpdater(ctx)
	<-ctx.Done()
}
