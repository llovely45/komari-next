//go:build darwin

package metrics

import (
	"math"
	"runtime"
	"testing"
)

func TestReadSystemDarwinProvidesNodeMetrics(t *testing.T) {
	system, err := ReadSystem()
	if err != nil {
		t.Fatalf("ReadSystem: %v", err)
	}

	if system.Release == "" {
		t.Fatal("Release is empty")
	}
	if system.Version == "" {
		t.Fatal("Version is empty")
	}
	if system.Uptime <= 0 {
		t.Fatalf("Uptime = %v, want positive", system.Uptime)
	}
	for index, load := range system.LoadAvg {
		if load < 0 || math.IsNaN(load) || math.IsInf(load, 0) {
			t.Fatalf("LoadAvg[%d] = %v, want a finite non-negative value", index, load)
		}
	}
	if system.TotalMem == 0 {
		t.Fatal("TotalMem is zero")
	}
	if system.FreeMem == 0 || system.FreeMem > system.TotalMem {
		t.Fatalf("FreeMem = %d, TotalMem = %d, want 0 < FreeMem <= TotalMem", system.FreeMem, system.TotalMem)
	}
	if len(system.CPUs) == 0 {
		t.Fatal("CPUs is empty")
	}
	if len(system.CPUs) != runtime.NumCPU() {
		t.Fatalf("len(CPUs) = %d, want runtime.NumCPU() = %d", len(system.CPUs), runtime.NumCPU())
	}
	for index, cpu := range system.CPUs {
		if cpu.Model == "" {
			t.Fatalf("CPUs[%d].Model is empty", index)
		}
		for _, field := range []string{"user", "nice", "sys", "idle", "irq"} {
			if _, ok := cpu.Times[field]; !ok {
				t.Fatalf("CPUs[%d].Times is missing %q", index, field)
			}
		}
	}
}

func TestReadProcessDarwinProvidesNodeMetrics(t *testing.T) {
	process, err := ReadProcess()
	if err != nil {
		t.Fatalf("ReadProcess: %v", err)
	}
	if process.UserCPU < 0 {
		t.Fatalf("UserCPU = %v, want non-negative", process.UserCPU)
	}
	if process.SystemCPU < 0 {
		t.Fatalf("SystemCPU = %v, want non-negative", process.SystemCPU)
	}
	if process.MaxRSS == 0 {
		t.Fatal("MaxRSS is zero")
	}
	if process.RSS == 0 {
		t.Fatal("RSS is zero")
	}
}
