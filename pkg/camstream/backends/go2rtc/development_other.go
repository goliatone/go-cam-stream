//go:build camstream_dev && !darwin

package go2rtc

func RunDevelopmentWorker([]string) (bool, int) { return false, 0 }
