package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

// handleAudio serves pronunciation MP3s. On cache hit, serves the local file
// (with Range support via http.ServeFile). On cache miss, kicks off a
// background download and 302-redirects the client to the upstream DWDS URL
// so playback isn't delayed.
func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	wordID, err := strconv.ParseInt(r.PathValue("wordID"), 10, 64)
	if err != nil {
		http.Error(w, "bad word id", http.StatusBadRequest)
		return
	}

	if s.AudioCache.Has(wordID) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, r, s.AudioCache.Path(wordID))
		return
	}

	var url sql.NullString
	err = s.DB.QueryRow(`SELECT audio_url FROM words WHERE id = ?`, wordID).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	if !url.Valid || url.String == "" {
		http.NotFound(w, r)
		return
	}

	s.AudioCache.FetchAsync(wordID, url.String)
	http.Redirect(w, r, url.String, http.StatusFound)
}
