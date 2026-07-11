package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// webhookHandler is POST /api/webhook (T7.4): HMAC-verified push and
// repository events drive targeted fetches / membership re-syncs without
// waiting for a poll. Registered outside huma: signature auth replaces the
// bearer token, and HMAC needs the raw body bytes. Gitea emits
// GitHub-compatible X-GitHub-Event / X-Hub-Signature-256 headers, so its
// webhooks work through the same endpoint (verified live, T7.4).
func webhookHandler(opts Options) http.HandlerFunc {
	respond := func(w http.ResponseWriter, code int, status string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
	// GitHub caps webhook payloads at 25 MB; a silently truncated body would
	// HMAC-mismatch and report a misleading "bad signature", so over-limit
	// bodies are rejected distinctly instead.
	const maxBody = 25 << 20
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil {
			respond(w, http.StatusBadRequest, "unreadable body")
			return
		}
		if len(body) > maxBody {
			respond(w, http.StatusRequestEntityTooLarge, "payload exceeds 25MB")
			return
		}
		if !validSignature(body, r.Header.Get("X-Hub-Signature-256"), opts.WebhookSecret) {
			respond(w, http.StatusUnauthorized, "bad signature")
			return
		}

		switch event := r.Header.Get("X-GitHub-Event"); event {
		case "ping":
			respond(w, http.StatusOK, "pong")
		case "push":
			var p struct {
				Repository struct {
					CloneURL string `json:"clone_url"`
				} `json:"repository"`
			}
			if err := json.Unmarshal(body, &p); err != nil || p.Repository.CloneURL == "" {
				respond(w, http.StatusBadRequest, "no repository.clone_url in payload")
				return
			}
			// clone_url → canonical name keeps this host-agnostic
			name, err := phebssync.RepoName(p.Repository.CloneURL)
			if err != nil {
				respond(w, http.StatusBadRequest, "unrecognized clone_url")
				return
			}
			repo, err := opts.Store.GetRepo(r.Context(), name)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					respond(w, http.StatusAccepted, "unknown repo "+name+" ignored")
					return
				}
				// a store failure is not "unknown repo": let the sender
				// retry rather than lose the push behind a 2xx
				respond(w, http.StatusInternalServerError, "repo lookup failed")
				return
			}
			if repo.Deleting {
				respond(w, http.StatusAccepted, "unknown repo "+name+" ignored")
				return
			}
			// pending-only dedup: a push landing while a fetch is already
			// running must queue another round, or its commits wait for the
			// resync cadence (lost-wakeup)
			if err := store.EnqueueUnlessPending(r.Context(), opts.Store, store.JobFetch, name); err != nil {
				respond(w, http.StatusInternalServerError, "enqueue failed")
				return
			}
			respond(w, http.StatusAccepted, "fetch enqueued for "+name)
		case "repository", "installation_repositories":
			// membership may have changed: re-list the code-host connections
			failed := false
			for _, conn := range opts.ResyncConnections {
				if err := store.EnqueueUnlessInFlight(r.Context(), opts.Store, store.JobSync, conn); err != nil {
					log.Printf("webhook: enqueue sync %s: %v", conn, err)
					failed = true
				}
			}
			if failed {
				respond(w, http.StatusInternalServerError, "connection re-sync enqueue failed")
				return
			}
			respond(w, http.StatusAccepted, "connection re-sync enqueued")
		default:
			respond(w, http.StatusAccepted, "event "+event+" ignored")
		}
	}
}

// validSignature checks GitHub's X-Hub-Signature-256 scheme:
// "sha256=" + hex HMAC-SHA256 of the raw body, compared in constant time.
func validSignature(body []byte, header, secret string) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}
