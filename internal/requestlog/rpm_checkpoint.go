package requestlog

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"

	"gpt-load/internal/platform/utils"
	"gpt-load/internal/rpm"
	"gpt-load/internal/storage/models"
)

func (s *Service) SetRPMStore(store *rpm.Store) {
	s.rpmStore = store
	if store == nil {
		return
	}
	if s.quotaWake == nil {
		s.quotaWake = make(chan struct{}, 1)
	}
	store.SetDirtyNotifier(s.wakeAccessQuotaCheckpoint)
}

func rpmRow(snapshot rpm.Snapshot) models.RPMStat {
	return models.RPMStat{Kind: string(snapshot.Kind), KeyID: snapshot.KeyID,
		MinuteMS: snapshot.MinuteMS, SessionID: snapshot.SessionID,
		Requests: snapshot.Requests, Rejected: snapshot.Rejected, Peak: snapshot.Peak}
}

// 与被动额度观测共用工作循环，写入失败不改变日志和额度的处理结果。
func (s *Service) flushRPMCheckpoints(ctx context.Context) {
	if s == nil || s.rpmStore == nil || s.db == nil {
		return
	}
	if err := s.writeRPMCheckpoints(ctx); err != nil {
		s.warnRPMFailure(err)
	}
	if s.rpmStore.Pending(s.now()) {
		s.wakeAccessQuotaCheckpoint()
	}
}

func (s *Service) writeRPMCheckpoints(ctx context.Context) error {
	s.rpmWriteMu.Lock()
	defer s.rpmWriteMu.Unlock()
	snapshots := s.rpmStore.Snapshots(s.now(), true, batchSize)
	if len(snapshots) == 0 {
		return nil
	}
	rows := make([]models.RPMStat, len(snapshots))
	for i, snapshot := range snapshots {
		rows[i] = rpmRow(snapshot)
	}
	// 限制占用共享工作线程的时间；存储锁不跨越数据库 I/O。
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.db.WithContext(writeCtx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}, {Name: "key_id"}, {Name: "minute_ms"}, {Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"requests", "rejected", "peak"}),
	}).CreateInBatches(rows, 100).Error; err != nil {
		return err
	}
	s.rpmStore.Ack(snapshots)
	return nil
}

func (s *Service) drainRPMCheckpoints(ctx context.Context) {
	if s.rpmStore == nil || s.db == nil {
		return
	}
	for ctx.Err() == nil && len(s.rpmStore.Snapshots(s.now(), true, 1)) > 0 {
		if err := s.writeRPMCheckpoints(ctx); err != nil {
			s.warnRPMFailure(err)
			return
		}
	}
}

func (s *Service) warnRPMFailure(err error) {
	now := s.now()
	s.warningMu.Lock()
	if !s.rpmWarningAt.IsZero() && now.Sub(s.rpmWarningAt) < warningInterval {
		s.warningMu.Unlock()
		return
	}
	s.rpmWarningAt = now
	s.warningMu.Unlock()
	utils.LogPlaneBestEffort(s.logger, logrus.WarnLevel, utils.LogPlaneData,
		logrus.Fields{"event": "rpm.persist_failed", "error": err.Error()}, "RPM statistics persistence failed")
}
