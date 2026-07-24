package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/platform/version"
)

const (
	systemInstanceModeSingle       = "single"
	systemDatabaseSQLite           = "sqlite"
	systemDistributionSingleBinary = "single_binary"
)

type systemDeploymentResponse struct {
	InstanceMode string `json:"instance_mode"`
	Database     string `json:"database"`
	Distribution string `json:"distribution"`
}

type systemSecretResponse struct {
	Source config.SecretSource `json:"source"`
	Path   *string             `json:"path"`
}

type systemEncryptionResponse struct {
	Enabled bool                `json:"enabled"`
	Source  config.SecretSource `json:"source"`
	Path    *string             `json:"path"`
}

type systemInfoResponse struct {
	Version    string                   `json:"version"`
	Deployment systemDeploymentResponse `json:"deployment"`
	DataDir    string                   `json:"data_dir"`
	AuthKey    systemSecretResponse     `json:"auth_key"`
	Encryption systemEncryptionResponse `json:"encryption"`
}

func newSystemInfoResponse(cfg *config.Config) systemInfoResponse {
	return systemInfoResponse{
		Version: version.Version,
		Deployment: systemDeploymentResponse{
			InstanceMode: systemInstanceModeSingle,
			Database:     systemDatabaseSQLite,
			Distribution: systemDistributionSingleBinary,
		},
		DataDir: cfg.DataDir,
		AuthKey: systemSecretResponse{
			Source: cfg.AuthKeyMetadata.Source,
			Path:   systemSecretPath(cfg.AuthKeyMetadata),
		},
		Encryption: systemEncryptionResponse{
			Enabled: true,
			Source:  cfg.EncryptionKeyMetadata.Source,
			Path:    systemSecretPath(cfg.EncryptionKeyMetadata),
		},
	}
}

func systemSecretPath(metadata config.SecretMetadata) *string {
	if metadata.Source != config.SecretSourceKeyFile {
		return nil
	}
	path := metadata.Path
	return &path
}

func (s *Server) handleSystemInfo(c *gin.Context) {
	response.SuccessI18n(c, "common.success", s.systemInfo)
}
