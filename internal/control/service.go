package control

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

type Service struct {
	db                *gorm.DB
	manager           *state.Manager
	registry          *state.KeyRegistry
	encryption        encryption.Service
	dialects          dialect.Set
	modelFetchTimeout time.Duration
	random            io.Reader
	writeMu           sync.Mutex
}

func NewService(
	db *gorm.DB,
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	dialects dialect.Set,
) *Service {
	return &Service{
		db: db, manager: manager, registry: registry,
		encryption: encryptionService, dialects: dialects,
		modelFetchTimeout: 3 * time.Second, random: rand.Reader,
	}
}

func (s *Service) writeConfig(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	afterPublish func() error,
) (*state.ConfigSnapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var input state.CompileInput
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		var err error
		input, err = stateloader.BuildCompileInput(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := state.Compile(input); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	snapshot, err := s.manager.Publish(input)
	if err != nil {
		return nil, fmt.Errorf("publish committed config: %w", err)
	}
	if afterPublish != nil {
		if err := afterPublish(); err != nil {
			return nil, fmt.Errorf("apply committed runtime update: %w", err)
		}
	}
	return snapshot, nil
}
