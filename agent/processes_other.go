//go:build !linux

package agent

import (
	"github.com/henrygd/beszel/internal/entities/container"
	"github.com/henrygd/beszel/internal/entities/process"
)

// processManager is a no-op outside Linux; process monitoring reads /proc.
type processManager struct{}

func newProcessManager() *processManager { return nil }

func (pm *processManager) collect(uint16) ([]*container.Stats, []*process.Proc) { return nil, nil }
