package server

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
)

type podSend struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Tracks  []string `json:"tracks"`
	Receipt string   `json:"receipt"`
}

func (a *apiV1) pendingPodSends(se *session) ([]podSend, error) {
	var sends []podSend
	err := a.get("pod-sends", se.podKey(se.stateKey()), accountID(se.User), &sends)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sends, err
}

func (a *apiV1) savePodSends(w http.ResponseWriter, se *session, sends []podSend) {
	if a.put("pod-sends", se.podKey(se.stateKey()), accountID(se.User), sends) != nil {
		verror(w, 503, "storage_unavailable", "Could not save the pod sync queue.")
		return
	}
	w.WriteHeader(204)
}

// The v1 mutation handler serializes enqueues with dock confirmations.
func (a *apiV1) enqueuePod(w http.ResponseWriter, r *http.Request, se *session) {
	if se.PodID == "" && !a.legacyPodPaired(se) {
		verror(w, 403, "unavailable", "This dock is paired with another library.")
		return
	}
	var item podSend
	if decode(r, &item) != nil || !slices.Contains([]string{"album", "playlist", "track"}, item.Kind) || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" || len(item.Tracks) == 0 || slices.Contains(item.Tracks, "") || (item.Kind == "track" && (len(item.Tracks) != 1 || item.Tracks[0] != item.ID)) {
		verror(w, 422, "invalid", "Select a playable track, album or playlist.")
		return
	}
	item.Receipt = r.Header.Get("Idempotency-Key")
	pending, err := a.pendingPodSends(se)
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not read the pod sync queue.")
		return
	}
	for i, existing := range pending {
		if item.Kind == existing.Kind && item.ID == existing.ID {
			pending[i] = item
			a.savePodSends(w, se, pending)
			return
		}
	}
	a.savePodSends(w, se, append(pending, item))
}

func (a *apiV1) confirmPodSends(w http.ResponseWriter, r *http.Request, se *session) {
	var delivered []string
	if decode(r, &delivered) != nil {
		verror(w, 422, "invalid", "Invalid delivery receipts.")
		return
	}
	receipts := make(map[string]bool, len(delivered))
	for _, id := range delivered {
		receipts[id] = true
	}
	pending, err := a.pendingPodSends(se)
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not read the pod sync queue.")
		return
	}
	if se.PodID != "" {
		var additions []podSend
		for _, item := range pending {
			if receipts[item.Receipt] {
				additions = append(additions, item)
			}
		}
		if len(additions) > 0 {
			desired, err := a.desiredSends(se, additions)
			if err != nil || a.put("pod-delivered-content", se.podKey(se.stateKey()), accountID(se.User), desired) != nil {
				verror(w, 503, "storage", "Cannot retain delivered selections.")
				return
			}
		}
	}
	pending = slices.DeleteFunc(pending, func(item podSend) bool { return receipts[item.Receipt] })
	a.savePodSends(w, se, pending)
}
