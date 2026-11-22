package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The dock uses the workstation's existing SSH transport. This listener only
// exists inside the service container and serves the configured pod library.
func (a *apiV1) startDock() error {
	path := filepath.Join(a.s.cfg.DataDir, "recordings", "dock.sock")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: http.HandlerFunc(a.dockRequest), ReadHeaderTimeout: 5 * time.Second}
	a.wg.Add(2)
	go func() { defer a.wg.Done(); _ = srv.Serve(ln) }()
	go func() { defer a.wg.Done(); <-a.ctx.Done(); _ = srv.Close() }()
	return nil
}

func (a *apiV1) podSource(se *session) (string, error) {
	var source string
	err := a.get("pod-source", se.stateKey(), accountID(se.User), &source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return source, err
}

func (a *apiV1) followBrowser(w http.ResponseWriter, r *http.Request, se *session, device string) {
	if se.User != a.nightly.Username || a.nightly.ID == "" || a.libraryID(se) != a.nightly.Library || !uuidKey.MatchString(device) {
		verror(w, 403, "unavailable", "This dock is paired with another library.")
		return
	}
	if err := a.put("pod-source", se.stateKey(), accountID(se.User), device); err != nil {
		verror(w, 503, "storage_unavailable", "Could not save the listening source.")
		return
	}
	w.WriteHeader(204)
}

func (a *apiV1) dockRequest(w http.ResponseWriter, r *http.Request) {
	se, err := a.deviceIdentity(deviceSession{Username: a.nightly.Username})
	if err != nil || se.ServerURL == "" || a.libraryID(se) != a.nightly.Library {
		verror(w, 403, "unavailable", "Dock library unavailable.")
		return
	}
	se.ctx = r.Context()
	p := r.URL.Path
	id := r.URL.Query().Get("id")
	switch {
	case r.Method == "GET" && p == "/sources":
		// List only browser queues from this account and this library.
		prefix := se.stateKey() + ":browser:"
		rows, e := a.db.Query("SELECT id,body FROM records WHERE owner=? AND kind='queue' AND substr(id,1,?)=?", accountID(se.User), len([]rune(prefix)), prefix)
		if e != nil {
			verror(w, 503, "storage_unavailable", "Could not read queues.")
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var key string
			var raw []byte
			var q userState
			if rows.Scan(&key, &raw) != nil || a.open(raw, &q) != nil {
				verror(w, 503, "storage_unavailable", "Could not read queue.")
				return
			}
			var track struct{ Title, Artist string }
			if json.Unmarshal(q.Current, &track) == nil && track.Title != "" {
				out = append(out, map[string]any{"device": strings.TrimPrefix(key, prefix), "title": track.Title, "artist": track.Artist, "count": 1 + len(q.Queue)})
			}
		}
		if rows.Err() != nil {
			verror(w, 503, "storage_unavailable", "Could not list queues.")
			return
		}
		dataOut(w, 200, out)
	case r.Method == "PUT" && p == "/source":
		a.followBrowser(w, r, se, id)
	case r.Method == "GET" && p == "/queue":
		source, e := a.podSource(se)
		if e != nil {
			verror(w, 503, "storage_unavailable", "Could not read source.")
			return
		}
		var q userState
		if source != "" {
			key := se.stateKey() + ":browser:" + source
			e = a.get("pod-checkpoint", key, accountID(se.User), &q)
			if errors.Is(e, sql.ErrNoRows) {
				e = a.get("queue", key, accountID(se.User), &q)
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				verror(w, 503, "storage_unavailable", "Could not read queue.")
				return
			}
		}
		dataOut(w, 200, map[string]any{"source": source, "current": q.Current, "queue": q.Queue})
	case r.Method == "GET" && p == "/recent":
		result, e := a.s.call(r.Context(), se, "getAlbumList2", url.Values{"type": {"newest"}, "size": {"12"}})
		if e != nil {
			verror(w, 502, "upstream_unavailable", "Recent albums unavailable.")
			return
		}
		dataOut(w, 200, result["albumList2"])
	case r.Method == "GET" && p == "/audio" && id != "":
		a.s.downloadTrack(w, r, se)
	case r.Method == "GET" && p == "/cover" && id != "":
		a.podCover(w, r, se, mediaLease{Resource: id})
	case r.Method == "GET" && (p == "/api/v1/me" || p == "/api/v1/library/tracks" || p == "/api/v1/library/albums" || strings.HasPrefix(p, "/api/v1/library/playlists/")):
		a.dispatch(w, r, se)
	case r.Method == "PUT" && p == "/api/v1/portable/status":
		a.mu.Lock()
		defer a.mu.Unlock()
		a.portableStatus(w, r, se)
	case (r.Method == "POST" && p == "/api/v1/library/scrobbles") || (r.Method == "PUT" && strings.HasPrefix(p, "/api/v1/library/favorites/track/")):
		a.mutation(w, r, se, a.dispatch)
	default:
		http.NotFound(w, r)
	}
}

func (a *apiV1) checkpoint(w http.ResponseWriter, r *http.Request, se *session) {
	source, e := a.podSource(se)
	device := r.Header.Get("X-Weazl-Device")
	if e != nil || !uuidKey.MatchString(device) || device != source || se.User != a.nightly.Username || a.libraryID(se) != a.nightly.Library {
		verror(w, 403, "unpaired", "This browser is not the listening source.")
		return
	}
	var q userState
	if decode(r, &q) != nil || !validSavedTrack(q.Current, false) || len(q.Queue) > 1000 {
		verror(w, 422, "invalid", "Invalid playing queue.")
		return
	}
	for _, row := range q.Queue {
		if !validSavedTrack(row, false) {
			verror(w, 422, "invalid", "Invalid queue entry.")
			return
		}
	}
	q.Stations = nil
	if a.put("pod-checkpoint", se.stateKey()+":browser:"+device, accountID(se.User), q) != nil {
		verror(w, 503, "storage_unavailable", "Could not save listening position.")
		return
	}
	w.WriteHeader(204)
}
