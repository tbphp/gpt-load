// Package jev defines the shared experimental Decisions connection.
package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid experimental configuration")

const SettingKey = "jev"

type Config struct {
	Model          string `json:"model"`
	GroupID        uint   `json:"group_id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func DefaultConfig() Config { return Config{TimeoutSeconds: 2} }

func Decode(raw []byte) (Config, error) {
	value := DefaultConfig()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return Config{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Config{}, fmt.Errorf("invalid Jev configuration")
	}
	if value.TimeoutSeconds < 1 || value.TimeoutSeconds > 60 || len(value.Model) > 255 || value.Model != strings.TrimSpace(value.Model) {
		return Config{}, fmt.Errorf("invalid Jev model or timeout")
	}
	return value, nil
}
