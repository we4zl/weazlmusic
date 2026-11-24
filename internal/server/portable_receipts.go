package server

import (
	"cmp"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// One revision identifies the ordered music; the bookmark can advance independently.
func queueRevisions(source string, q userState) (music, bookmark string) {
	var current map[string]json.RawMessage
	_ = json.Unmarshal(q.Current, &current)
	position := current["position"]
	delete(current, "position")
	b, _ := json.Marshal([]any{source, current, q.Queue})
	music = digest(string(b))
	b, _ = json.Marshal([]any{music, position})
	return music, digest(string(b))
}

// Include explicitly sent albums and playlists even after their transfer is
// acknowledged. Replacing an SD card must restore those selections as well.
func (a *apiV1) desiredSends(se *session, additions []podSend) ([]podSend, error) {
	var saved []podSend
	err := a.get("pod-delivered-content", se.podKey(se.stateKey()), accountID(se.User), &saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	byID := map[[2]string]podSend{}
	for _, s := range append(saved, additions...) {
		byID[[2]string{s.Kind, s.ID}] = s
	}
	out := make([]podSend, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y podSend) int {
		if x.Kind == y.Kind {
			return cmp.Compare(x.ID, y.ID)
		}
		return cmp.Compare(x.Kind, y.Kind)
	})
	return out, nil
}

func contentRevision(music string, desired []podSend) string {
	if len(desired) == 0 {
		return music
	}
	raw, _ := json.Marshal(desired)
	return digest(music + string(raw))
}

func cardHash(revision string) string {
	b, err := hex.DecodeString(revision)
	if err != nil || len(b) != 32 {
		return ""
	}
	return digest(string(b))
}

type receiptProof struct {
	Music, Bookmark, Art string
	At                   time.Time
}
type artRevision struct {
	Revision string
	At       time.Time
}

func (a *apiV1) receiptStatus(se *session, latest *packReport) bool {
	p, ok := a.podProofs.Load(se.PodID)
	if !ok {
		return false
	}
	proof := p.(receiptProof)
	if a.now().Sub(proof.At) > 25*time.Second || (latest != nil && latest.Phase == "failed" && !proof.At.After(latest.Seen)) {
		return false
	}
	art, ok := a.podArt.Load(se.PodID)
	if !ok || art.(artRevision).Revision != proof.Art || a.now().Sub(art.(artRevision).At) > 6*time.Minute {
		return false
	}
	source, q, err := a.dockBookmark(se)
	if err != nil {
		return false
	}
	m, b := queueRevisions(source, q)
	pending, err := a.pendingPodSends(se)
	if err != nil || len(pending) != 0 {
		return false
	}
	desired, err := a.desiredSends(se, nil)
	return err == nil && proof.Music == contentRevision(m, desired) && proof.Bookmark == b
}

func (a *apiV1) receiptQueue(w http.ResponseWriter, r *http.Request, se *session, source string, q userState, sends []podSend) {
	m, b := queueRevisions(source, q)
	desired, err := a.desiredSends(se, sends)
	if err != nil {
		verror(w, 503, "storage", "Cannot read selected music.")
		return
	}
	m = contentRevision(m, desired)
	query := r.URL.Query()
	out := map[string]any{"source": source, "current": q.Current, "musicRevision": m, "bookmarkRevision": b}
	art, ok := a.podArt.Load(se.PodID)
	out["artCheckDue"] = !ok || query.Get("art") != cardHash(art.(artRevision).Revision) || a.now().Sub(art.(artRevision).At) >= 5*time.Minute
	matched := len(sends) == 0 && query.Get("music") == cardHash(m)
	if matched && ok && query.Get("art") == cardHash(art.(artRevision).Revision) && query.Get("bookmark") == b && a.now().Sub(art.(artRevision).At) < 6*time.Minute {
		a.podProofs.Store(se.PodID, receiptProof{m, b, art.(artRevision).Revision, a.now()})
	} else {
		a.podProofs.Delete(se.PodID)
	}
	v, err := a.verification(se)
	if err != nil {
		verror(w, 503, "storage", "Cannot read verification.")
		return
	}
	if v.State == "queued" {
		out["verifyRequest"] = v.ID
	}
	out["musicMatch"] = matched
	if !matched {
		out["selections"] = desired
		out["queue"] = q.Queue
		out["sends"] = sends
	}
	dataOut(w, 200, out)
}

func (a *apiV1) receiptRadio(w http.ResponseWriter, r *http.Request, se *session) {
	raw, err := os.ReadFile(filepath.Join(a.s.cfg.DataDir, "recordings", "overnight-pod.json"))
	if os.IsNotExist(err) {
		dataOut(w, 200, nil)
		return
	}
	var ed struct {
		Account                   string `json:"accountId"`
		Library                   string `json:"libraryId"`
		Name, SessionID, Revision string
	}
	if err != nil || json.Unmarshal(raw, &ed) != nil || ed.SessionID == "" || ed.Revision == "" {
		verror(w, 503, "storage", "Cannot read published recording.")
		return
	}
	if ed.Account != accountID(se.User) || ed.Library != a.libraryID(se) {
		dataOut(w, 200, nil)
		return
	}
	revision := digest(ed.SessionID + ":" + ed.Revision)
	matched := r.URL.Query().Get("radio") == cardHash(revision)
	out := map[string]any{"revision": revision, "matched": matched, "name": ed.Name, "accountId": ed.Account, "libraryId": ed.Library}
	if !matched {
		out["manifest"] = json.RawMessage(raw)
	}
	dataOut(w, 200, out)
}

type cardVerification struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

func (a *apiV1) verification(se *session) (v cardVerification, err error) {
	err = a.get("pod-verification", se.PodID, accountID(se.User), &v)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}
func (a *apiV1) verifyRequest(w http.ResponseWriter, r *http.Request, se *session) {
	if se.PodID == "" {
		verror(w, 422, "unknown_player", "Select a named player.")
		return
	}
	a.podVerifyMu.Lock()
	defer a.podVerifyMu.Unlock()
	v, e := a.verification(se)
	if e != nil {
		verror(w, 503, "storage", "Cannot read verification.")
		return
	}
	if r.Method == "PUT" {
		var result struct{ ID, Error string }
		if decode(r, &result) != nil || len(result.Error) > 1024 || result.ID != v.ID || v.State != "queued" {
			verror(w, 409, "changed", "Verification changed.")
			return
		}
		v.State = "verified"
		v.Message = result.Error
		if result.Error != "" {
			v.State = "failed"
		}
	} else if v.State != "queued" {
		v = cardVerification{ID: randomID(), State: "queued"}
	}
	if e = a.put("pod-verification", se.PodID, accountID(se.User), v); e != nil {
		verror(w, 503, "storage", "Cannot save verification.")
		return
	}
	dataOut(w, 200, v)
}
