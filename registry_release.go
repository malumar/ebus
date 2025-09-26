//go:build !ebusdebug

package ebus

const comment = "enable ebusdebug build flag to capture first registration origin"

func captureOrigin(skip int) string {
	return comment
}
