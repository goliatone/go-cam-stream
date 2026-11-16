package rtsp

import (
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	secrets "github.com/goliatone/go-secrets"
)

func normalizeHost(host string) (string, bool) {
	// A terminal DNS dot must not bypass IP checks (for example 0.0.0.0.).
	if !strings.Contains(host, ":") {
		host = strings.TrimSuffix(host, ".")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		return addr.String(), addr.Zone() == "" && !addr.IsUnspecified() && !addr.IsMulticast()
	}
	host = strings.ToLower(host)
	if len(host) == 0 || len(host) > 253 {
		return "", false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !validHostLabel(label) {
			return "", false
		}
	}
	return host, true
}

func validHostLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, ch := range label {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validID(value string) bool {
	return value != "" && len(value) <= MaxLabelBytes && strings.TrimSpace(value) == value && validText(value)
}

func validReferences(username, password secrets.Reference) bool {
	user, pass := username.URI(), password.URI()
	if user == "" || pass == "" {
		return user == "" && pass == ""
	}
	// References are opaque values; revalidate syntax without imposing a resolver
	// scheme or reading a secret. Do not retain provider errors as causes.
	_, userErr := secrets.ParseReference(user)
	_, passErr := secrets.ParseReference(pass)
	return userErr == nil && passErr == nil
}

func validProfile(p Profile) bool {
	return validID(string(p.ID)) && (p.Name == "" || validID(p.Name)) &&
		len(p.Path) > 0 && len(p.Path) <= MaxPathBytes && p.Path[0] == '/' &&
		validText(p.Path) && !strings.ContainsAny(p.Path, "?#") && validQuery(p.Query)
}

func validQuery(query string) bool {
	if len(query) > MaxQueryBytes {
		return false
	}
	// A raw query must already be escaped. Allow RFC 3986 query characters;
	// spaces, non-ASCII bytes and fragment delimiters must not reach request lines.
	for _, ch := range query {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
			strings.ContainsRune("-._~!$&'()*+,;=:@/?%", ch) {
			continue
		}
		return false
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return false
	}
	for key, entries := range values {
		if !validText(key) {
			return false
		}
		for _, entry := range entries {
			if !validText(entry) {
				return false
			}
		}
	}
	return true
}
