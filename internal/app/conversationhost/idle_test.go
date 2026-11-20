package conversationhost

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/goliatone/go-cam-stream/config"
)

func TestListenerDrainsAfterLastRoom(t *testing.T) {
	runtime, err := Start(t.Context(), config.Conversations{Loopback: true}, nil, fstest.MapFS{
		"index.html": {Data: []byte("<html><head></head><body></body></html>")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	newRoom := func() string {
		t.Helper()
		room, err := runtime.Create(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = runtime.End(t.Context(), room.Admission.Room.RoomRef); err != nil {
			t.Fatal(err)
		}
		return room.PageURL
	}
	page := newRoom()
	now := time.Now()
	runtime.reapIdle(now, listenerGrace)
	res, err := (&http.Client{Timeout: time.Second}).Get(page)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || !strings.Contains(string(body), `name="camstream-room-state" content="ended"`) {
		t.Fatalf("old invitation did not render its terminal page: %s", body)
	}
	newRoom() // Restart in grace keeps the same port and resets the deadline.
	next := now.Add(listenerGrace / 2)
	runtime.reapIdle(next, listenerGrace)
	runtime.reapIdle(now.Add(listenerGrace), listenerGrace)
	if runtime.Closed() {
		t.Fatal("the old grace deadline closed the restarted listener")
	}
	runtime.reapIdle(next.Add(listenerGrace), listenerGrace)
	if !runtime.Closed() {
		t.Fatal("the new grace deadline did not close the listener")
	}
	origin, _ := url.Parse(page)
	listener, err := net.Listen("tcp", origin.Host)
	if err != nil {
		t.Fatalf("grace did not release the port: %v", err)
	}
	_ = listener.Close()
}
