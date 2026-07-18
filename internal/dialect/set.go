package dialect

import "gpt-load/internal/protocol"

type Set map[protocol.Protocol]Dialect

func NewSet(values ...Dialect) Set {
	result := make(Set, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		result[value.Protocol()] = value
	}
	return result
}
