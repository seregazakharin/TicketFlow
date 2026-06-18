package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketflow/internal/config"
	"ticketflow/internal/httpx"
	"ticketflow/internal/id"
	"ticketflow/internal/postgres"
	"ticketflow/internal/redisx"
)

const eventsCacheKey = "events:list:v1"

type server struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

type event struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	StartsAt   time.Time `json:"starts_at"`
	PriceCents int       `json:"price_cents"`
	Capacity   int       `json:"capacity"`
	Available  int       `json:"available"`
	CreatedAt  time.Time `json:"created_at"`
}

func main() {
	ctx := context.Background()
	db, err := postgres.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	rdb := redisx.Connect()
	defer rdb.Close()

	s := &server{db: db, redis: rdb}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/events", s.events)
	mux.HandleFunc("/events/", s.eventAction)

	addr := ":" + config.Env("PORT", "8082")
	log.Printf("event-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type createEventRequest struct {
	Title      string `json:"title"`
	StartsAt   string `json:"starts_at"`
	PriceCents int    `json:"price_cents"`
	Capacity   int    `json:"capacity"`
}

type reserveRequest struct {
	OrderID  string `json:"order_id"`
	UserID   string `json:"user_id"`
	Quantity int    `json:"quantity"`
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listEvents(w, r)
	case http.MethodPost:
		s.createEvent(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) listEvents(w http.ResponseWriter, r *http.Request) {
	if cached, err := s.redis.Get(r.Context(), eventsCacheKey).Bytes(); err == nil {
		httpx.WriteRawJSON(w, http.StatusOK, cached)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select id, title, starts_at, price_cents, capacity, available, created_at
		from events
		order by starts_at asc, created_at desc
	`)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list events")
		return
	}
	defer rows.Close()

	events := make([]event, 0)
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.Title, &e.StartsAt, &e.PriceCents, &e.Capacity, &e.Available, &e.CreatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "could not scan event")
			return
		}
		events = append(events, e)
	}
	body, _ := json.Marshal(map[string]any{"events": events})
	_ = s.redis.Set(r.Context(), eventsCacheKey, body, 30*time.Second).Err()
	httpx.WriteRawJSON(w, http.StatusOK, body)
}

func (s *server) createEvent(w http.ResponseWriter, r *http.Request) {
	var req createEventRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || req.Capacity <= 0 || req.PriceCents < 0 {
		httpx.Error(w, http.StatusBadRequest, "title, capacity and price are required")
		return
	}
	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "starts_at must be RFC3339")
		return
	}

	e := event{
		ID:         id.New("evt"),
		Title:      title,
		StartsAt:   startsAt,
		PriceCents: req.PriceCents,
		Capacity:   req.Capacity,
		Available:  req.Capacity,
	}
	err = s.db.QueryRow(r.Context(), `
		insert into events (id, title, starts_at, price_cents, capacity, available)
		values ($1, $2, $3, $4, $5, $6)
		returning created_at
	`, e.ID, e.Title, e.StartsAt, e.PriceCents, e.Capacity, e.Available).Scan(&e.CreatedAt)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create event")
		return
	}
	_ = s.redis.Del(r.Context(), eventsCacheKey).Err()
	httpx.WriteJSON(w, http.StatusCreated, e)
}

func (s *server) eventAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "events" || parts[2] != "reserve" {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	if !httpx.RequireMethod(w, r, http.MethodPost) {
		return
	}
	s.reserve(w, r, parts[1])
}

func (s *server) reserve(w http.ResponseWriter, r *http.Request, eventID string) {
	var req reserveRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.OrderID == "" || req.UserID == "" || req.Quantity <= 0 {
		httpx.Error(w, http.StatusBadRequest, "order_id, user_id and positive quantity are required")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not start tx")
		return
	}
	defer tx.Rollback(r.Context())

	var existingID string
	err = tx.QueryRow(r.Context(), `select id from reservations where order_id = $1`, req.OrderID).Scan(&existingID)
	if err == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"reservation_id": existingID,
			"status":         "reserved",
			"idempotent":     true,
		})
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusInternalServerError, "could not check reservation")
		return
	}

	var available int
	err = tx.QueryRow(r.Context(), `
		update events
		set available = available - $1, updated_at = now()
		where id = $2 and available >= $1
		returning available
	`, req.Quantity, eventID).Scan(&available)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusConflict, "not enough tickets")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not reserve tickets")
		return
	}

	reservationID := id.New("rsv")
	_, err = tx.Exec(r.Context(), `
		insert into reservations (id, order_id, event_id, user_id, quantity, status)
		values ($1, $2, $3, $4, $5, 'reserved')
	`, reservationID, req.OrderID, eventID, req.UserID, req.Quantity)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			httpx.Error(w, http.StatusConflict, "reservation already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not create reservation")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not commit reservation")
		return
	}
	_ = s.redis.Del(r.Context(), eventsCacheKey).Err()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"reservation_id": reservationID,
		"event_id":       eventID,
		"quantity":       req.Quantity,
		"available":      available,
		"status":         "reserved",
	})
}
