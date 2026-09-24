//go:build linux

package sys

import (
	"bufio"
	"context"
	"os"
	"strings"

	"sentinel/internal/core"
)

func collectProc(ctx context.Context, name string) ([]core.Metric, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		if sc.Scan() && strings.TrimSpace(sc.Text()) == name {
			count++
		}
	}
	alive := 0.0
	msg := "process " + name + " not found"
	if count > 0 {
		alive = 1
		msg = "process " + name + " running"
	}
	return []core.Metric{{
		Name: "proc_alive", Value: alive, Up: true,
		Message: msg, Labels: map[string]string{"proc": name},
	}}, nil
}
