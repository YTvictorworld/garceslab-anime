package eval

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RepoRoot returns the root of this repository when path is inside it. Eval data
// and outputs (real subtitles, official translations, model outputs) must never be
// inside the repository; the tool refuses such paths.
func RepoRoot(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if isThisRepo(dir) {
			return dir, true
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", false
		}
	}
}

func isThisRepo(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if strings.TrimSpace(first) != "module seanime" {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "internal", "subtranslate"))
	return err == nil && fi.IsDir()
}

// InGitWorkTree reports the enclosing git work tree of path, if any (a warning: eval
// data in any repository risks being committed).
func InGitWorkTree(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", false
		}
	}
}

// VRAMSampler polls nvidia-smi while a run goes on: peak used memory, peak
// temperature and whether the GPU slowed down for heat or power (laptops do, and then
// the speed numbers mean little). Without nvidia-smi it records nothing.
type VRAMSampler struct {
	mu        sync.Mutex
	peak      int
	maxTemp   int
	throttled bool
	stop      context.CancelFunc
	done      chan struct{}
}

// Clock event reasons that mean "slowed down" (nvidia-smi clocks_event_reasons):
// SW power cap 0x4, HW slowdown 0x8, SW thermal 0x20, HW thermal 0x40, HW power
// brake 0x80.
const slowdownMask = 0x4 | 0x8 | 0x20 | 0x40 | 0x80

// StartVRAM starts sampling every interval (GPU index gpu, "" = all GPUs, max taken).
func StartVRAM(gpu string, interval time.Duration) *VRAMSampler {
	s := &VRAMSampler{done: make(chan struct{})}
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		close(s.done)
		return s
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	go func() {
		defer close(s.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			s.sample(ctx, gpu)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return s
}

func (s *VRAMSampler) sample(ctx context.Context, gpu string) {
	args := []string{"--query-gpu=memory.used,temperature.gpu,clocks_throttle_reasons.active", "--format=csv,noheader,nounits"}
	if gpu != "" {
		args = append(args, "-i", gpu)
	}
	out, err := exec.CommandContext(ctx, "nvidia-smi", args...).Output()
	if err != nil {
		return
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		mem, temp, reasons := parseGPUSample(sc.Text())
		s.mu.Lock()
		s.peak = max(s.peak, mem)
		s.maxTemp = max(s.maxTemp, temp)
		s.throttled = s.throttled || reasons&slowdownMask != 0
		s.mu.Unlock()
	}
}

func parseGPUSample(line string) (mem, temp int, reasons uint64) {
	f := strings.Split(line, ",")
	if len(f) > 0 {
		mem, _ = strconv.Atoi(strings.TrimSpace(f[0]))
	}
	if len(f) > 1 {
		temp, _ = strconv.Atoi(strings.TrimSpace(f[1]))
	}
	if len(f) > 2 {
		reasons, _ = strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(f[2]), "0x"), 16, 64)
	}
	return mem, temp, reasons
}

// Stop ends sampling.
func (s *VRAMSampler) Stop() {
	if s.stop != nil {
		s.stop()
	}
	<-s.done
}

// Read returns the peak memory (MiB), the peak temperature (°C) and whether the GPU
// slowed down so far.
func (s *VRAMSampler) Read() (peakMiB, maxTempC int, throttled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak, s.maxTemp, s.throttled
}

// Reset starts a new measurement window (one item).
func (s *VRAMSampler) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peak, s.maxTemp, s.throttled = 0, 0, false
}
