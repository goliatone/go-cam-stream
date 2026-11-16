//go:build darwin && camstream_dev && !cgo

package go2rtc

const nativeSocketInspection = false

func nativeListenerOwned(int, int, listener) bool { return false }
