//go:build darwin && !cgo

package metrics

import "runtime"

func darwinCPUs(model string, speed int) ([]CPUInfo, error) {
	count := runtime.NumCPU()
	if count < 1 {
		count = 1
	}
	result := make([]CPUInfo, count)
	for index := range result {
		result[index] = CPUInfo{
			Model: model,
			Speed: speed,
			Times: map[string]uint64{
				"user": 0,
				"nice": 0,
				"sys":  0,
				"idle": 0,
				"irq":  0,
			},
		}
	}
	return result, nil
}
