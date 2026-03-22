package tether

import (
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

// defaultMaxUploadSize is used when UploadConfig.MaxSize is zero.
const defaultMaxUploadSize = 10 << 20 // 10 MB

// multipartMemory is the threshold passed to ParseMultipartForm. Data
// beyond this is spilled to temporary files on disk. This is
// deliberately much smaller than MaxSize so large uploads don't
// consume unbounded RAM.
const multipartMemory = 32 << 20 // 32 MB

// UploadConfig enables file upload support. When set on [StatefulConfig], the
// handler accepts multipart POST requests from the upload extension JS
// and delivers each file to the Handle callback.
type UploadConfig[S any] struct {
	// Handle is called when a file upload completes. The callback
	// runs in its own goroutine so it is safe to perform I/O (e.g.
	// writing to disk or S3). Use [Session.Update] to re-render
	// after processing the file.
	Handle func(session *StatefulSession[S], upload Upload) error

	// MaxSize is the maximum upload size in bytes. Requests
	// exceeding this limit are rejected with 413. Default 10 MB.
	MaxSize int64

	// MaxMemory is the maximum number of bytes held in RAM during
	// multipart parsing. Data beyond this threshold is spilled to
	// temporary files on disk. This is deliberately separate from
	// MaxSize so large uploads don't consume unbounded RAM. Zero
	// defaults to 32 MB.
	MaxMemory int64

	// Accept is a list of allowed MIME type patterns (e.g.
	// "image/*", "application/pdf"). When empty, all types are
	// accepted. The server validates the file's Content-Type
	// header after the upload arrives.
	Accept []string
}

// Upload represents a single uploaded file delivered to the
// [UploadConfig].Handle callback.
type Upload struct {
	// Action is the value from bind.Upload - the application-defined
	// name that identifies this upload (e.g. "avatar", "document").
	Action string

	// Name is the original filename as reported by the browser.
	Name string

	// Size is the file size in bytes.
	Size int64

	// ContentType is the MIME type from the part's Content-Type header.
	ContentType string

	header *multipart.FileHeader
}

// Open returns the uploaded file for reading. The caller must close
// the returned file when done.
func (u Upload) Open() (multipart.File, error) {
	return u.header.Open()
}

// handleUpload processes a multipart file upload POST. It validates
// the session, parses the multipart body, checks MIME types against
// the accept list, and calls the UploadConfig.Handle callback.
func (h *Handler[S]) handleUpload(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Upload == nil || h.cfg.Upload.Handle == nil {
		http.Error(w, "uploads not configured", http.StatusNotFound)
		return
	}
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	id := r.Header.Get("X-Tether-Session")
	if id == "" {
		http.Error(w, "missing X-Tether-Session header", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	sess, ok := h.active[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	maxSize := h.cfg.Upload.MaxSize
	if maxSize == 0 {
		maxSize = defaultMaxUploadSize
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	memLimit := h.cfg.Upload.MaxMemory
	if memLimit == 0 {
		memLimit = multipartMemory
	}
	if err := r.ParseMultipartForm(memLimit); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	action := r.Header.Get("X-Tether-Upload")

	// Validate MIME types and collect uploads before spawning the
	// background goroutine. On validation failure, clean up
	// immediately and return an error.
	var uploads []Upload
	for _, headers := range r.MultipartForm.File {
		for _, fh := range headers {
			ct := fh.Header.Get("Content-Type")
			if !mimeAllowed(ct, h.cfg.Upload.Accept) {
				h.Diagnostics.Publish(Diagnostic{
					Kind:      UploadRejected,
					SessionID: id,
					Err:       fmt.Errorf("content type %q not in %v", ct, h.cfg.Upload.Accept),
					Detail:    filepath.Base(fh.Filename),
				})
				r.MultipartForm.RemoveAll()
				http.Error(w, "file type not allowed", http.StatusUnsupportedMediaType)
				return
			}
			uploads = append(uploads, Upload{
				Action:      action,
				Name:        filepath.Base(fh.Filename),
				Size:        fh.Size,
				ContentType: ct,
				header:      fh,
			})
		}
	}

	// Hand ownership of the multipart form to a single goroutine
	// that processes all files. Temp files are cleaned up after
	// every callback has returned - not before. The WaitGroup
	// ensures Shutdown waits for active uploads to finish.
	form := r.MultipartForm
	handler := h.cfg.Upload.Handle
	sessionID := sess.id
	diag := h.Diagnostics
	h.uploadWG.Go(func() {
		defer form.RemoveAll()
		defer func() {
			if r := recover(); r != nil {
				err := panicErr(r)
				slog.Error("panic in upload handler", "session", sessionID, "panic", r)
				diag.Publish(Diagnostic{
					Kind:      UploadError,
					SessionID: sessionID,
					Err:       err,
					Detail:    "panic",
				})
			}
		}()
		for _, u := range uploads {
			if err := handler(sess, u); err != nil {
				diag.Publish(Diagnostic{
					Kind:      UploadError,
					SessionID: sessionID,
					Err:       err,
					Detail:    u.Action,
				})
			}
		}
	})

	w.WriteHeader(http.StatusNoContent)
}

// mimeAllowed checks whether contentType matches any pattern in the
// accept list. Patterns support wildcard subtypes (e.g. "image/*").
// An empty accept list allows all types.
func mimeAllowed(contentType string, accept []string) bool {
	if len(accept) == 0 {
		return true
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "" {
		return false
	}
	for _, pattern := range accept {
		if pattern == mediaType {
			return true
		}
		// Wildcard subtype: "image/*" matches "image/png"
		if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
			if strings.HasPrefix(mediaType, prefix+"/") {
				return true
			}
		}
	}
	return false
}
