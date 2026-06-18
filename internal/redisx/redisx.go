package redisx

import (
	"github.com/redis/go-redis/v9"

	"ticketflow/internal/config"
)

func Connect() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     config.Env("REDIS_ADDR", "localhost:6379"),
		Password: config.Env("REDIS_PASSWORD", ""),
		DB:       config.EnvInt("REDIS_DB", 0),
	})
}
