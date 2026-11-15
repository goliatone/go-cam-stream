package maccamera

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type Service struct {
	Dir   string
	Tools Tools
}

func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "camstream-camera"), err
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("camera directory must be owned by this user with mode 0700")
	}
	return nil
}

func (s *Service) lock() (*os.File, error) {
	return s.namedLock("instance.lock")
}

func (s *Service) namedLock(name string) (*os.File, error) {
	if err := privateDir(s.Dir); err != nil {
		return nil, err
	}
	name = filepath.Join(s.Dir, name)
	fd, err := syscall.Open(name, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close() //nolint:errcheck // Preserve the lock-acquisition error.
		return nil, errors.New("camera is already running or being configured")
	}
	return f, nil
}

// Configuration returns non-secret camera settings. Credentials never cross the
// native app protocol; explicit export returns private file locations only.
func (s *Service) Configuration() (Config, error) {
	c, _, err := load(s.Dir)
	return c, err
}

type Exports struct {
	Configuration string `json:"configuration"`
	Environment   string `json:"environment"`
}

func (s *Service) Export() (Exports, error) {
	lock, err := s.lock()
	if err != nil {
		return Exports{}, err
	}
	defer lock.Close() //nolint:errcheck // Closing releases the configuration lock.
	c, secret, err := load(s.Dir)
	if err != nil {
		return Exports{}, err
	}
	if err = s.export(c, secret); err != nil {
		return Exports{}, err
	}
	return Exports{Configuration: filepath.Join(s.Dir, "camstream-camera.yaml"), Environment: filepath.Join(s.Dir, "camstream-camera.env")}, nil
}

func (s *Service) runtimeDir() (string, error) {
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return "", err
	}
	// A short path avoids Darwin's Unix socket path length limit.
	if real, resolveErr := filepath.EvalSymlinks(dir); resolveErr == nil {
		dir = real
	}
	hash := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("/tmp/camstream-camera-%d-%x", os.Getuid(), hash[:8]), nil
}

func (s *Service) Configure(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer lock.Close() //nolint:errcheck // Closing releases the flock.
	devices, err := s.Devices(ctx)
	if err != nil {
		return err
	}
	c.Device, err = selectDevice(c.Device, devices)
	if err != nil {
		return err
	}
	secretPath := filepath.Join(s.Dir, "credentials.json")
	var secret credentials
	if err = readJSON(secretPath, &secret); errors.Is(err, os.ErrNotExist) {
		secret, err = newCredentials()
		if err != nil {
			return err
		}
		if err = writeJSON(secretPath, secret); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = secret.validate(); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		return err
	}
	return s.export(c, secret)
}

func (s *Service) export(c Config, secret credentials) error {
	if err := writePrivate(filepath.Join(s.Dir, "camstream-camera.yaml"), snippet(c)); err != nil {
		return err
	}
	env := fmt.Sprintf("MAC_CAMERA_USERNAME=%s\nMAC_CAMERA_PASSWORD=%s\n", secret.Username, secret.Password)
	return writePrivate(filepath.Join(s.Dir, "camstream-camera.env"), []byte(env))
}

// Start remains attached to the terminal. The supervisor receives no secrets in
// its arguments; EOF on the lifetime pipe stops its children if this owner dies.
func (s *Service) Start(ctx context.Context, out, stderr io.Writer) error {
	if _, _, err := load(s.Dir); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__supervise", "--dir", s.Dir) //nolint:noctx // Cancellation closes the lifetime pipe so the supervisor can reap its children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err = cmd.Start(); err != nil {
		_ = pipe.Close() //nolint:errcheck // EOF, including a previously closed pipe, terminates the supervisor.
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = pipe.Close() //nolint:errcheck // EOF, including a previously closed pipe, terminates the supervisor.
		case <-done:
		}
	}()
	err = cmd.Wait()
	_ = pipe.Close() //nolint:errcheck // EOF, including a previously closed pipe, terminates the supervisor.
	if err != nil {
		return errors.New("camera stopped with an error; see the supervisor message")
	}
	return nil
}

type Status struct {
	Preview *PreviewEndpoint `json:"preview,omitempty"`
	State   string           `json:"state"`
	URL     string           `json:"url,omitempty"`
	Frames  int64            `json:"frames,omitempty"`
}

func (s *Service) request(ctx context.Context, action string) (Status, error) {
	dir, err := s.runtimeDir()
	if err != nil {
		return Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return Status{State: "stopped"}, nil
		}
		return Status{}, err
	}
	defer conn.Close() //nolint:errcheck // Connection is no longer used.
	if err = conn.SetDeadline(time.Now().Add(6 * time.Second)); err != nil {
		return Status{}, err
	}
	if err = json.NewEncoder(conn).Encode(action); err != nil {
		return Status{}, err
	}
	var status Status
	err = json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&status)
	return status, err
}

func (s *Service) Status(ctx context.Context) (Status, error) { return s.request(ctx, "status") }

func (s *Service) Stop(ctx context.Context) error {
	status, err := s.request(ctx, "stop")
	if err != nil || status.State == "stopped" {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("stop not confirmed; inspect the foreground camera process")
		case <-ticker.C:
			status, err = s.Status(ctx)
			if err != nil {
				return err
			}
			if status.State == "stopped" {
				return nil
			}
		}
	}
}
