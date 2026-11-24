package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestPodSendDelivery(t *testing.T) {
	h := setup(t)
	h.login("alice")
	h.login("bob")
	alice, bob := h.native("alice", "source"), h.native("bob", "other")
	a := h.app.Config.Handler.(*Server).v1
	pair := recordingSchedule{ID: "dock", Username: "alice", Library: alice.Library}
	a.nightly = pair
	item := podSend{Kind: "playlist", ID: "mix", Name: "Mix", Tracks: []string{"one", "two", "one"}}
	if code, _, _ := h.vrequest("POST", "portable/send", item, bob.Access, nil); code != 403 {
		t.Fatal(code)
	}
	send := func() {
		if code, b, _ := h.vrequest("POST", "portable/send", item, alice.Access, nil); code != 204 {
			t.Fatal(code, string(b))
		}
	}
	read := func() []podSend {
		w := httptest.NewRecorder()
		a.dockRequest(w, httptest.NewRequest("GET", "/queue", nil))
		return decodeData[struct{ Sends []podSend }](t, w.Body.Bytes()).Sends
	}
	confirm := func(key string) {
		b, _ := json.Marshal([]string{key})
		w := httptest.NewRecorder()
		a.dockRequest(w, httptest.NewRequest("POST", "/delivered", bytes.NewReader(b)))
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	send()
	h.restart()
	a = h.app.Config.Handler.(*Server).v1
	a.nightly = pair
	first := read()
	if len(first) != 1 || len(first[0].Tracks) != 3 {
		t.Fatal(first)
	}
	send()
	confirm(first[0].Receipt)
	second := read()
	if len(second) != 1 || second[0].Receipt == first[0].Receipt {
		t.Fatal(second)
	}
	confirm(second[0].Receipt)
	if left := read(); len(left) != 0 {
		t.Fatal(left)
	}
}
