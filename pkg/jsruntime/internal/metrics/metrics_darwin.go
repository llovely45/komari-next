//go:build darwin

package metrics

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinCPUStateCount     = 4
	darwinCPUTicksPerSecond = 100
	darwinLoadAverageBytes  = 24
)

func ReadSystem() (System, error) {
	release, err := unix.Sysctl("kern.osrelease")
	if err != nil {
		return System{}, fmt.Errorf("kern.osrelease: %w", err)
	}
	version, err := unix.Sysctl("kern.version")
	if err != nil {
		return System{}, fmt.Errorf("kern.version: %w", err)
	}
	bootTime, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return System{}, fmt.Errorf("kern.boottime: %w", err)
	}
	loadAverage, err := darwinLoadAverage()
	if err != nil {
		return System{}, err
	}
	totalMemory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return System{}, fmt.Errorf("hw.memsize: %w", err)
	}
	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return System{}, fmt.Errorf("hw.pagesize: %w", err)
	}
	freePages, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return System{}, fmt.Errorf("vm.page_free_count: %w", err)
	}

	model := darwinCPUModel()
	cpus, err := darwinCPUs(model, darwinCPUSpeed())
	if err != nil {
		return System{}, err
	}

	uptime := time.Since(time.Unix(bootTime.Sec, int64(bootTime.Usec)*time.Microsecond.Nanoseconds())).Seconds()
	if uptime < 0 {
		return System{}, fmt.Errorf("kern.boottime is in the future")
	}

	return System{
		Release:  strings.TrimSpace(release),
		Version:  strings.TrimSpace(version),
		Uptime:   uptime,
		LoadAvg:  loadAverage,
		TotalMem: totalMemory,
		FreeMem:  uint64(freePages) * uint64(pageSize),
		CPUs:     cpus,
	}, nil
}

func darwinLoadAverage() ([3]float64, error) {
	var result [3]float64
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return result, fmt.Errorf("vm.loadavg: %w", err)
	}
	if len(raw) < darwinLoadAverageBytes {
		return result, fmt.Errorf("vm.loadavg: got %d bytes, want at least %d", len(raw), darwinLoadAverageBytes)
	}
	scale := binary.LittleEndian.Uint64(raw[16:24])
	if scale == 0 {
		return result, fmt.Errorf("vm.loadavg: invalid zero scale")
	}
	for index := range result {
		result[index] = float64(binary.LittleEndian.Uint32(raw[index*4:])) / float64(scale)
	}
	return result, nil
}

func darwinCPUModel() string {
	model, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil || strings.TrimSpace(model) == "" {
		model, _ = unix.Sysctl("hw.machine")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = runtime.GOARCH
	}
	return model
}

func darwinCPUSpeed() int {
	frequency, err := unix.SysctlUint64("hw.cpufrequency")
	if err != nil {
		return 0
	}
	return int(frequency / 1_000_000)
}

func darwinTicksToMilliseconds(ticks uint32) uint64 {
	return uint64(ticks) * 1_000 / darwinCPUTicksPerSecond
}

func ReadProcess() (Process, error) {
	usage := unix.Rusage{}
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return Process{}, fmt.Errorf("getrusage: %w", err)
	}
	rss, err := darwinRSS()
	if err != nil {
		return Process{}, err
	}
	maxRSS := uint64(0)
	if usage.Maxrss > 0 {
		maxRSS = uint64(usage.Maxrss)
	}
	if rss == 0 {
		rss = maxRSS
	}
	return Process{
		UserCPU:             darwinTimevalDuration(usage.Utime),
		SystemCPU:           darwinTimevalDuration(usage.Stime),
		MaxRSS:              maxRSS,
		RSS:                 rss,
		MinorPageFault:      usage.Minflt,
		MajorPageFault:      usage.Majflt,
		FSRead:              usage.Inblock,
		FSWrite:             usage.Oublock,
		VoluntarySwitches:   usage.Nvcsw,
		InvoluntarySwitches: usage.Nivcsw,
	}, nil
}

func darwinTimevalDuration(value unix.Timeval) time.Duration {
	return time.Duration(value.Sec)*time.Second + time.Duration(value.Usec)*time.Microsecond
}

// Darwin exposes proc_pidinfo through the proc_info system call. x/sys/unix
// exposes the system call number, but not the libproc wrapper or its structs.
type darwinProcTaskInfo struct {
	_            uint64
	residentSize uint64
	_            [4]uint64
	_            [12]int32
}

func darwinRSS() (uint64, error) {
	var info darwinProcTaskInfo
	const (
		darwinProcInfoCallPIDInfo = 2
		darwinProcPIDTaskInfo     = 4
	)
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		uintptr(darwinProcInfoCallPIDInfo),
		uintptr(unix.Getpid()),
		uintptr(darwinProcPIDTaskInfo),
		0,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if errno != 0 {
		return 0, fmt.Errorf("proc_info: %w", errno)
	}
	if returned != unsafe.Sizeof(info) {
		return 0, fmt.Errorf("proc_info: returned %d bytes, want %d", returned, unsafe.Sizeof(info))
	}
	return info.residentSize, nil
}
