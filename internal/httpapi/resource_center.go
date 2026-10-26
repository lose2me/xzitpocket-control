package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

// Resource-centre endpoints for the app: signed-in users list the courses that
// have content and fetch course resources, textbooks and temporary file URLs.

func (s *Server) userResourceCourses(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	// Fetch the revision first so a concurrent write cannot pair stale rows
	// with a newer version, which would make the client keep the stale cache.
	version, err := s.App.ResourceCenterVersion(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := s.App.ListResourceCourses(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "version": version})
}

func (s *Server) userCourseResources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	items, total, version, err := s.App.ListCourseResourcesForUser(r.Context(), r.URL.Query().Get("course_code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "version": version})
}

func (s *Server) userCourseTextbooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	items, total, version, err := s.App.ListCourseTextbooksForUser(r.Context(), r.URL.Query().Get("course_code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "version": version})
}

func (s *Server) userResourceAI(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	out, err := s.App.ResourceAIForUser(r.Context(), id, userURLTTL(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) userResourceURL(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	out, err := s.App.ResourceURL(r.Context(), id, userURLTTL(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.App.RecordResourceDownload(r.Context(), id, true)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) userTextbookURL(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	out, err := s.App.TextbookResourceURL(r.Context(), id, kind, userURLTTL(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "" || kind == "pdf" {
		s.App.RecordTextbookDownload(r.Context(), id, true)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) userUploadTextbooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authSession(w, r); !ok {
		return
	}
	items, err := s.App.ListTextbooksForUpload(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) userUploadStats(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.GetUploadStats(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func userURLTTL(r *http.Request) time.Duration {
	ttl := time.Duration(0)
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			ttl = time.Duration(seconds) * time.Second
		}
	}
	return ttl
}
