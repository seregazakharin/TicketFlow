package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketflow/internal/config"
)

func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, config.Env("DATABASE_URL", "postgres://ticketflow:ticketflow@localhost:5432/ticketflow?sslmode=disable"))
}
