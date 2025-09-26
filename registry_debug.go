//go:build ebusdebug

package ebus

import (
	"fmt"
	"runtime"
)

func captureOrigin(skip int) string {
	if _, file, line, ok := runtime.Caller(skip); ok {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return "unknown"
}
