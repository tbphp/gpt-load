package policy

import "regexp"

const (
	// MaxConfigBytes 顶层配置正文最大字节数（256 KiB）
	MaxConfigBytes = 256 << 10

	// MaxRulesPerConfig 单个配置最大规则条数
	MaxRulesPerConfig = 100

	// MaxConditionDepth 条件树最大深度（根为 1）
	MaxConditionDepth = 16

	// MaxJSONDepth JSON 语法嵌套深度防护上限（推导自配置包装与 MaxConditionDepth）
	MaxJSONDepth = 64

	// MaxNodesPerRule 单条规则最大条件节点数
	MaxNodesPerRule = 256

	// MaxNodesPerConfig 单个配置全部规则累计最大节点数
	MaxNodesPerConfig = 4096

	// MaxListItems 列表项最大数量（如 in 的 value 列表、weekdays 等）
	MaxListItems = 100

	// MaxIDLength 规则 ID 最大字符数（ASCII [A-Za-z0-9_-]+）
	MaxIDLength = 64

	// MaxNameLength 规则名称最大 Unicode 字符数
	MaxNameLength = 128

	// MaxModelLength 模型名称最大 Unicode 字符数
	MaxModelLength = 255
)

var (
	idRegex = regexp.MustCompile("^[A-Za-z0-9_-]+$")
)
