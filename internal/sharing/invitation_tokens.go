package sharing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-urlkit/securelink"
)

type secureInvitations struct{ key string }

func NewInvitationTokens(key []byte) (InvitationTokens, error) {
	if len(key) < 32 {
		return nil, fault(api.ShareInvalidRequest)
	}
	return secureInvitations{key: string(key)}, nil
}

func (t secureInvitations) manager(ttl time.Duration) (securelink.Manager, error) {
	return securelink.NewManager(securelink.Config{
		BaseURL: "https://invitation.invalid", QueryKey: "token", AsQuery: true,
		Routes: map[string]string{"share": "/s"}, Expiration: ttl,
		SigningKey: t.key, SigningMethod: jwt.SigningMethodHS256,
	})
}

func (t secureInvitations) Issue(claims InvitationClaims, ttl time.Duration) (Secret, error) {
	if !claims.valid() || (ttl != 15*time.Minute && ttl != time.Hour && ttl != 8*time.Hour) {
		return "", fault(api.ShareInvalidRequest)
	}
	manager, err := t.manager(ttl)
	if err != nil {
		return "", fault(api.ShareInvalidRequest)
	}
	raw, err := manager.Generate("share", securelink.Payload{
		"v": claims.Version, "purpose": claims.Purpose, "share_id": claims.ShareID, "nonce": claims.Nonce,
	})
	if err != nil {
		return "", fault(api.ShareInvalidRequest)
	}
	link, err := url.Parse(raw)
	if err != nil || link.Query().Get("token") == "" {
		return "", fault(api.ShareInvalidRequest)
	}
	return Secret(link.Query().Get("token")), nil
}

func (t secureInvitations) Verify(token Secret) (InvitationClaims, error) {
	if (api.RedeemShareRequest{Invitation: string(token)}).Validate() != nil {
		return InvitationClaims{}, fault(api.ShareInvalidInvitation)
	}
	manager, err := t.manager(time.Hour)
	if err != nil {
		return InvitationClaims{}, fault(api.ShareInvalidInvitation)
	}
	if _, err = manager.Validate(string(token)); err != nil {
		return InvitationClaims{}, fault(api.ShareInvalidInvitation)
	}
	parts := strings.Split(string(token), ".")
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return InvitationClaims{}, fault(api.ShareInvalidInvitation)
	}
	var signed struct {
		Claims InvitationClaims `json:"dat"`
		Issued int64            `json:"iat"`
		Expiry int64            `json:"exp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&signed) != nil || signed.Issued <= 0 || signed.Expiry <= signed.Issued || !signed.Claims.valid() {
		return InvitationClaims{}, fault(api.ShareInvalidInvitation)
	}
	return signed.Claims, nil
}

func (c InvitationClaims) valid() bool {
	return c.Version == InvitationVersion && c.Purpose == InvitationPurpose && api.ValidShareID(c.ShareID) && api.ValidShareID(c.Nonce)
}
