package shareguest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
)

type fixtureGuestIngress struct{}

func (fixtureGuestIngress) Start(context.Context) (sharing.IngressBinding, error) {
	return sharing.IngressBinding{Origin: "https://sharing.example.test", Audience: api.SharePublicAudience}, nil
}
func (fixtureGuestIngress) Check(context.Context) error { return nil }
func (fixtureGuestIngress) Stop(context.Context) error  { return nil }

type fixtureGuestMedia struct{ sharing.Media } // Admissions never open media.

func TestSharingFirstVisitsKeepIndependentBrowserSessions(t *testing.T) {
	tokens, err := sharing.NewInvitationTokens(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := sharing.NewService(sharing.Options{
		Source: func(context.Context, string) (string, error) { return "fixture-source", nil },
		Tokens: tokens, Ingress: fixtureGuestIngress{}, Media: fixtureGuestMedia{},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := sharing.NewCommands(service)
	defer commands.Close()
	handler, err := New(fstest.MapFS{api.ShareAssetEntry: {Data: []byte("fixture")}}, commands)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(sharing.WithOperator(t.Context()), sharing.CreateShare{
		CreateShareRequest: api.CreateShareRequest{CameraID: "camera", Options: api.ShareOptions{ViewerLimit: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(created.Link.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base := link.Scheme + "://" + link.Host + api.ShareAPIPrefix + created.Share.ShareID
	body, err := json.Marshal(api.RedeemShareRequest{Invitation: link.Fragment})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, csrf string, body io.Reader, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(api.ShareCSRFHeader, csrf)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, req)
		return reply
	}

	// Both requests leave the same empty browser cookie jar before either
	// reply is applied. Store the two responses as the browser would.
	var replies [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	for i := range replies {
		wg.Go(func() { replies[i] = request("/redeem", "", bytes.NewReader(body), nil) })
	}
	wg.Wait()
	var tabs [2]api.RedeemShareResult
	for i, reply := range replies {
		cookies := reply.Result().Cookies()
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &tabs[i]) != nil ||
			len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure ||
			cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" || cookies[0].Domain != "" {
			t.Fatal("invalid redemption result")
		}
		jar.SetCookies(link, cookies)
	}
	cookies := jar.Cookies(link)
	if len(cookies) != 2 {
		t.Fatalf("the browser kept %d cookies for two tabs", len(cookies))
	}
	for _, tab := range tabs {
		if reply := request("/viewers", tab.CSRFToken, strings.NewReader("{}"), cookies); reply.Code != http.StatusCreated {
			t.Fatalf("valid tab was refused: %d", reply.Code)
		}
	}
	first := cookies[0]
	if first.Name == api.ShareGuestCookiePrefix+tabs[1].CSRFToken {
		first = cookies[1]
	}
	if reply := request("/viewers", tabs[1].CSRFToken, strings.NewReader("{}"), []*http.Cookie{first}); reply.Code != http.StatusForbidden {
		t.Fatalf("wrong tab's cookie passed authorization: %d", reply.Code)
	}
}
