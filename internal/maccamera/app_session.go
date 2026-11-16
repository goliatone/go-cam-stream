package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// AppRequest/Reply are the version-one NDJSON contract used by the native shell.
// All fields are bounded and no error embeds a raw process/configuration error.
type AppRequest struct {
	Version           int     `json:"version"`
	ID                string  `json:"id"`
	Method            string  `json:"method"`
	Config            *Config `json:"config,omitempty"`
	PreviewGeneration string  `json:"preview_generation,omitempty"`
}

type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type AppReply struct {
	Version int       `json:"version"`
	ID      string    `json:"id,omitempty"`
	Event   string    `json:"event,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *AppError `json:"error,omitempty"`
}

func appError(code string) *AppError {
	if message, ok := audioMessages[code]; ok {
		return &AppError{Code: code, Message: message}
	}
	messages := map[string]string{
		"preview_unavailable":  "Preview unavailable; retry while the camera is streaming.",
		"invalid_request":      "Unsupported or malformed camera request.",
		"busy":                 "Camera is already owned or running.",
		"not_configured":       "Configure the camera before starting.",
		"configuration_failed": "Camera settings could not be saved.",
		"device_unavailable":   "Camera discovery failed; check permission and device availability.",
		"capture_failed":       "Capture stopped; check camera permission, device and runtime.",
		"status_unavailable":   "Camera status is unavailable.",
		"export_failed":        "Private connection files could not be exported; stop capture first.",
	}
	return &AppError{Code: code, Message: messages[code]}
}

func decodeAppRequest(b []byte) (AppRequest, error) {
	var r AppRequest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if d.Decode(new(any)) != io.EOF || r.Version != 1 || !safeID.MatchString(r.ID) {
		return r, errors.New("invalid request")
	}
	if r.Method == "configure" {
		if r.Config == nil {
			return r, errors.New("missing config")
		}
		if err := r.Config.Validate(); err != nil {
			return r, err
		}
	} else if r.Config != nil {
		return r, errors.New("unexpected config")
	}
	if r.Method == "preview_detach" {
		if _, err := parsePreviewGeneration(r.PreviewGeneration); err != nil {
			return r, err
		}
	} else if r.PreviewGeneration != "" {
		return r, errors.New("unexpected preview generation")
	}
	switch r.Method {
	case "preview_attach", "preview_detach", "audio_devices", "devices", "config", "configure", "start", "status", "stop", "export", "quit":
		return r, nil
	default:
		return r, errors.New("unknown method")
	}
}

type appSession struct {
	service *Service
	encoder *json.Encoder
	run     func(context.Context) error
	running <-chan error
	stop    context.CancelFunc
	failure *AppError
}

// AppSession binds all owned capture to the native parent's stdin pipe. EOF,
// read failure, a signal or protocol failure cancels and reaps owned capture.
// The caller must keep the pipe open, and must not invoke CLI start detached.
func (s *Service) AppSession(ctx context.Context, in io.ReadCloser, out io.Writer) error {
	session := &appSession{service: s, encoder: json.NewEncoder(out)}
	session.run = func(ctx context.Context) error { return s.Start(ctx, io.Discard, io.Discard) }
	return session.serve(ctx, in)
}

func (a *appSession) serve(ctx context.Context, in io.ReadCloser) error {
	lock, err := a.service.namedLock("app-session.lock")
	if err != nil {
		return a.encoder.Encode(AppReply{Version: 1, Event: "session_error", Error: appError("busy")})
	}
	defer lock.Close() //nolint:errcheck // Release the native session lock after capture cleanup.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer in.Close() //nolint:errcheck // Unblock the protocol reader on shutdown.
	defer a.stopOwned()
	requests := readRequests(ctx, in, cancel)
	if err = a.encoder.Encode(AppReply{Version: 1, Event: "ready", Result: map[string]int{"protocol": 1, "preview": 1, "audio": 1}}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case captureErr := <-a.running:
			a.stop()
			a.running = nil
			a.stop = nil
			r := AppReply{Version: 1, Event: "capture_stopped", Result: a.service.stoppedStatus("stopped")}
			if captureErr != nil {
				r.Error = CaptureFailure(captureErr)
				a.failure = r.Error
				r.Result = a.service.stoppedStatus("failed")
			}
			if err = a.encoder.Encode(r); err != nil {
				return err
			}
		case line := <-requests:
			if ctx.Err() != nil {
				return nil
			}
			r, decodeErr := decodeAppRequest(line)
			if decodeErr != nil {
				reply := AppReply{Version: 1, Error: appError("invalid_request")}
				if _, ok := errors.AsType[audioError](decodeErr); ok {
					reply.ID = r.ID
					reply.Error = CaptureFailure(decodeErr)
				}
				if err = a.encoder.Encode(reply); err != nil {
					return err
				}
				continue
			}
			result, requestErr := a.dispatch(ctx, r)
			if err = a.encoder.Encode(AppReply{Version: 1, ID: r.ID, Result: result, Error: requestErr}); err != nil {
				return err
			}
			if r.Method == "quit" {
				return nil
			}
		}
	}
}

func readRequests(ctx context.Context, in io.Reader, cancel context.CancelFunc) <-chan []byte {
	requests := make(chan []byte, 8)
	go func() {
		defer cancel()
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 16384)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case <-ctx.Done():
				return
			case requests <- line:
			default:
				return
			}
		}
	}()
	return requests
}

func (a *appSession) stopOwned() {
	if a.stop != nil {
		a.stop()
		<-a.running //nolint:errcheck // Cancellation is intentional; waiting guarantees owned capture is reaped.
		a.running = nil
		a.stop = nil
	}
}

func (a *appSession) startOwned(ctx context.Context) (any, *AppError) {
	if a.running != nil {
		return nil, appError("busy")
	}
	c, err := a.service.Configuration()
	if err != nil {
		return nil, appError("not_configured")
	}
	status, err := a.service.Status(ctx)
	if err != nil {
		return nil, appError("status_unavailable")
	}
	if status.State != "stopped" {
		return nil, appError("busy")
	}
	a.failure = nil
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	a.running = done
	a.stop = cancel
	go func() { done <- a.run(ctx) }()
	return Status{State: "starting", Audio: (&audioProgress{}).status(c, "starting")}, nil
}

func (a *appSession) dispatch(ctx context.Context, r AppRequest) (any, *AppError) {
	if r.Method == "start" {
		return a.startOwned(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	switch r.Method {
	case "preview_attach", "preview_detach":
		return a.dispatchPreview(ctx, r)
	case "audio_devices":
		v, err := a.service.AudioDevices(ctx)
		if err != nil {
			return nil, CaptureFailure(err)
		}
		return v, nil
	case "devices":
		v, err := a.service.Devices(ctx)
		if err != nil {
			return nil, appError("device_unavailable")
		}
		return v, nil
	case "config":
		v, err := a.service.Configuration()
		if err != nil {
			return nil, appError("not_configured")
		}
		return v, nil
	case "configure":
		return a.configure(ctx, *r.Config)
	case "status":
		v, err := a.service.Status(ctx)
		if err != nil {
			return nil, appError("status_unavailable")
		}
		if v.State == "stopped" && a.failure != nil {
			return a.service.stoppedStatus("failed"), a.failure
		}
		return v, nil
	case "stop", "quit":
		a.stopOwned()
		a.failure = nil
		return a.service.stoppedStatus("stopped"), nil
	case "export":
		return a.exportStopped()
	default:
		return nil, appError("invalid_request")
	}
}

func (a *appSession) exportStopped() (any, *AppError) {
	if a.running != nil {
		return nil, appError("export_failed")
	}
	v, err := a.service.Export()
	if err != nil {
		return nil, appError("export_failed")
	}
	return v, nil
}

func (a *appSession) dispatchPreview(ctx context.Context, r AppRequest) (any, *AppError) {
	switch r.Method {
	case "preview_attach":
		if a.running == nil {
			return nil, appError("preview_unavailable")
		}
		v, err := (AttachPreviewHandler{Service: a.service}).Run(ctx, AttachPreview{})
		if err != nil {
			return nil, appError("preview_unavailable")
		}
		return v, nil
	case "preview_detach":
		if a.running == nil {
			return Status{State: "stopped"}, nil
		}
		if err := (DetachPreviewHandler{Service: a.service}).Execute(ctx, DetachPreview{Generation: r.PreviewGeneration}); err != nil {
			return nil, appError("preview_unavailable")
		}
		return map[string]bool{"detached": true}, nil
	default:
		return nil, appError("invalid_request")
	}
}

func (a *appSession) configure(ctx context.Context, c Config) (any, *AppError) {
	if a.running != nil {
		return nil, appError("busy")
	}
	if err := (ConfigureHandler{Service: a.service}).Execute(ctx, Configure{Config: c}); err != nil {
		if _, ok := errors.AsType[audioError](err); ok {
			return nil, CaptureFailure(err)
		}
		return nil, appError("configuration_failed")
	}
	a.failure = nil
	v, err := a.service.Configuration()
	if err != nil {
		return nil, appError("configuration_failed")
	}
	return v, nil
}
