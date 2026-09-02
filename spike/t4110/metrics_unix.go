//go:build darwin

package t4110

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const phaseProcessSampleInterval = 50 * time.Millisecond

type phaseProcessSampler struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	peak   int64
	err    error
}

func startPhaseProcessSampler(ctx context.Context) (*phaseProcessSampler, error) {
	sampleCtx, cancel := context.WithCancel(ctx)
	sampler := &phaseProcessSampler{ctx: sampleCtx, cancel: cancel, done: make(chan struct{})}
	if err := sampler.sample(); err != nil {
		cancel()
		return nil, err
	}
	go sampler.run()
	return sampler, nil
}

func (sampler *phaseProcessSampler) run() {
	defer close(sampler.done)
	ticker := time.NewTicker(phaseProcessSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-sampler.ctx.Done():
			return
		case <-ticker.C:
			if err := sampler.sample(); err != nil {
				if errors.Is(err, context.Canceled) &&
					errors.Is(sampler.ctx.Err(), context.Canceled) {
					return
				}
				sampler.mu.Lock()
				sampler.err = errors.Join(sampler.err, err)
				sampler.mu.Unlock()
				return
			}
		}
	}
}

func (sampler *phaseProcessSampler) sample() error {
	ctx, cancel := context.WithTimeout(sampler.ctx, 2*time.Second)
	defer cancel()
	observed, err := t4013.ObserveProcessTree(ctx, os.Getpid())
	if err != nil {
		return err
	}
	sampler.mu.Lock()
	sampler.peak = max(sampler.peak, observed.RSSBytes)
	sampler.mu.Unlock()
	return nil
}

func (sampler *phaseProcessSampler) stop() (int64, error) {
	sampler.cancel()
	<-sampler.done
	finalCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	observed, finalErr := t4013.ObserveProcessTree(finalCtx, os.Getpid())
	cancel()
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.peak = max(sampler.peak, observed.RSSBytes)
	return sampler.peak, errors.Join(sampler.err, finalErr)
}

func remainingProcessChildren(ctx context.Context) (int, error) {
	observed, err := t4013.ObserveProcessTree(ctx, os.Getpid())
	return observed.Descendants, err
}

func diskUsage(root string) (logical, allocated int64, retErr error) {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			if info.Size() > math.MaxInt64-logical {
				return errors.New("T41.10 logical-byte measurement overflow")
			}
			logical += info.Size()
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Blocks < 0 || stat.Blocks > math.MaxInt64/512 {
			return errors.New("T41.10 allocated-byte measurement is unavailable")
		}
		bytes := int64(stat.Blocks) * 512
		if bytes > math.MaxInt64-allocated {
			return errors.New("T41.10 allocated-byte measurement overflow")
		}
		allocated += bytes
		return nil
	})
	return logical, allocated, err
}
