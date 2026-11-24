package server

import (
	"database/sql"
	"errors"
	"net/http"
)

// Polling dock status only reads the small ownership record.
type podListening struct {
	Source     string
	Stopped    bool
	Writer     string
	Generation uint64
}

type podBookmark struct {
	Source     string
	Generation uint64
	Sequence   uint64
	Queue      userState
}

type podWriterChange struct {
	Action     string  `json:"action"`
	Writer     string  `json:"writer"`
	Generation *uint64 `json:"generation"`
}

func (a *apiV1) podPaired(se *session) bool {
	if se.PodID != "" || a.legacyPodPaired(se) {
		return true
	}
	pods, err := a.userPods(se)
	return err == nil && len(pods) > 0
}

func (a *apiV1) legacyPodPaired(se *session) bool {
	return a.nightly.ID != "" && se.User == a.nightly.Username && a.libraryID(se) == a.nightly.Library
}

func (a *apiV1) podListening(se *session) (podListening, error) {
	var listening podListening
	err := a.get("pod-listening", se.stateKey(), accountID(se.User), &listening)
	if !errors.Is(err, sql.ErrNoRows) {
		return listening, err
	}

	// A pre-upgrade source has a bookmark but no authorized tab.
	err = a.get("pod-source", se.stateKey(), accountID(se.User), &listening.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return listening, nil
	}
	return listening, err
}

func (a *apiV1) podBookmark(se *session) (podBookmark, error) {
	var b podBookmark
	key, owner := se.stateKey(), accountID(se.User)
	err := a.get("pod-bookmark", key, owner, &b)
	if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	var source string
	err = a.get("pod-source", key, owner, &source)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && source == "") {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	b.Source = source
	key += ":browser:" + source
	err = a.get("pod-checkpoint", key, owner, &b.Queue)
	if errors.Is(err, sql.ErrNoRows) {
		err = a.get("queue", key, owner, &b.Queue)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	b.Queue.Stations = nil
	return b, err
}

func (l podListening) follows(device string) bool {
	return l.Source != "" && l.Source == device && !l.Stopped
}

func (l podListening) writerStatus(device, writer string) map[string]any {
	follows := l.follows(device)
	return map[string]any{
		"following": follows, "followingStopped": l.Stopped,
		"writerActive":     follows && writer != "" && l.Writer == writer,
		"writerGeneration": l.Generation,
	}
}

func (a *apiV1) changePodWriter(w http.ResponseWriter, r *http.Request, se *session) {
	device := r.Header.Get("X-Weazl-Device")
	if !a.podPaired(se) || !uuidKey.MatchString(device) {
		verror(w, 403, "unpaired", "This dock is paired with another library.")
		return
	}
	var change podWriterChange
	if decode(r, &change) != nil || change.Generation == nil || !uuidKey.MatchString(change.Writer) ||
		(change.Action != "follow" && change.Action != "claim" && change.Action != "stop") {
		verror(w, 422, "invalid", "Choose how this tab should follow playback.")
		return
	}

	a.podWriterMu.Lock()
	defer a.podWriterMu.Unlock()
	listening, err := a.podListening(se)
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not read the listening source.")
		return
	}
	if listening.Generation != *change.Generation || (change.Action != "follow" && !listening.follows(device)) {
		verror(w, 409, "pod_writer_changed", "The listening source changed. Refresh the pod page and try again.")
		return
	}
	if listening.Generation == 0 {
		if err := a.freezeLegacyBookmark(se); err != nil {
			verror(w, 503, "storage_unavailable", "Could not retain the existing bookmark.")
			return
		}
	}
	listening.Source = device
	listening.Stopped = change.Action == "stop"
	listening.Writer = change.Writer
	if listening.Stopped {
		listening.Writer = ""
	}
	listening.Generation++
	if err := a.put("pod-listening", se.stateKey(), accountID(se.User), listening); err != nil {
		verror(w, 503, "storage_unavailable", "Could not save the listening source.")
		return
	}
	dataOut(w, 200, listening.writerStatus(device, change.Writer))
}

// Under podWriterMu, capture the legacy browser queue once before a writer or
// stop makes the dock independent of general browser state saves.
func (a *apiV1) freezeLegacyBookmark(se *session) error {
	b, err := a.podBookmark(se)
	if err != nil {
		return err
	}
	return a.put("pod-bookmark", se.stateKey(), accountID(se.User), b)
}

func (a *apiV1) dockBookmark(se *session) (string, userState, error) {
	a.podWriterMu.Lock()
	defer a.podWriterMu.Unlock()
	listening, err := a.podListening(se)
	if err != nil {
		return "", userState{}, err
	}
	b, err := a.podBookmark(se)
	if b.Source != "" {
		return b.Source, b.Queue, err
	}
	return listening.Source, b.Queue, err
}

// Selecting a browser on the workstation does not authorize any of its tabs.
func (a *apiV1) followDockBrowser(w http.ResponseWriter, se *session, device string) {
	if !a.podPaired(se) || !uuidKey.MatchString(device) {
		verror(w, 403, "unpaired", "This dock is paired with another library.")
		return
	}
	a.podWriterMu.Lock()
	defer a.podWriterMu.Unlock()
	listening, err := a.podListening(se)
	if err == nil && !listening.follows(device) {
		if listening.Generation == 0 {
			err = a.freezeLegacyBookmark(se)
		}
		listening.Source, listening.Writer, listening.Stopped = device, "", false
		listening.Generation++
		if err == nil {
			err = a.put("pod-listening", se.stateKey(), accountID(se.User), listening)
		}
	}
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not save the listening source.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) checkpoint(w http.ResponseWriter, r *http.Request, se *session) {
	if !a.podPaired(se) {
		verror(w, 403, "unpaired", "This dock is paired with another library.")
		return
	}
	var in struct {
		userState
		Writer     string `json:"writer"`
		Generation uint64 `json:"generation"`
		Sequence   uint64 `json:"sequence"`
	}
	if decode(r, &in) != nil || !uuidKey.MatchString(in.Writer) || in.Generation == 0 || in.Sequence == 0 ||
		!validSavedTrack(in.Current, false) || len(in.Queue) > 1000 {
		verror(w, 422, "invalid", "Invalid listening checkpoint.")
		return
	}
	for _, row := range in.Queue {
		if !validSavedTrack(row, false) {
			verror(w, 422, "invalid", "Invalid queue entry.")
			return
		}
	}

	a.podWriterMu.Lock()
	defer a.podWriterMu.Unlock()
	listening, err := a.podListening(se)
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not read the listening source.")
		return
	}
	if !listening.follows(r.Header.Get("X-Weazl-Device")) || listening.Writer != in.Writer ||
		listening.Generation != in.Generation {
		verror(w, 409, "pod_writer_changed", "This tab no longer owns the listening bookmark.")
		return
	}
	current, err := a.podBookmark(se)
	if err != nil {
		verror(w, 503, "storage_unavailable", "Could not read the listening bookmark.")
		return
	}
	if current.Generation == in.Generation && in.Sequence <= current.Sequence {
		verror(w, 409, "pod_checkpoint_old", "A newer listening position has already been saved.")
		return
	}
	in.Stations = nil
	next := podBookmark{Source: listening.Source, Generation: in.Generation, Sequence: in.Sequence, Queue: in.userState}
	if a.put("pod-bookmark", se.stateKey(), accountID(se.User), next) != nil {
		verror(w, 503, "storage_unavailable", "Could not save listening position.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
