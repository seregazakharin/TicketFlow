package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	"ticketflow/internal/auth"
	"ticketflow/internal/config"
	"ticketflow/internal/httpx"
	"ticketflow/internal/id"
	"ticketflow/internal/kafkax"
	"ticketflow/internal/postgres"
	"ticketflow/internal/redisx"
)

type server struct {
	db           *pgxpool.Pool
	redis        *redis.Client
	writer       *kafka.Writer
	httpClient   *http.Client
	authSecret   string
	eventBaseURL string
}

type order struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	EventID     string    `json:"event_id"`
	Quantity    int       `json:"quantity"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
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
	writer := kafkax.Writer("orders")
	defer writer.Close()

	s := &server{
		db:           db,
		redis:        rdb,
		writer:       writer,
		httpClient:   &http.Client{Timeout: config.EnvDuration("HTTP_TIMEOUT", 3*time.Second)},
		authSecret:   config.Env("JWT_SECRET", "dev-secret-change-me"),
		eventBaseURL: strings.TrimRight(config.Env("EVENT_SERVICE_URL", "http://localhost:8082"), "/"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/orders", s.orders)
	mux.HandleFunc("/orders/", s.orderByID)

	addr := ":" + config.Env("PORT", "8083")
	log.Printf("order-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type createOrderRequest struct {
	EventID        string `json:"event_id"`
	Quantity       int    `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *server) orders(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodPost) {
		return
	}
	claims, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req createOrderRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.EventID) == "" || req.Quantity <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		httpx.Error(w, http.StatusBadRequest, "event_id, quantity and idempotency_key are required")
		return
	}

	cacheKey := "idem:orders:" + claims.UserID + ":" + req.IdempotencyKey
	locked, err := s.redis.SetNX(r.Context(), cacheKey, "processing", 10*time.Minute).Result()
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "idempotency store unavailable")
		return
	}
	if !locked {
		value, _ := s.redis.Get(r.Context(), cacheKey).Bytes()
		if bytes.HasPrefix(value, []byte("{")) {
			httpx.WriteRawJSON(w, http.StatusOK, value)
			return
		}
		httpx.Error(w, http.StatusConflict, "order with this idempotency key is processing")
		return
	}

	created, statusCode := s.createOrder(r.Context(), claims.UserID, req.EventID, req.Quantity)
	body, _ := json.Marshal(created)
	_ = s.redis.Set(r.Context(), cacheKey, body, 24*time.Hour).Err()
	httpx.WriteRawJSON(w, statusCode, body)
}

func (s *server) createOrder(ctx context.Context, userID, eventID string, quantity int) (order, int) {
	ord := order{
		ID:       id.New("ord"),
		UserID:   userID,
		EventID:  eventID,
		Quantity: quantity,
		Status:   "pending",
	}
	err := s.db.QueryRow(ctx, `
		insert into orders (id, user_id, event_id, quantity, status)
		values ($1, $2, $3, $4, $5)
		returning created_at
	`, ord.ID, ord.UserID, ord.EventID, ord.Quantity, ord.Status).Scan(&ord.CreatedAt)
	if err != nil {
		ord.Status = "rejected"
		ord.Reason = "could not create order"
		return ord, http.StatusInternalServerError
	}

	if err := s.reserveTickets(ctx, ord); err != nil {
		ord.Status = "rejected"
		ord.Reason = err.Error()
		_, _ = s.db.Exec(ctx, `
			update orders
			set status = 'rejected', reason = $2, updated_at = now()
			where id = $1
		`, ord.ID, ord.Reason)
		_ = kafkax.Publish(ctx, s.writer, kafkax.Event{
			Type:     "order.rejected",
			OrderID:  ord.ID,
			UserID:   userID,
			EventID:  eventID,
			Quantity: quantity,
			Status:   ord.Status,
			Reason:   ord.Reason,
		})
		return ord, http.StatusConflict
	}

	ord.Status = "confirmed"
	ord.ConfirmedAt = time.Now().UTC()
	_, err = s.db.Exec(ctx, `
		update orders
		set status = 'confirmed', confirmed_at = $2, updated_at = now()
		where id = $1
	`, ord.ID, ord.ConfirmedAt)
	if err != nil {
		ord.Status = "rejected"
		ord.Reason = "could not confirm order"
		return ord, http.StatusInternalServerError
	}
	_ = kafkax.Publish(ctx, s.writer, kafkax.Event{
		Type:     "order.created",
		OrderID:  ord.ID,
		UserID:   userID,
		EventID:  eventID,
		Quantity: quantity,
		Status:   ord.Status,
	})
	return ord, http.StatusCreated
}

func (s *server) reserveTickets(ctx context.Context, ord order) error {
	payload, _ := json.Marshal(map[string]any{
		"order_id": ord.ID,
		"user_id":  ord.UserID,
		"quantity": ord.Quantity,
	})
	url := s.eventBaseURL + "/events/" + ord.EventID + "/reserve"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return errors.New("inventory service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if len(body) == 0 {
		return errors.New("tickets reservation rejected")
	}
	return errors.New(string(body))
}

func (s *server) orderByID(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodGet) {
		return
	}
	claims, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	orderID := strings.TrimPrefix(r.URL.Path, "/orders/")
	if orderID == "" {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	var ord order
	err := s.db.QueryRow(r.Context(), `
		select id, user_id, event_id, quantity, status, coalesce(reason, ''), created_at, coalesce(confirmed_at, '0001-01-01'::timestamptz)
		from orders
		where id = $1 and user_id = $2
	`, orderID, claims.UserID).Scan(&ord.ID, &ord.UserID, &ord.EventID, &ord.Quantity, &ord.Status, &ord.Reason, &ord.CreatedAt, &ord.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load order")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ord)
}

func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (auth.Claims, bool) {
	token, err := httpx.BearerToken(r)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, err.Error())
		return auth.Claims{}, false
	}
	claims, err := auth.VerifyToken(s.authSecret, token)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, err.Error())
		return auth.Claims{}, false
	}
	return claims, true
}
