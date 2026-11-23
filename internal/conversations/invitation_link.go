package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

type NativeLink struct {
	Origin     string
	Room       api.RoomRef
	Invitation string
}

func (NativeLink) String() string   { return "[native room link redacted]" }
func (NativeLink) GoString() string { return "[native room link redacted]" }

// ParseNativeLink accepts the original HTTPS invitation and its Camstream
// equivalent. The link never grants authority to trust a new HTTPS origin.
func ParseNativeLink(raw string, trusted []string) (NativeLink, error) {
	return parseNativeLink(raw, trusted, false)
}

func parseNativeLink(raw string, trusted []string, loopback bool) (NativeLink, error) {
	invalid := Fault(api.RoomInvitationInvalid)
	if len(raw) > 8192 || strings.TrimSpace(raw) != raw {
		return NativeLink{}, invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.RawFragment != "" || u.ForceQuery {
		return NativeLink{}, invalid
	}
	var link NativeLink
	switch u.Scheme {
	case "https", "http":
		if !strings.HasPrefix(u.Path, "/r/") || u.RawQuery != "" {
			return NativeLink{}, invalid
		}
		link.Origin = u.Scheme + "://" + u.Host
		link.Room.RoomID = strings.TrimPrefix(u.Path, "/r/")
	case api.ConversationJoinScheme:
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil || u.Host != "conversation" || u.Path != "/join" || len(query) != 3 ||
			len(query["origin"]) != 1 || len(query["room"]) != 1 || len(query["v"]) != 1 {
			return NativeLink{}, invalid
		}
		if query.Get("v") != "1" {
			return NativeLink{}, Fault(api.RoomIncompatibleClient)
		}
		link.Origin, link.Room.RoomID = query.Get("origin"), query.Get("room")
	default:
		return NativeLink{}, invalid
	}
	link.Invitation = u.Fragment
	if link.Room.Validate() != nil || (api.RoomRedeemRequest{Invitation: link.Invitation}).Validate() != nil {
		return NativeLink{}, invalid
	}
	link.Origin, err = config.ConversationOrigin(link.Origin, loopback)
	if err != nil {
		return NativeLink{}, invalid
	}
	for _, value := range trusted {
		if origin, err := config.ConversationOrigin(value, loopback); err == nil && origin == link.Origin {
			return link, nil
		}
	}
	return NativeLink{}, Fault(api.RoomPermissionDenied)
}

func RedeemNativeLink(ctx context.Context, raw string, trusted []string, client *http.Client) (api.NativeConversationPrejoin, error) {
	return redeemNativeLink(ctx, raw, trusted, client, false, "")
}

func RedeemLoopbackLink(ctx context.Context, raw string, trusted []string) (api.NativeConversationPrejoin, error) {
	return redeemNativeLink(ctx, raw, trusted, nil, true, "")
}

func RedeemRecoverableLink(ctx context.Context, raw string, trusted []string, loopback bool, token string) (api.NativeConversationPrejoin, error) {
	value, err := redeemNativeLink(ctx, raw, trusted, nil, loopback, token)
	if token != "" && err == Fault(api.RoomInvalidRequest) {
		// Earlier room hosts reject the new field before taking a seat.
		return redeemNativeLink(ctx, raw, trusted, nil, loopback, "")
	}
	return value, err
}

func ReleaseNativeAttempt(ctx context.Context, raw string, trusted []string, loopback bool, token string) error {
	link, err := parseNativeLink(raw, trusted, loopback)
	if err != nil {
		return err
	}
	return ReleaseNativeSeat(ctx, api.NativeConversationPrejoin{
		PageURL:   link.Origin + "/r/" + link.Room.RoomID,
		Admission: api.RoomAdmission{Room: api.RoomView{RoomRef: link.Room}, Role: api.RoomGuest, Token: token},
	})
}

func redeemNativeLink(ctx context.Context, raw string, trusted []string, client *http.Client, loopback bool, token string) (api.NativeConversationPrejoin, error) {
	link, err := parseNativeLink(raw, trusted, loopback)
	if err != nil {
		return api.NativeConversationPrejoin{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	strict := privateConversationClient(client)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	input := api.RoomRedeemRequest{Version: api.ConversationVersion, Invitation: link.Invitation, GuestToken: token}
	body, err := json.Marshal(input)
	if err != nil {
		return api.NativeConversationPrejoin{}, Fault(api.RoomInvitationInvalid)
	}
	target := link.Origin + api.ConversationAPIPrefix + link.Room.RoomID + "/redeem"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return api.NativeConversationPrejoin{}, Fault(api.RoomInvitationInvalid)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", link.Origin)
	response, err := strict.Do(req)
	if err != nil {
		return api.NativeConversationPrejoin{}, Fault(api.RoomMediaUnreachable)
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16*1024))
	if response.StatusCode != http.StatusOK {
		var failure api.RoomErrorResponse
		if decoder.Decode(&failure) == nil {
			switch failure.Error.Code {
			case api.RoomInvalidRequest:
				if response.StatusCode == http.StatusBadRequest && decoder.Decode(new(any)) == io.EOF {
					return api.NativeConversationPrejoin{}, Fault(api.RoomInvalidRequest)
				}
			case api.RoomFull, api.RoomInvitationInvalid, api.RoomAccessExpired, api.RoomAccessEnded,
				api.RoomPermissionDenied, api.RoomIncompatibleClient:
				return api.NativeConversationPrejoin{}, Fault(failure.Error.Code)
			}
		}
		return api.NativeConversationPrejoin{}, Fault(api.RoomMediaUnreachable)
	}
	var admission api.RoomAdmission
	if err := decoder.Decode(&admission); err != nil || decoder.Decode(new(any)) != io.EOF ||
		admission.Room.RoomRef != link.Room || admission.Room.Version != api.ConversationVersion ||
		admission.Room.Generation.Validate() != nil || admission.Role != api.RoomGuest || !api.ValidShareID(admission.Token) {
		return api.NativeConversationPrejoin{}, Fault(api.RoomIncompatibleClient)
	}
	return api.NativeConversationPrejoin{PageURL: link.Origin + "/r/" + link.Room.RoomID, Admission: admission}, nil
}

func privateConversationClient(client *http.Client) http.Client {
	strict := *client
	strict.Jar = nil
	strict.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return strict
}

// ReleaseNativeSeat uses the scoped guest admission returned by redemption.
// The owner helper retains this admission in memory so closing the native
// surface can release a seat even if the page never loaded.
func ReleaseNativeSeat(ctx context.Context, guest api.NativeConversationPrejoin) error {
	page, err := url.Parse(guest.PageURL)
	if err != nil || page.User != nil || page.RawPath != "" || page.ForceQuery || page.RawQuery != "" || page.Fragment != "" ||
		guest.Admission.Role != api.RoomGuest || !api.ValidShareID(guest.Admission.Token) ||
		guest.Admission.Room.RoomRef.Validate() != nil || page.Path != "/r/"+guest.Admission.Room.RoomID {
		return api.ErrInvalidConversationRequest
	}
	origin, err := config.ConversationOrigin(page.Scheme+"://"+page.Host, true)
	if err != nil {
		return api.ErrInvalidConversationRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	target := origin + api.ConversationAPIPrefix + guest.Admission.Room.RoomID + "/leave"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("{}"))
	if err != nil {
		return api.ErrInvalidConversationRequest
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Authorization", "Bearer "+guest.Admission.Token)
	client := privateConversationClient(http.DefaultClient)
	response, err := client.Do(req)
	if err != nil {
		return Fault(api.RoomMediaUnreachable)
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16*1024))
	switch response.StatusCode {
	case http.StatusOK:
		var room api.RoomView
		if decoder.Decode(&room) == nil && decoder.Decode(new(any)) == io.EOF && room.RoomRef == guest.Admission.Room.RoomRef &&
			room.Version == api.ConversationVersion && room.Generation.Validate() == nil {
			return nil
		}
	case http.StatusForbidden, http.StatusGone:
		var failure api.RoomErrorResponse
		if decoder.Decode(&failure) == nil && decoder.Decode(new(any)) == io.EOF {
			code := failure.Error.Code
			if (response.StatusCode == http.StatusForbidden && code == api.RoomPermissionDenied) ||
				(response.StatusCode == http.StatusGone && (code == api.RoomAccessEnded || code == api.RoomAccessExpired)) {
				return nil
			}
		}
	}
	return Fault(api.RoomMediaUnreachable)
}
