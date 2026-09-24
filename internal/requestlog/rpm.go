package requestlog

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gpt-load/internal/rpm"
	"gpt-load/internal/storage/models"
)

type RPMPoint struct {
	BucketStartMS int64 `json:"bucket_start_ms"`
	Peak          int64 `json:"peak_rpm"`
	Requests      int64 `json:"requests"`
	Rejected      int64 `json:"rejected"`
}
type RPMReport struct {
	FromMS        int64        `json:"from_ms"`
	ToMS          int64        `json:"to_ms"`
	ObservedAtMS  int64        `json:"observed_at_ms"`
	BucketWidthMS int64        `json:"bucket_width_ms"`
	Current       *rpm.Counter `json:"current,omitempty"`
	Peak          *int64       `json:"peak_rpm,omitempty"`
	Requests      int64        `json:"requests"`
	Rejected      int64        `json:"rejected"`
	Points        []RPMPoint   `json:"points"`
}

func (s *Service) QueryRPM(ctx context.Context, kind rpm.Kind, id uint, now time.Time) (RPMReport, error) {
	from := now.Add(-time.Hour)
	result := RPMReport{FromMS: from.UnixMilli(), ToMS: now.UnixMilli(), ObservedAtMS: now.UnixMilli(), BucketWidthMS: 60_000, Points: []RPMPoint{}}
	if !validRPMKind(kind) || id == 0 || from.UnixMilli() < 0 {
		return result, fmt.Errorf("invalid RPM query")
	}
	if s == nil || s.db == nil {
		return result, fmt.Errorf("RPM database unavailable")
	}
	queryFrom := s.rpmQueryFrom(from, now)
	var rows []models.RPMStat
	if err := s.db.WithContext(ctx).Where("kind = ? AND key_id = ? AND minute_ms >= ? AND minute_ms <= ?", string(kind), id, queryFrom, now.UnixMilli()).Find(&rows).Error; err != nil {
		return result, err
	}
	type identity struct {
		minute  int64
		session string
	}
	merged := make(map[identity]models.RPMStat, len(rows))
	for _, row := range rows {
		merged[identity{row.MinuteMS, row.SessionID}] = row
	}
	// 同一进程的绝对快照覆盖持久化版本，查询和写入并发也不重复累加。
	for _, snapshot := range s.rpmStore.Snapshots(now, false, 0) {
		if snapshot.Kind != kind || snapshot.KeyID != id || snapshot.MinuteMS < queryFrom || snapshot.MinuteMS > now.UnixMilli() {
			continue
		}
		key := identity{snapshot.MinuteMS, snapshot.SessionID}
		row := rpmRow(snapshot)
		if persisted, exists := merged[key]; exists {
			row.Requests = max(row.Requests, persisted.Requests)
			row.Rejected = max(row.Rejected, persisted.Rejected)
			row.Peak = max(row.Peak, persisted.Peak)
		}
		merged[key] = row
	}
	points := make(map[int64]RPMPoint)
	for _, row := range merged {
		at := row.MinuteMS - row.MinuteMS%result.BucketWidthMS
		point := points[at]
		point.BucketStartMS = at
		point.Peak = max(point.Peak, row.Peak)
		point.Requests += row.Requests
		point.Rejected += row.Rejected
		points[at] = point
		result.Requests += row.Requests
		result.Rejected += row.Rejected
		if result.Peak == nil || row.Peak > *result.Peak {
			peak := row.Peak
			result.Peak = &peak
		}
	}
	for _, point := range points {
		result.Points = append(result.Points, point)
	}
	sort.Slice(result.Points, func(i, j int) bool { return result.Points[i].BucketStartMS < result.Points[j].BucketStartMS })
	if result.Peak != nil && s.rpmStore != nil {
		current := s.rpmStore.Current(kind, id, now)
		result.Current = &current
	}
	return result, nil
}
func (s *Service) RPMPeaks(ctx context.Context, kind rpm.Kind, ids []uint, now time.Time) (map[uint]int64, error) {
	result := make(map[uint]int64)
	if !validRPMKind(kind) {
		return nil, fmt.Errorf("invalid RPM kind")
	}
	if len(ids) == 0 {
		return result, nil
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("RPM database unavailable")
	}
	from := s.rpmQueryFrom(now.Add(-time.Hour), now)
	for offset := 0; offset < len(ids); offset += 500 {
		var rows []struct {
			KeyID uint
			Peak  int64
		}
		if err := s.db.WithContext(ctx).Model(&models.RPMStat{}).
			Select("key_id, MAX(peak) AS peak").Where("kind = ? AND key_id IN ? AND minute_ms >= ? AND minute_ms <= ?", string(kind), ids[offset:min(offset+500, len(ids))], from, now.UnixMilli()).Group("key_id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result[row.KeyID] = row.Peak
		}
	}
	selected := make(map[uint]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	for _, snapshot := range s.rpmStore.Snapshots(now, false, 0) {
		if snapshot.Kind == kind && selected[snapshot.KeyID] && snapshot.MinuteMS >= from && snapshot.MinuteMS <= now.UnixMilli() {
			result[snapshot.KeyID] = max(result[snapshot.KeyID], snapshot.Peak)
		}
	}
	return result, nil
}

func validRPMKind(kind rpm.Kind) bool { return kind == rpm.AccessKey || kind == rpm.Credential }

func (s *Service) rpmQueryFrom(from, now time.Time) int64 {
	cutoff := from.UnixMilli()
	if s.retentionPolicy != nil {
		cutoff = max(cutoff, now.Add(-time.Duration(s.retentionPolicy.RequestLogRetentionDays())*24*time.Hour).UnixMilli())
	}
	// 分钟汇总只返回窗口内的分钟，避免把窗口外峰值带入。
	return (cutoff + 59_999) / 60_000 * 60_000
}

func (s *Service) deleteExpiredRPM(ctx context.Context, cutoffMS int64, now time.Time) {
	s.rpmWriteMu.Lock()
	s.rpmStore.DiscardBefore(cutoffMS)
	s.rpmWriteMu.Unlock()
	for ctx.Err() == nil {
		var ids []uint
		if err := s.db.WithContext(ctx).Model(&models.RPMStat{}).Where("minute_ms < ?", cutoffMS).
			Order("minute_ms ASC").Limit(retentionBatchSize).Pluck("id", &ids).Error; err != nil {
			if ctx.Err() == nil {
				s.recordRetentionDeleteFailure(now)
			}
			return
		}
		if len(ids) == 0 {
			return
		}
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&models.RPMStat{}).Error; err != nil {
			if ctx.Err() == nil {
				s.recordRetentionDeleteFailure(now)
			}
			return
		}
	}
}
