package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/junaidk/recall/internal/models"
)

const (
	cookieName     = "anki_session"
	sessionTTL     = 30 * 24 * time.Hour
	tokenByteLen   = 32
)

type ctxKey int

const userCtxKey ctxKey = 0

type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) Create(w http.ResponseWriter, userID int64) error {
	b := make([]byte, tokenByteLen)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	exp := time.Now().Add(sessionTTL)
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, exp,
	); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Store) Destroy(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err == nil {
		s.DB.Exec(`DELETE FROM sessions WHERE token = ?`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    cookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
}

func (s *Store) lookup(r *http.Request) (*models.User, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, errors.New("no session cookie")
	}
	row := s.DB.QueryRow(
		`SELECT u.id, u.username, u.password_hash, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > CURRENT_TIMESTAMP`,
		c.Value,
	)
	var u models.User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// RequireUser is middleware: redirects to /login if no session.
func (s *Store) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.lookup(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CurrentUser pulls the user from context, set by RequireUser.
func CurrentUser(r *http.Request) *models.User {
	u, _ := r.Context().Value(userCtxKey).(*models.User)
	return u
}

// MaybeUser returns the user if logged in, nil otherwise. No redirect.
func (s *Store) MaybeUser(r *http.Request) *models.User {
	u, _ := s.lookup(r)
	return u
}
