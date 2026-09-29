package control

import (
	app_errors "gpt-load/internal/platform/errors"
)

// ConcurrencyView 由现有管理资源返回，当前值只读取运行时内存。
type ConcurrencyView struct {
	Current int64 `json:"current"`
	Limit   int64 `json:"limit"`
}

func normalizeConcurrencyLimit(field optionalField[int64]) (*int64, error) {
	if !field.Set || field.Null {
		return nil, nil
	}
	if field.Value < 0 || field.Value > 9007199254740991 {
		return nil, app_errors.ErrValidation
	}
	value := field.Value
	return &value, nil
}

func (s *Service) fillAccessKeyConcurrency(value *AccessKeyMetadata) {
	if s.manager == nil {
		return
	}
	snapshot := s.manager.Current()
	if snapshot == nil {
		return
	}
	limit := snapshot.Settings.DefaultAccessKeyConcurrencyLimit
	if key, ok := snapshot.AccessKeysByID[value.ID]; ok && key.ConcurrencyLimit != nil {
		limit = *key.ConcurrencyLimit
	}
	value.Concurrency = ConcurrencyView{Current: s.manager.Concurrency().AccessKeyCurrent(value.ID), Limit: limit}
}
