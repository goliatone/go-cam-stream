package viewer

import (
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	"gopkg.in/yaml.v3"
)

func normalizeDraft(input api.DraftInput) (api.DraftInput, rtsp.Config, error) {
	if !validCameraName(input.Name) || len(input.Endpoint) > 8192 {
		return input, rtsp.Config{}, invalid()
	}
	u, err := url.Parse(input.Endpoint)
	if err != nil || u.Scheme != "rtsp" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return input, rtsp.Config{}, invalid()
	}
	port := uint64(554)
	if u.Port() != "" {
		port, err = strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || port == 0 {
			return input, rtsp.Config{}, invalid()
		}
	}
	cfg, err := (rtsp.Config{Host: u.Hostname(), Port: uint16(port), Profiles: []rtsp.Profile{{ID: "default", Path: u.Path, Query: u.RawQuery}}}).Normalize()
	if err != nil {
		return input, rtsp.Config{}, invalid()
	}
	if input.Authentication != api.AuthenticationAnonymous && input.Authentication != api.AuthenticationCredentials {
		return input, cfg, invalid()
	}
	if input.Authentication == api.AuthenticationAnonymous && input.Credentials != nil {
		return input, cfg, invalid()
	}
	if input.Credentials != nil && !validManagedCredential(ManagedCredential{Username: []byte(input.Credentials.Username), Password: []byte(input.Credentials.Password)}) {
		return input, cfg, api.ManagementFailure(api.CodeCredentialsRequired)
	}
	input.Endpoint = endpointString(cfg.Host, cfg.Port, cfg.Profiles[0].Path, cfg.Profiles[0].Query)
	return input, cfg, nil
}

// validCameraName is the display-name rule for drafts and renames.
func validCameraName(name string) bool {
	return name != "" && len(name) <= rtsp.MaxLabelBytes && utf8.ValidString(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

// endpointString is the normalized address form reviews and settings return.
func endpointString(host string, port uint16, path, query string) string {
	u := url.URL{Scheme: "rtsp", Host: net.JoinHostPort(host, strconv.Itoa(int(port))), Path: path, RawQuery: query}
	return u.String()
}
func importCamera(request api.ImportDraftRequest) (api.DraftInput, error) {
	fail := func() (api.DraftInput, error) { return api.DraftInput{}, api.ManagementFailure(api.CodeImportInvalid) }
	if len(request.YAML) == 0 || len(request.YAML) > api.MaxImportYAMLBytes || len(request.Env) > api.MaxImportEnvBytes {
		return fail()
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(request.YAML))
	if decoder.Decode(&root) != nil || decoder.Decode(new(any)) != io.EOF || !safeYAML(&root, 0) {
		return fail()
	}
	var wire struct {
		Cameras []struct {
			ID         string `yaml:"id"`
			Name       string `yaml:"name"`
			PhysicalID string `yaml:"physical_id"`
			Strategy   string `yaml:"strategy"`
			Config     struct {
				Host        string `yaml:"host"`
				Port        uint16 `yaml:"port"`
				UsernameRef string `yaml:"username_ref"`
				PasswordRef string `yaml:"password_ref"`
				Profiles    []struct {
					ID    string `yaml:"id"`
					Path  string `yaml:"path"`
					Audio bool   `yaml:"audio"`
				} `yaml:"profiles"`
			} `yaml:"config"`
		} `yaml:"cameras"`
		Access []struct {
			Login  string `yaml:"login"`
			Grants []struct {
				CameraID  string   `yaml:"camera_id"`
				Streams   []string `yaml:"streams"`
				View      bool     `yaml:"view"`
				Diagnose  bool     `yaml:"diagnose"`
				Reconnect bool     `yaml:"reconnect"`
			} `yaml:"grants"`
		} `yaml:"access"`
	}
	decoder = yaml.NewDecoder(strings.NewReader(request.YAML))
	decoder.KnownFields(true)
	if decoder.Decode(&wire) != nil || len(wire.Cameras) != 1 {
		return fail()
	}
	camera := wire.Cameras[0]
	c := camera.Config
	if camera.Strategy != "rtsp" || c.Port == 0 || len(c.Profiles) != 1 || c.Profiles[0].ID != "default" || c.Profiles[0].Audio || c.UsernameRef != "env://MAC_CAMERA_USERNAME" || c.PasswordRef != "env://MAC_CAMERA_PASSWORD" {
		return fail()
	}
	u := url.URL{Scheme: "rtsp", Host: net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), Path: c.Profiles[0].Path}
	out := api.DraftInput{Name: camera.Name, Endpoint: u.String(), Authentication: api.AuthenticationCredentials}
	if request.Env != "" {
		values := map[string]string{}
		for line := range strings.SplitSeq(strings.ReplaceAll(request.Env, "\r\n", "\n"), "\n") {
			if line == "" {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || (key != "MAC_CAMERA_USERNAME" && key != "MAC_CAMERA_PASSWORD") || values[key] != "" || value == "" {
				return fail()
			}
			// The exporter emits camera and a 48-character hexadecimal password. No
			// dotenv dialect, quoting, interpolation or shell execution is supported.
			if key == "MAC_CAMERA_USERNAME" && value != "camera" {
				return fail()
			}
			if key == "MAC_CAMERA_PASSWORD" {
				if len(value) != 48 || strings.ContainsAny(value, "\r\n") {
					return fail()
				}
				for _, ch := range value {
					if !strings.ContainsRune("0123456789abcdef", ch) {
						return fail()
					}
				}
			}
			values[key] = value
		}
		if len(values) != 2 {
			return fail()
		}
		out.Credentials = &api.Credentials{Username: values["MAC_CAMERA_USERNAME"], Password: values["MAC_CAMERA_PASSWORD"]}
	}
	out, _, err := normalizeDraft(out)
	if err != nil {
		return fail()
	}
	return out, nil
}
func safeYAML(node *yaml.Node, depth int) bool {
	if depth > 20 || node.Kind == yaml.AliasNode || node.Anchor != "" || node.Style&yaml.TaggedStyle != 0 {
		return false
	}
	switch node.Tag {
	case "", "!!map", "!!seq", "!!str", "!!int", "!!bool", "!!null":
	default:
		return false
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return false
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if !safeYAML(child, depth+1) {
			return false
		}
	}
	return true
}
