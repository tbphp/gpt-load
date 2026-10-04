package catalog

import (
	"regexp"
	"sort"
	"strings"
)

var gptModelVersion = regexp.MustCompile(`^gpt-([0-9]+(?:\.[0-9]+)*)`)

// ClientModelCatalogEnabled 仅决定客户端目录展示，不参与访问权限或调用路由。
func ClientModelCatalogEnabled(model string, overrides ClientModelOverrides) bool {
	if overrides.CatalogEnabled != nil {
		return *overrides.CatalogEnabled
	}
	return strings.HasPrefix(model, "gpt-") && model != "gpt-image" && !strings.HasPrefix(model, "gpt-image-")
}

// SortClientModelCatalog 将手动顺序放在前面，其余模型按动态版本号从新到旧排列。
func SortClientModelCatalog(models []string, overrides map[string]ClientModelOverrides) {
	sort.Slice(models, func(left, right int) bool {
		leftOrder, rightOrder := overrides[models[left]].CatalogOrder, overrides[models[right]].CatalogOrder
		if leftOrder != nil || rightOrder != nil {
			if leftOrder == nil {
				return false
			}
			if rightOrder == nil {
				return true
			}
			if *leftOrder != *rightOrder {
				return *leftOrder < *rightOrder
			}
		}
		leftVersion, rightVersion := gptModelVersion.FindStringSubmatch(models[left]), gptModelVersion.FindStringSubmatch(models[right])
		if len(leftVersion) != len(rightVersion) {
			return len(leftVersion) > len(rightVersion)
		}
		if len(leftVersion) > 0 {
			leftParts, rightParts := strings.Split(leftVersion[1], "."), strings.Split(rightVersion[1], ".")
			for index := 0; index < max(len(leftParts), len(rightParts)); index++ {
				leftPart, rightPart := "0", "0"
				if index < len(leftParts) {
					leftPart = leftParts[index]
				}
				if index < len(rightParts) {
					rightPart = rightParts[index]
				}
				// 按数字长度和文本比较，避免新版本号超出机器整数范围。
				leftPart, rightPart = strings.TrimLeft(leftPart, "0"), strings.TrimLeft(rightPart, "0")
				if len(leftPart) != len(rightPart) {
					return len(leftPart) > len(rightPart)
				}
				if leftPart != rightPart {
					return leftPart > rightPart
				}
			}
		}
		return models[left] < models[right]
	})
}
