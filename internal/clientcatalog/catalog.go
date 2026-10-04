// Package clientcatalog 为网关和管理面共用 Codex 目录选择、序列化和字节预算。
package clientcatalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

// LimitBytes 是已验证的 Codex 自定义目录响应体上限。
const LimitBytes int64 = 1 << 20

type Entry struct {
	ClientModel string `json:"client_model"`
	Bytes       int64  `json:"bytes"`
	Included    bool   `json:"included"`
}

type Result struct {
	Body          []byte  `json:"-"`
	LimitBytes    int64   `json:"limit_bytes"`
	ResponseBytes int64   `json:"response_bytes"`
	SelectedBytes int64   `json:"selected_bytes"`
	IncludedCount int     `json:"included_count"`
	Entries       []Entry `json:"entries"`
}

// Candidates 只包含可通过 Responses create 调用的请求模型名称。
func Candidates(snapshot *state.ConfigSnapshot, key state.AccessKeyView) []string {
	if snapshot == nil {
		return []string{}
	}
	if len(key.Filters.Protocols) > 0 {
		if _, allowed := key.Filters.Protocols[protocol.OpenAIResponses]; !allowed {
			return []string{}
		}
	}
	visible := map[string]struct{}{}
	for name, targets := range snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate] {
		if name == state.NoModelRouteKey {
			continue
		}
		if len(key.Filters.Models) > 0 {
			if _, allowed := key.Filters.Models[name]; !allowed {
				continue
			}
		}
		for _, target := range targets {
			if len(key.Filters.Groups) == 0 {
				visible[name] = struct{}{}
				break
			}
			if _, allowed := key.Filters.Groups[target.GroupID]; allowed {
				visible[name] = struct{}{}
				break
			}
		}
	}
	if snapshot.AutoModels != nil && snapshot.AutoModels.Enabled() {
		for _, config := range snapshot.AutoModels.Config().Models {
			entry, exists := snapshot.AutoModels.Lookup(config.Name)
			if !exists || !entry.Enabled {
				continue
			}
			if len(key.Filters.Models) > 0 {
				if _, allowed := key.Filters.Models[entry.Name]; !allowed {
					continue
				}
			}
			for _, preset := range entry.Presets {
				if preset.ID != entry.Fallback {
					continue
				}
				model := preset.Model
				if len(scheduler.CandidateGroupIDsForQuery(snapshot, scheduler.Query{
					ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate,
					ExternalModel: &model, AccessKey: key,
				})) > 0 {
					visible[entry.Name] = struct{}{}
				}
			}
		}
	}
	names := make([]string, 0, len(visible))
	for name := range visible {
		names = append(names, name)
	}
	catalog.SortClientModelCatalog(names, snapshot.ClientModelOverrides)
	return names
}

func Selected(snapshot *state.ConfigSnapshot, key state.AccessKeyView) []string {
	names := []string{}
	for _, name := range Candidates(snapshot, key) {
		if catalog.ClientModelCatalogEnabled(name, snapshot.ClientModelOverrides[name]) {
			names = append(names, name)
		}
	}
	return names
}

// Build 按真实 JSON 字节计算容量，预留逗号与闭合字符，返回可容纳的最大完整前缀。
func Build(snapshot *state.ConfigSnapshot, key state.AccessKeyView, version string, limit int64) (Result, error) {
	limit = min(limit, LimitBytes)
	result := Result{LimitBytes: limit, Body: []byte(`{"models":[`), Entries: []Entry{}}
	if int64(len(result.Body)+2) > limit {
		return Result{}, fmt.Errorf("client model catalog budget too small")
	}
	result.SelectedBytes = int64(len(result.Body) + 2)
	full := false
	for index, name := range Selected(snapshot, key) {
		overrides := snapshot.ClientModelOverrides[name]
		fallback := false
		if snapshot.AutoModels != nil {
			_, fallback = snapshot.AutoModels.Lookup(name)
		}
		model, _, _, err := catalog.BuildCodexCatalogModel(name, index, overrides, fallback)
		if err != nil {
			return Result{}, err
		}
		if usesModelMessages(version) {
			delete(model, "base_instructions")
		}
		item, err := json.Marshal(model)
		if err != nil {
			return Result{}, err
		}
		size := int64(len(item))
		if index > 0 {
			size++
		}
		result.SelectedBytes += size
		included := !full && size <= limit-int64(len(result.Body))-2
		result.Entries = append(result.Entries, Entry{ClientModel: name, Bytes: int64(len(item)), Included: included})
		if !included {
			full = true
			continue
		}
		if index > 0 {
			result.Body = append(result.Body, ',')
		}
		result.Body = append(result.Body, item...)
		result.IncludedCount++
	}
	result.Body = append(result.Body, ']', '}')
	result.ResponseBytes = int64(len(result.Body))
	return result, nil
}

func usesModelMessages(clientVersion string) bool {
	parts := strings.Split(clientVersion, ".")
	if len(parts) != 3 {
		return false
	}
	var version [3]int
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return false
		}
		version[index] = parsed
	}
	// 0.159.2 的客户端契约已将 base_instructions 变为可选的旧字段。
	for index, minimum := range [3]int{0, 159, 2} {
		if version[index] != minimum {
			return version[index] > minimum
		}
	}
	return true
}
