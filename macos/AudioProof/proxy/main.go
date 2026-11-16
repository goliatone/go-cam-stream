// Proof-only argument adapter. Production config and helper protocol stay unchanged.
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

type selection struct {
	Enabled bool   `json:"enabled"`
	UID     string `json:"uid"`
}

func adapt(args []string, s selection) ([]string, error) {
	capture := false
	for _, a := range args {
		if strings.HasPrefix(a, "rtsp://") {
			capture = true
		}
	}
	if !capture || !s.Enabled {
		return args, nil
	}
	if s.UID == "" || len(s.UID) > 1024 || !utf8.ValidString(s.UID) || strings.IndexFunc(s.UID, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid audio selection")
	}
	out := []string{}
	for i := range args {
		if args[i] == "-an" {
			continue
		}
		if args[i] == "-i" {
			out = append(out, "-camstream_audio_uid", s.UID)
		}
		if args[i] == "-f" && i+1 < len(args) && args[i+1] == "rtsp" {
			out = append(out, "-map", "0:v:0", "-map", "0:a:0", "-c:a", "libopus", "-ar", "48000", "-ac", "1", "-b:a", "64k", "-frame_duration", "20")
		}
		out = append(out, args[i])
	}
	return out, nil
}
func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	real := filepath.Join(filepath.Dir(exe), "ffmpeg-real")
	args := os.Args[1:]
	var s selection
	// Configuration is private and written by the isolated native proof only.
	path := os.Getenv("CAMSTREAM_AUDIO_PROOF_SELECTION")
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			os.Exit(1)
		}
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm()&0077 != 0 || len(b) > 4096 {
			os.Exit(1)
		}
		if json.Unmarshal(b, &s) != nil {
			os.Exit(1)
		}
	}
	args, err = adapt(args, s)
	if err != nil {
		os.Exit(1)
	}
	// Proof-only diagnostic seam: publisher stderr is otherwise discarded by the
	// unchanged helper. Keep it private; never copy raw diagnostics into evidence.
	publishing := false
	for _, arg := range args {
		publishing = publishing || strings.HasPrefix(arg, "rtsp://")
	}
	if path != "" && s.Enabled && publishing {
		log, e := os.OpenFile(filepath.Join(filepath.Dir(path), "capture-diagnostic.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0600)
		if e != nil || syscall.Dup2(int(log.Fd()), 2) != nil {
			os.Exit(1)
		}
		log.Close()
	}
	if syscall.Exec(real, append([]string{real}, args...), os.Environ()) != nil {
		os.Exit(1)
	}
}
