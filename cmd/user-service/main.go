package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"ticketflow/internal/auth"
	"ticketflow/internal/config"
	"ticketflow/internal/httpx"
	"ticketflow/internal/id"
	"ticketflow/internal/postgres"
)

type server struct {
	db     pgxQuerier
	secret string
	pepper string
}

type pgxQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func main() {
	ctx := context.Background()
	db, err := postgres.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	s := &server{
		db:     db,
		secret: config.Env("JWT_SECRET", "dev-secret-change-me"),
		pepper: config.Env("PASSWORD_PEPPER", "dev-pepper-change-me"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/users/register", s.register)
	mux.HandleFunc("/users/login", s.login)

	addr := ":" + config.Env("PORT", "8081")
	log.Printf("user-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req registerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || len(req.Password) < 6 {
		httpx.Error(w, http.StatusBadRequest, "email and password with at least 6 chars are required")
		return
	}

	passwordHash, err := auth.HashPassword(req.Password, s.pepper)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	userID := id.New("usr")
	err = s.db.QueryRow(r.Context(), `
		insert into users (id, email, name, password_hash)
		values ($1, $2, $3, $4)
		returning id
	`, userID, req.Email, req.Name, passwordHash).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_key") {
			httpx.Error(w, http.StatusConflict, "email already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not create user")
		return
	}
	token, err := auth.IssueToken(s.secret, auth.Claims{UserID: userID, Email: req.Email}, 24*time.Hour)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user_id": userID,
		"token":   token,
	})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodPost) {
		return
	}
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	var userID, passwordHash string
	err := s.db.QueryRow(r.Context(), `
		select id, password_hash from users where email = $1
	`, req.Email).Scan(&userID, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) || !auth.CheckPassword(passwordHash, req.Password, s.pepper) {
		httpx.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load user")
		return
	}
	token, err := auth.IssueToken(s.secret, auth.Claims{UserID: userID, Email: req.Email}, 24*time.Hour)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"token":   token,
	})
}
