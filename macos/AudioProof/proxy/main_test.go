package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestAdapter(t *testing.T) {
	a := []string{"-f", "avfoundation", "-i", "MacBook Pro Camera:none", "-an", "-c:v", "libx264", "-f", "rtsp", "rtsp://127.0.0.1:18557/mic"}
	off, e := adapt(a, selection{})
	if e != nil || !reflect.DeepEqual(off, a) {
		t.Fatal("audio off changed")
	}
	on, e := adapt(a, selection{true, "UID:with space"})
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(on, "|")
	if strings.Contains(joined, "|-an|") || !strings.Contains(joined, "-camstream_audio_uid|UID:with space|-i") || !strings.Contains(joined, "-c:a|libopus") {
		t.Fatal("audio mapping missing")
	}
	for _, uid := range []string{"", "bad\nuid", strings.Repeat("x", 1025)} {
		if _, e = adapt(a, selection{true, uid}); e == nil {
			t.Fatal("invalid UID accepted")
		}
	}
	list := []string{"-f", "avfoundation", "-list_devices", "true", "-i", ""}
	out, e := adapt(list, selection{true, "id"})
	if e != nil || !reflect.DeepEqual(out, list) {
		t.Fatal("enumeration changed")
	}
}
