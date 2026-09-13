package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/response"
)

// ModernCredentialItem 复用凭据读快照，仅补充配置来源，不改变经典 API 或调度逻辑。
type ModernCredentialItem struct {
	CredentialItemResponse
	WeightManual *int `json:"weight_manual"`
}

func (s *Server) handleListModernCredentials(c *gin.Context) {
	id, ok := groupID(c, "list_modern_credentials")
	if !ok {
		return
	}
	query, apiErr := parseCredentialCollectionQuery(c.Request.URL.RawQuery)
	if apiErr != nil {
		writeServiceError(c, "list_modern_credentials", apiErr)
		return
	}
	result, err := s.service.ListGroupCredentials(c.Request.Context(), id, query)
	if err != nil {
		writeServiceError(c, "list_modern_credentials", err)
		return
	}
	items := make([]ModernCredentialItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, ModernCredentialItem{CredentialItemResponse: item, WeightManual: item.WeightManual})
	}
	response.SuccessI18n(c, "common.success", struct {
		CredentialCollectionResponse
		Items []ModernCredentialItem `json:"items"`
	}{CredentialCollectionResponse: result, Items: items})
}

func (s *Server) handleGetModernCredential(c *gin.Context) {
	group, ok := groupID(c, "get_modern_credential")
	if !ok {
		return
	}
	id, ok := credentialID(c, "get_modern_credential")
	if !ok {
		return
	}
	result, err := s.service.GetCredentialDetail(c.Request.Context(), group, id)
	if err != nil {
		writeServiceError(c, "get_modern_credential", err)
		return
	}
	response.SuccessI18n(c, "common.success", struct {
		Credential  ModernCredentialItem          `json:"credential"`
		Observation CredentialObservationResponse `json:"observation"`
	}{
		Credential:  ModernCredentialItem{CredentialItemResponse: result.Credential, WeightManual: result.Credential.WeightManual},
		Observation: result.Observation,
	})
}
