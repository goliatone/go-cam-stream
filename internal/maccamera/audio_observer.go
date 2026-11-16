package maccamera

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type audioObserver struct {
	child  *child
	done   chan error
	source string
}

func (o *audioObserver) close() {
	o.child.stop()
	_ = os.Remove(o.source) //nolint:errcheck // Remove private credentials only after reaping the reader.
}

func (s *Service) observeAudio(c Config, secret credentials, dir string, progress *audioProgress) (*audioObserver, error) {
	source := filepath.Join(dir, "audio.ffconcat")
	input := fmt.Sprintf("ffconcat version 1.0\nfile 'rtsp://%s:%s@127.0.0.1:%d/%s'\noption rtsp_transport tcp\noption rw_timeout 5000000\noption probesize 32768\noption analyzeduration 500000\n", secret.Username, secret.Password, c.Port, c.Path)
	if err := writePrivate(source, []byte(input)); err != nil {
		return nil, audioFailure("audio_observer_failed")
	}
	cmd := exec.Command(s.Tools.FFprobe, "-v", "error", "-protocol_whitelist", "file,tcp,rtsp,rtp,udp", "-f", "concat", "-safe", "0", "-i", source, "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=codec_type,pts_time,duration_time:packet_side_data=", "-of", "compact=p=0") //nolint:noctx // Supervisor owns bounded stop/kill/reap, including on startup failure.
	raw, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.Remove(source) //nolint:errcheck // Best-effort cleanup preserves the primary operation result.
		return nil, audioFailure("audio_observer_failed")
	}
	reader, err := startChild(cmd)
	if err != nil {
		_ = raw.Close()       //nolint:errcheck // Best-effort cleanup preserves the primary operation result.
		_ = os.Remove(source) //nolint:errcheck // Best-effort cleanup preserves the primary operation result.
		return nil, audioFailure("audio_observer_failed")
	}
	o := &audioObserver{child: reader, source: source, done: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(raw)
		scanner.Buffer(make([]byte, 1024), 4096)
		previous := math.Inf(-1)
		for scanner.Scan() {
			if scanner.Text() == "" {
				continue
			}
			pts, ok := audioPacketTime(scanner.Text())
			if !ok || pts <= previous {
				o.done <- audioFailure("audio_observer_failed")
				return
			}
			previous = pts
			now := time.Now()
			progress.first.CompareAndSwap(nil, &now)
			progress.last.Store(&now)
			progress.packets.Add(1)
		}
		o.done <- audioFailure("audio_observer_failed")
	}()
	return o, nil
}

func audioPacketTime(line string) (float64, bool) {
	fields := strings.Split(line, "|")
	if len(fields) < 3 {
		return 0, false
	}
	values := make(map[string]string, 3)
	for _, field := range fields {
		if field == "" {
			continue
		}
		key, value, ok := strings.Cut(field, "=")
		if strings.HasPrefix(key, "side_datum/") {
			continue
		}
		if key != "codec_type" && key != "pts_time" && key != "duration_time" {
			return 0, false
		}
		if !ok || values[key] != "" {
			return 0, false
		}
		values[key] = value
	}
	return packetTiming(values)
}

func packetTiming(values map[string]string) (float64, bool) {
	pts, e1 := strconv.ParseFloat(values["pts_time"], 64)
	duration, e2 := strconv.ParseFloat(values["duration_time"], 64)
	return pts, values["codec_type"] == "audio" && e1 == nil && e2 == nil && !math.IsNaN(pts) && !math.IsInf(pts, 0) && duration > 0 && duration <= 0.12
}

// audioWatch is inactive for video-only capture. The supervisor owns its reader
// independently of the optional preview decoder.
type audioWatch struct {
	service  *Service
	config   Config
	secret   credentials
	dir      string
	progress *audioProgress
	observer *audioObserver
	observed <-chan error
}

func (w *audioWatch) close() {
	if w.observer != nil {
		w.observer.close()
	}
}
func (w *audioWatch) tick(frames int64) error {
	if !w.config.AudioEnabled {
		return nil
	}
	if last := w.progress.last.Load(); last != nil && time.Since(*last) >= audioStallTimeout {
		return audioFailure("audio_stalled")
	}
	if w.observer == nil && frames > 0 {
		var err error
		w.observer, err = w.service.observeAudio(w.config, w.secret, w.dir, w.progress)
		if err != nil {
			return err
		}
		w.observed = w.observer.done
	}
	return nil
}
