package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "gateway url")
	eventID := flag.String("event", "", "event id")
	users := flag.Int("users", 20, "number of users")
	orders := flag.Int("orders", 100, "number of orders")
	quantity := flag.Int("qty", 1, "tickets per order")
	flag.Parse()
	if *eventID == "" {
		panic("pass -event")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	tokens := make([]string, *users)
	for i := range tokens {
		email := fmt.Sprintf("load-%d-%d@example.com", time.Now().UnixNano(), i)
		token, err := register(client, *baseURL, email, "secret123")
		if err != nil {
			panic(err)
		}
		tokens[i] = token
	}

	var ok, failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < *orders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := tokens[i%len(tokens)]
			status, err := createOrder(client, *baseURL, token, *eventID, *quantity, fmt.Sprintf("load-%d", i))
			if err == nil && status >= 200 && status < 300 {
				ok.Add(1)
				return
			}
			failed.Add(1)
		}(i)
	}
	wg.Wait()
	fmt.Printf("done: confirmed=%d failed=%d\n", ok.Load(), failed.Load())
}

func register(client *http.Client, baseURL, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"email":    email,
		"password": password,
		"name":     "Load User",
	})
	resp, err := client.Post(baseURL+"/users/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("register failed: %s", string(raw))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

func createOrder(client *http.Client, baseURL, token, eventID string, quantity int, key string) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"event_id":        eventID,
		"quantity":        quantity,
		"idempotency_key": key,
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
