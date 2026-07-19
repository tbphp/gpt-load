package dialect

import "fmt"

const (
	maxModelListPages         = 100
	maxUniqueModelListEntries = 100_000
)

type modelListCollector struct {
	values []string
	seen   map[string]struct{}
}

func newModelListCollector() *modelListCollector {
	return &modelListCollector{
		values: make([]string, 0),
		seen:   make(map[string]struct{}),
	}
}

func (collector *modelListCollector) Add(values []string) error {
	for _, value := range values {
		if _, duplicate := collector.seen[value]; duplicate {
			continue
		}
		if len(collector.values) == maxUniqueModelListEntries {
			return fmt.Errorf("model list unique-result limit exceeded")
		}
		collector.seen[value] = struct{}{}
		collector.values = append(collector.values, value)
	}
	return nil
}

func (collector *modelListCollector) Full() bool {
	return len(collector.values) == maxUniqueModelListEntries
}

func (collector *modelListCollector) Result() []string {
	result := make([]string, len(collector.values))
	copy(result, collector.values)
	return result
}
