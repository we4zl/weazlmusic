package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type namedPod struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Library  string `json:"libraryId"`
}

func (a *apiV1) userPods(se *session) ([]namedPod, error) {
	rows, err := a.list("named-pod", accountID(se.User))
	if err != nil {
		return nil, err
	}
	pods := []namedPod{}
	for _, raw := range rows {
		var pod namedPod
		if err = json.Unmarshal(raw, &pod); err != nil {
			return nil, err
		}
		if pod.Library == a.libraryID(se) {
			pods = append(pods, pod)
		}
	}
	return pods, nil
}

func (a *apiV1) portableDevices(w http.ResponseWriter, r *http.Request, se *session) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		pods, err := a.userPods(se)
		if err != nil {
			verror(w, 503, "storage", "Cannot list players.")
			return
		}
		dataOut(w, 200, pods)
		return
	}
	var in struct{ Name string }
	if decode(r, &in) != nil {
		verror(w, 422, "invalid", "Enter a player name.")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) == 0 || len(name) > 32 || strings.IndexFunc(name, func(c rune) bool { return c < 32 || c > 126 }) >= 0 {
		verror(w, 422, "invalid", "Use 1 to 32 printable ASCII characters.")
		return
	}
	token := ""
	id := se.PodID
	if r.Method == "POST" {
		token = randomID()
		id = digest(token)
	} else if id == "" {
		verror(w, 422, "invalid", "Select a player.")
		return
	}
	pod := namedPod{id, name, se.User, a.libraryID(se)}
	if se.ServerURL == "" || pod.Library == "" {
		verror(w, 409, "unavailable", "Connect a music library first.")
		return
	}
	if err := a.put("named-pod", id, accountID(se.User), pod); err != nil {
		verror(w, 503, "storage", "Cannot save player.")
		return
	}
	dataOut(w, 200, map[string]any{"device": pod, "pairingKey": token})
}

func (a *apiV1) selectPod(w http.ResponseWriter, r *http.Request, se *session) (*session, bool) {
	id := r.Header.Get("X-Weazl-Pod")
	if id == "" {
		return se, true
	}
	var pod namedPod
	err := a.get("named-pod", id, accountID(se.User), &pod)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		verror(w, 503, "storage", "Cannot read player.")
		return nil, false
	}
	if err != nil || pod.Library != a.libraryID(se) {
		verror(w, 404, "unknown_player", "Player unavailable.")
		return nil, false
	}
	copy := *se
	copy.PodID = id
	return &copy, true
}

func (se *session) podKey(key string) string {
	if se.PodID != "" {
		return key + ":pod:" + se.PodID
	}
	return key
}
