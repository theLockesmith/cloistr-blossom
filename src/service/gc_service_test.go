package service

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

func TestClampGCLimit(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"zero falls back to default", 0, 1000},
		{"negative falls back to default", -5, 1000},
		{"in-range passes through", 250, 250},
		{"at max passes through", gcReconcileMaxLimit, gcReconcileMaxLimit},
		{"over max is clamped", gcReconcileMaxLimit + 1, gcReconcileMaxLimit},
		{"huge is clamped", 1 << 30, gcReconcileMaxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampGCLimit(tt.in); got != tt.want {
				t.Fatalf("clampGCLimit(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestGCConfigFromYAML(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		// An empty YAML GC block: Enabled false, Interval/BatchSize apply defaults
		// via config defaulting; here we exercise the mapper's own fallbacks.
		conf := &config.Config{}
		cfg := gcConfigFromYAML(conf)
		if cfg.Enabled {
			t.Fatal("expected disabled by default")
		}
		if cfg.Interval != 1*time.Hour {
			t.Fatalf("interval = %v, want 1h", cfg.Interval)
		}
		if cfg.BatchSize != 1000 {
			t.Fatalf("batch size = %d, want 1000", cfg.BatchSize)
		}
	})

	t.Run("honors explicit values", func(t *testing.T) {
		conf := &config.Config{}
		conf.GC.Enabled = true
		conf.GC.Interval = "15m"
		conf.GC.BatchSize = 250
		cfg := gcConfigFromYAML(conf)
		if !cfg.Enabled {
			t.Fatal("expected enabled")
		}
		if cfg.Interval != 15*time.Minute {
			t.Fatalf("interval = %v, want 15m", cfg.Interval)
		}
		if cfg.BatchSize != 250 {
			t.Fatalf("batch size = %d, want 250", cfg.BatchSize)
		}
	})

	t.Run("malformed interval falls back to default", func(t *testing.T) {
		conf := &config.Config{}
		conf.GC.Interval = "not-a-duration"
		cfg := gcConfigFromYAML(conf)
		if cfg.Interval != 1*time.Hour {
			t.Fatalf("interval = %v, want 1h fallback", cfg.Interval)
		}
	})
}

func TestStartWorkerDisabledIsNoop(t *testing.T) {
	// With Enabled=false, StartWorker must return without touching the DB (rawDB
	// is nil here) and StopWorker must be safe and idempotent.
	s := &gcService{
		config: core.GCConfig{Enabled: false},
		log:    zap.NewNop(),
		stopCh: make(chan struct{}),
	}
	s.StartWorker(context.Background())
	s.StopWorker()
	s.StopWorker() // idempotent: must not panic on double close
}
