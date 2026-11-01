package observation

import (
	"bytes"
	"encoding/json"
)

// QuotaWindowObservationTimes 分别记录用量、状态和重置时间的证据时间。
// 上游可以只返回其中一部分，不能用一次局部更新替其他字段续期。
type QuotaWindowObservationTimes struct {
	Usage int64 `json:"usage,omitempty"`
	State int64 `json:"state,omitempty"`
	Reset int64 `json:"reset,omitempty"`
}

// UnmarshalJSON 兼容此前快照中的单一窗口时间，作为三个字段组的已有基线。
func (value *QuotaWindowObservationTimes) UnmarshalJSON(raw []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		type fields QuotaWindowObservationTimes
		var decoded fields
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		*value = QuotaWindowObservationTimes(decoded)
		return nil
	}
	var legacy int64
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return err
	}
	*value = QuotaWindowObservationTimes{Usage: legacy, State: legacy, Reset: legacy}
	return nil
}
