package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/junaidk/recall/internal/audio"
	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/fsrs"
	"github.com/junaidk/recall/internal/settings"
	"github.com/junaidk/recall/internal/web"
)

// Server bundles dependencies passed to every handler.
type Server struct {
	DB         *sql.DB
	Sessions   *auth.Store
	Templates  *web.Templates
	AudioCache *audio.Cache
	// DefaultSettings seed new users at registration and act as a fallback when
	// a user has no user_settings row. FSRS scheduling is otherwise per-user.
	DefaultSettings settings.Settings
}

func New(db *sql.DB, sessions *auth.Store, t *web.Templates, audioCache *audio.Cache, defaults settings.Settings) *Server {
	return &Server{DB: db, Sessions: sessions, Templates: t, AudioCache: audioCache, DefaultSettings: defaults}
}

// settingsFor loads a user's FSRS settings, falling back to DefaultSettings if
// the user has no row yet.
func (s *Server) settingsFor(userID int64) (settings.Settings, error) {
	set, err := settings.Load(s.DB, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.DefaultSettings, nil
	}
	if err != nil {
		return settings.Settings{}, err
	}
	return set, nil
}

// schedulerFor builds an FSRS scheduler from a user's settings. fsrs.New is
// cheap, so constructing one per request is fine.
func (s *Server) schedulerFor(userID int64) (*fsrs.Scheduler, error) {
	set, err := s.settingsFor(userID)
	if err != nil {
		return nil, err
	}
	return fsrs.New(fsrs.Options{
		RequestRetention: set.RequestRetention,
		MaximumInterval:  set.MaximumInterval,
		EnableFuzz:       set.EnableFuzz,
	}), nil
}

// Register mounts all routes on mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("GET /static/", web.StaticHandler())

	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /register", s.handleRegisterForm)
	mux.HandleFunc("POST /register", s.handleRegister)

	mux.Handle("POST /logout", s.Sessions.RequireUser(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /decks", s.Sessions.RequireUser(http.HandlerFunc(s.handleDecks)))
	mux.Handle("GET /settings", s.Sessions.RequireUser(http.HandlerFunc(s.handleSettingsForm)))
	mux.Handle("POST /settings", s.Sessions.RequireUser(http.HandlerFunc(s.handleSaveSettings)))
	mux.Handle("GET /decks/{id}/study", s.Sessions.RequireUser(http.HandlerFunc(s.handleStudyPage)))
	mux.Handle("GET /decks/{id}/stats", s.Sessions.RequireUser(http.HandlerFunc(s.handleDeckStats)))
	mux.Handle("GET /decks/{id}/cards", s.Sessions.RequireUser(http.HandlerFunc(s.handleBrowse)))
	mux.Handle("GET /review/{deckID}/next", s.Sessions.RequireUser(http.HandlerFunc(s.handleNextCard)))
	mux.Handle("GET /review/{deckID}/card/{cardID}", s.Sessions.RequireUser(http.HandlerFunc(s.handleRevealCard)))
	mux.Handle("POST /review/{cardID}/grade", s.Sessions.RequireUser(http.HandlerFunc(s.handleGrade)))
	mux.Handle("POST /review/log/{logID}/undo", s.Sessions.RequireUser(http.HandlerFunc(s.handleUndo)))

	mux.Handle("GET /media/audio/{wordID}", s.Sessions.RequireUser(http.HandlerFunc(s.handleAudio)))

	mux.Handle("GET /review/{cardID}/edit", s.Sessions.RequireUser(http.HandlerFunc(s.handleEditCardForm)))
	mux.Handle("POST /review/{cardID}/edit", s.Sessions.RequireUser(http.HandlerFunc(s.handleEditCard)))
	mux.Handle("GET /review/{cardID}/edit/candidates", s.Sessions.RequireUser(http.HandlerFunc(s.handleEditCandidates)))

	mux.Handle("POST /review/word/{wordID}/example/next", s.Sessions.RequireUser(http.HandlerFunc(s.handleNextExample)))
	mux.Handle("GET /review/word/{wordID}/example/choices", s.Sessions.RequireUser(http.HandlerFunc(s.handleExampleChoices)))
	mux.Handle("POST /review/word/{wordID}/example/set", s.Sessions.RequireUser(http.HandlerFunc(s.handleSetExample)))
	mux.Handle("GET /review/word/{wordID}/example/block", s.Sessions.RequireUser(http.HandlerFunc(s.handleExampleBlock)))
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.Sessions.MaybeUser(r) != nil {
		http.Redirect(w, r, "/decks", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
