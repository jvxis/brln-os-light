package system

import (
	"runtime"
	"sync"
	"time"
)

const (
	cpuSamplerInterval      = 2 * time.Second
	cpuSamplerWindow        = 30 * time.Second
	cpuPercentFallbackDelay = 250 * time.Millisecond
)

type cpuUsageSample struct {
	At      time.Time
	Percent float64
}

type cpuUsageSnapshot struct {
	Latest      float64
	Average30s  float64
	SampleCount int
	Cores       int
}

type cpuUsageSampler struct {
	mu          sync.RWMutex
	initialized bool
	prevIdle    uint64
	prevTotal   uint64
	latest      float64
	samples     []cpuUsageSample
	cores       int
}

var (
	cpuSamplerOnce sync.Once
	cpuSamplerInst *cpuUsageSampler
)

func readCPUUsageSnapshot() (cpuUsageSnapshot, error) {
	snapshot := cpuUsageSamplerInstance().snapshot()
	if snapshot.SampleCount > 0 {
		return snapshot, nil
	}
	percent, err := readCPUPercent(cpuPercentFallbackDelay)
	if err != nil {
		return snapshot, err
	}
	return cpuUsageSnapshot{
		Latest:      percent,
		Average30s:  percent,
		SampleCount: 1,
		Cores:       runtime.NumCPU(),
	}, nil
}

func preferredCPUPercent(snapshot cpuUsageSnapshot) float64 {
	if snapshot.SampleCount > 0 {
		return snapshot.Average30s
	}
	return snapshot.Latest
}

func cpuUsageSamplerInstance() *cpuUsageSampler {
	cpuSamplerOnce.Do(func() {
		sampler := &cpuUsageSampler{
			cores: runtime.NumCPU(),
		}
		sampler.prime()
		go sampler.run()
		cpuSamplerInst = sampler
	})
	return cpuSamplerInst
}

func (s *cpuUsageSampler) prime() {
	idle, total, err := readCPUStat()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.initialized = true
	s.prevIdle = idle
	s.prevTotal = total
	s.mu.Unlock()
}

func (s *cpuUsageSampler) run() {
	ticker := time.NewTicker(cpuSamplerInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.collect()
	}
}

func (s *cpuUsageSampler) collect() {
	idle, total, err := readCPUStat()
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		s.initialized = true
		s.prevIdle = idle
		s.prevTotal = total
		return
	}

	usage, ok := cpuUsageFromDeltas(s.prevIdle, s.prevTotal, idle, total)
	s.prevIdle = idle
	s.prevTotal = total
	if !ok {
		return
	}

	now := time.Now()
	s.latest = usage
	s.samples = appendCPUUsageSample(s.samples, cpuUsageSample{
		At:      now,
		Percent: usage,
	}, cpuSamplerWindow)
}

func (s *cpuUsageSampler) snapshot() cpuUsageSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cpuUsageSnapshot{
		Latest:      s.latest,
		Average30s:  averageCPUUsageSamples(s.samples),
		SampleCount: len(s.samples),
		Cores:       s.cores,
	}
}

func cpuUsageFromDeltas(idle1, total1, idle2, total2 uint64) (float64, bool) {
	idle := idle2 - idle1
	total := total2 - total1
	if total == 0 || idle > total {
		return 0, false
	}
	usage := (1.0 - float64(idle)/float64(total)) * 100.0
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, true
}

func appendCPUUsageSample(samples []cpuUsageSample, sample cpuUsageSample, window time.Duration) []cpuUsageSample {
	samples = append(samples, sample)
	if window <= 0 {
		return samples
	}
	cutoff := sample.At.Add(-window)
	keep := 0
	for keep < len(samples) && samples[keep].At.Before(cutoff) {
		keep++
	}
	if keep == 0 {
		return samples
	}
	trimmed := make([]cpuUsageSample, len(samples)-keep)
	copy(trimmed, samples[keep:])
	return trimmed
}

func averageCPUUsageSamples(samples []cpuUsageSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total float64
	for _, sample := range samples {
		total += sample.Percent
	}
	return total / float64(len(samples))
}
