package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestNamedPodsAreOwnedAndIndependent(t *testing.T) {
	h := setup(t)
	h.login("alice")
	h.login("bob")
	alice, bob := h.native("alice", "web"), h.native("bob", "web")
	type registration struct {
		Device     namedPod
		PairingKey string
	}
	register := func(name string) registration {
		code, body, _ := h.vrequest("POST", "portable/devices", map[string]string{"name": name}, alice.Access, nil)
		if code != 200 {
			t.Fatal(code, string(body))
		}
		v := decodeData[registration](t, body)
		if v.Device.ID != digest(v.PairingKey) || v.Device.Name != name || v.Device.Username != "alice" {
			t.Fatal("registration")
		}
		return v
	}
	left, right := register("Pocket"), register("Car")
	header := map[string]string{"X-Weazl-Pod": left.Device.ID}
	if code, _, _ := h.vrequest("PATCH", "portable/devices", map[string]string{"name": "Travel"}, alice.Access, header); code != 200 {
		t.Fatal(code)
	}
	if code, _, _ := h.vrequest("PATCH", "portable/devices", map[string]string{"name": "Other"}, bob.Access, header); code != 404 {
		t.Fatal(code)
	}
	if code, body, _ := h.vrequest("GET", "portable/devices", nil, bob.Access, nil); code != 200 || len(decodeData[[]namedPod](t, body)) != 0 {
		t.Fatal("foreign devices")
	}
	item := podSend{Kind: "track", ID: "a", Name: "A", Tracks: []string{"a"}}
	for _, pod := range []registration{left, right} {
		if code, _, _ := h.vrequest("POST", "portable/send", item, alice.Access, map[string]string{"X-Weazl-Pod": pod.Device.ID}); code != 204 {
			t.Fatal(code)
		}
	}
	dock := func(method, path, key string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-Weazl-Pod-Key", key)
		w := httptest.NewRecorder()
		h.app.Config.Handler.(*Server).v1.dockRequest(w, r)
		return w
	}
	h.restart()
	if dock("GET", "/identity", left.Device.ID, nil).Code != 403 {
		t.Fatal("public ID accepted as credential")
	}
	if got := decodeData[namedPod](t, dock("GET", "/identity", left.PairingKey, nil).Body.Bytes()); got.Name != "Travel" {
		t.Fatal(got.Name)
	}
	type queue struct{ Sends []podSend }
	before := decodeData[queue](t, dock("GET", "/queue", left.PairingKey, nil).Body.Bytes())
	if len(before.Sends) != 1 {
		t.Fatal(before)
	}
	if dock("POST", "/delivered", left.PairingKey, []string{before.Sends[0].Receipt}).Code != 204 {
		t.Fatal("ack")
	}
	if len(decodeData[queue](t, dock("GET", "/queue", left.PairingKey, nil).Body.Bytes()).Sends) != 0 || len(decodeData[queue](t, dock("GET", "/queue", right.PairingKey, nil).Body.Bytes()).Sends) != 1 {
		t.Fatal("shared receipts")
	}
}
