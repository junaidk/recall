package handlers

import (
	"net/http"
	"strconv"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
	"github.com/junaidk/recall/internal/settings"
)

type settingsPage struct {
	User             *models.User
	RequestRetention float64
	MaximumInterval  float64
	EnableFuzz       bool
	NewCardsPerDay   int
	Saved            bool
}

func (s *Server) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	set, err := s.settingsFor(u.ID)
	if err != nil {
		http.Error(w, "load settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPage(w, "settings.html", settingsPage{
		User:             u,
		RequestRetention: set.RequestRetention,
		MaximumInterval:  set.MaximumInterval,
		EnableFuzz:       set.EnableFuzz,
		NewCardsPerDay:   set.NewCardsPerDay,
		Saved:            r.URL.Query().Get("saved") == "1",
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Blank/invalid numbers fall back to the clamped defaults via Sanitize.
	retention, _ := strconv.ParseFloat(r.FormValue("request_retention"), 64)
	maxInterval, _ := strconv.ParseFloat(r.FormValue("maximum_interval"), 64)
	newPerDay, _ := strconv.Atoi(r.FormValue("new_cards_per_day"))

	set := settings.Settings{
		RequestRetention: retention,
		MaximumInterval:  maxInterval,
		EnableFuzz:       r.FormValue("enable_fuzz") != "",
		NewCardsPerDay:   newPerDay,
	}.Sanitize()

	if err := settings.Save(s.DB, u.ID, set); err != nil {
		http.Error(w, "save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
