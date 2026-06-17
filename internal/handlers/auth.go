package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/settings"
)

type authPage struct {
	User  any
	Error string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.Templates.RenderPage(w, "login.html", authPage{})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		s.Templates.RenderPage(w, "login.html", authPage{Error: "Username and password are required."})
		return
	}

	var id int64
	var hash string
	err := s.DB.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, username).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !auth.VerifyPassword(hash, password)) {
		s.Templates.RenderPage(w, "login.html", authPage{Error: "Invalid username or password."})
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Sessions.Create(w, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/decks", http.StatusSeeOther)
}

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	s.Templates.RenderPage(w, "register.html", authPage{})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if len(username) < 2 || len(password) < 6 {
		s.Templates.RenderPage(w, "register.html", authPage{Error: "Username must be 2+ chars; password 6+ chars."})
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	res, err := s.DB.Exec(`INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, hash)
	if err != nil {
		// Unique constraint violation
		s.Templates.RenderPage(w, "register.html", authPage{Error: "That username is taken."})
		return
	}
	id, _ := res.LastInsertId()
	if err := settings.InsertForUser(s.DB, id, s.DefaultSettings); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Sessions.Create(w, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/decks", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Destroy(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
