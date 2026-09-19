//go:build darwin && cgo

package metrics

/*
#include <stdint.h>
#include <mach/mach_init.h>
#include <mach/mach_host.h>
#include <mach/host_info.h>
#include <mach/processor_info.h>
#include <mach/vm_map.h>

static int komari_read_cpu_times(uint32_t *out, uint32_t capacity, uint32_t *count) {
	if (out == NULL || count == NULL) {
		return -1;
	}

	natural_t cpu_count = 0;
	mach_msg_type_number_t info_count = 0;
	processor_info_array_t cpu_load = NULL;
	kern_return_t status = host_processor_info(
		mach_host_self(), PROCESSOR_CPU_LOAD_INFO, &cpu_count, &cpu_load, &info_count);
	if (status != KERN_SUCCESS) {
		return (int)status;
	}
	if (cpu_count == 0 || cpu_count > capacity) {
		vm_deallocate(mach_task_self(), (vm_address_t)cpu_load,
			(vm_size_t)(info_count * sizeof(integer_t)));
		return -2;
	}

	processor_cpu_load_info_t info = (processor_cpu_load_info_t)cpu_load;
	for (natural_t cpu = 0; cpu < cpu_count; cpu++) {
		for (int state = 0; state < CPU_STATE_MAX; state++) {
			out[cpu * CPU_STATE_MAX + state] = info[cpu].cpu_ticks[state];
		}
	}
	*count = (uint32_t)cpu_count;
	vm_deallocate(mach_task_self(), (vm_address_t)cpu_load,
		(vm_size_t)(info_count * sizeof(integer_t)));
	return 0;
}

*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

func darwinCPUs(model string, speed int) ([]CPUInfo, error) {
	capacity := runtime.NumCPU()
	if capacity < 1 {
		capacity = 1
	}
	ticks := make([]C.uint32_t, capacity*darwinCPUStateCount)
	var count C.uint32_t
	status := C.komari_read_cpu_times(
		(*C.uint32_t)(unsafe.Pointer(&ticks[0])), C.uint32_t(capacity), &count,
	)
	if status != 0 {
		return nil, fmt.Errorf("host_processor_info: status %d", int(status))
	}

	cpuCount := int(count)
	result := make([]CPUInfo, cpuCount)
	for index := range result {
		offset := index * darwinCPUStateCount
		result[index] = CPUInfo{
			Model: model,
			Speed: speed,
			Times: map[string]uint64{
				"user": darwinTicksToMilliseconds(uint32(ticks[offset])),
				"nice": darwinTicksToMilliseconds(uint32(ticks[offset+3])),
				"sys":  darwinTicksToMilliseconds(uint32(ticks[offset+1])),
				"idle": darwinTicksToMilliseconds(uint32(ticks[offset+2])),
				"irq":  0,
			},
		}
	}
	return result, nil
}
