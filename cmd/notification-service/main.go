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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"

	"ticketflow/internal/auth"
	"ticketflow/internal/config"
	"ticketflow/internal/httpx"
	"ticketflow/internal/id"
	"ticketflow/internal/kafkax"
	"ticketflow/internal/postgres"
)

type server struct {
	db         *pgxpool.Pool
	authSecret string
}

type notification struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	OrderID   string    `json:"order_id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	ctx := context.Background()
	db, err := postgres.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	s := &server{
		db:         db,
		authSecret: config.Env("JWT_SECRET", "dev-secret-change-me"),
	}
	go consumeOrders(ctx, db)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/notifications", s.listNotifications)

	addr := ":" + config.Env("PORT", "8084")
	log.Printf("notification-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func consumeOrders(ctx context.Context, db *pgxpool.Pool) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     config.KafkaBrokers(),
		GroupID:     "notification-service",
		GroupTopics: []string{"orders"},
		MinBytes:    1,
		MaxBytes:    10e6,
	})
	defer reader.Close()

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("kafka read error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		var event kafkax.Event
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("bad event payload: %v", err)
			continue
		}
		if event.UserID == "" || event.OrderID == "" {
			continue
		}
		n := notification{
			ID:      id.New("ntf"),
			UserID:  event.UserID,
			OrderID: event.OrderID,
			Kind:    event.Type,
			Message: messageFor(event),
		}
		_, err = db.Exec(ctx, `
			insert into notifications (id, user_id, order_id, kind, message)
			values ($1, $2, $3, $4, $5)
			on conflict do nothing
		`, n.ID, n.UserID, n.OrderID, n.Kind, n.Message)
		if err != nil {
			log.Printf("could not store notification: %v", err)
		}
	}
}

func messageFor(event kafkax.Event) string {
	switch event.Type {
	case "order.created":
		return "Order " + event.OrderID + " confirmed"
	case "order.rejected":
		reason := strings.TrimSpace(event.Reason)
		if reason == "" {
			reason = "reservation failed"
		}
		return "Order " + event.OrderID + " rejected: " + reason
	default:
		return "Order " + event.OrderID + " changed status to " + event.Status
	}
}

func (s *server) listNotifications(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodGet) {
		return
	}
	token, err := httpx.BearerToken(r)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, err.Error())
		return
	}
	claims, err := auth.VerifyToken(s.authSecret, token)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, err.Error())
		return
	}
	rows, err := s.db.Query(r.Context(), `
		select id, user_id, order_id, kind, message, created_at
		from notifications
		where user_id = $1
		order by created_at desc
		limit 50
	`, claims.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"notifications": []notification{}})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load notifications")
		return
	}
	defer rows.Close()

	items := make([]notification, 0)
	for rows.Next() {
		var n notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.OrderID, &n.Kind, &n.Message, &n.CreatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "could not scan notification")
			return
		}
		items = append(items, n)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"notifications": items})
}
