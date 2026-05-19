package handlers

import (
	"database/sql"
	"net/http"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/web"
)

// Server bundles dependencies passed to every handler.
type Server struct {
	DB        *sql.DB
	Sessions  *auth.Store
	Templates *web.Templates
}

func New(db *sql.DB, sessions *auth.Store, t *web.Templates) *Server {
	return &Server{DB: db, Sessions: sessions, Templates: t}
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
	mux.Handle("GET /decks/{id}/study", s.Sessions.RequireUser(http.HandlerFunc(s.handleStudyPage)))
	mux.Handle("GET /review/{deckID}/next", s.Sessions.RequireUser(http.HandlerFunc(s.handleNextCard)))
	mux.Handle("GET /review/{deckID}/card/{cardID}", s.Sessions.RequireUser(http.HandlerFunc(s.handleRevealCard)))
	mux.Handle("POST /review/{cardID}/grade", s.Sessions.RequireUser(http.HandlerFunc(s.handleGrade)))
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
