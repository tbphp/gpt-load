package execution

// UsesModelCooldown 只包含消耗模型推理额度的操作，计数、探测和资源管理不共用限制。
func (o Operation) UsesModelCooldown() bool {
	switch o {
	case OperationChatCompletion, OperationResponsesCreate, OperationResponsesCompact,
		OperationImagesGenerate, OperationImagesEdit, OperationEmbeddingsCreate, OperationRerank:
		return true
	default:
		return false
	}
}
