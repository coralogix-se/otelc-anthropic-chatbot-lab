// Anthropic LLM chatbot demo for OpenTelemetry Go compile-time instrumentation (otelc).
//
// Intentionally contains zero OpenTelemetry import/setup code. Build with
// `otelc go build` to inject instrumentation for net/http, Anthropic, Redis,
// database/sql, and log/slog.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
)

const (
	defaultModel       = "claude-sonnet-4-5"
	maxHistoryMessages = 20
	sessionTTL         = 24 * time.Hour
)

type server struct {
	log      *slog.Logger
	anthropic anthropic.Client
	rdb      *redis.Client
	db       *sql.DB
	model    string
	static   http.Handler
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type chatResponse struct {
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
	Model     string `json:"model"`
	Cached    bool   `json:"cached,omitempty"`
}

type historyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	addr := flag.String("addr", envOr("LISTEN_ADDR", ":8080"), "HTTP listen address")
	model := flag.String("model", envOr("ANTHROPIC_MODEL", defaultModel), "Anthropic model")
	redisAddr := flag.String("redis-addr", envOr("REDIS_ADDR", "localhost:6379"), "Redis address")
	dbPath := flag.String("db-path", envOr("DB_PATH", "./data/chatbot.db"), "SQLite database path")
	staticDir := flag.String("static-dir", envOr("STATIC_DIR", "./web"), "Static web UI directory")
	logLevel := flag.String("log-level", envOr("LOG_LEVEL", "info"), "Log level")
	flag.Parse()

	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		logger.Warn("ANTHROPIC_API_KEY not set; /api/chat will return 503 until configured")
	}

	db, err := openDB(*dbPath)
	if err != nil {
		logger.Error("open database failed", "error", err, "path", *dbPath)
		os.Exit(1)
	}
	defer db.Close()

	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis ping failed", "error", err, "addr", *redisAddr)
		os.Exit(1)
	}
	defer rdb.Close()

	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	srv := &server{
		log:       logger,
		anthropic: client,
		rdb:       rdb,
		db:        db,
		model:     *model,
		static:    http.FileServer(http.Dir(*staticDir)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", srv.handleHealth)
	mux.HandleFunc("GET /api/sessions/{id}", srv.handleGetSession)
	mux.HandleFunc("POST /api/chat", srv.handleChat)
	mux.Handle("GET /", srv.static)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		ln, err := net.Listen("tcp", *addr)
		if err != nil {
			logger.Error("listen failed", "error", err)
			os.Exit(1)
		}
		logger.Info("chatbot listening",
			"addr", ln.Addr().String(),
			"model", *model,
			"redis", *redisAddr,
			"db", *dbPath,
		)
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	// Give otelc-injected exporters time to flush.
	time.Sleep(2 * time.Second)
	logger.Info("shutdown complete")
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := map[string]string{"status": "ok"}
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		status["redis"] = "down"
		status["status"] = "degraded"
	} else {
		status["redis"] = "up"
	}
	if err := s.db.PingContext(ctx); err != nil {
		status["db"] = "down"
		status["status"] = "degraded"
	} else {
		status["db"] = "up"
	}

	code := http.StatusOK
	if status["status"] != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, status)
}

func (s *server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	messages, err := s.loadHistory(r.Context(), sessionID)
	if err != nil {
		s.log.Error("load history failed", "error", err, "session_id", sessionID)
		http.Error(w, "failed to load session", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"messages":   messages,
	})
}

func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = uuid.NewString()
	}

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		http.Error(w, "ANTHROPIC_API_KEY is not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	s.log.Info("chat request received",
		"session_id", req.SessionID,
		"message_len", len(req.Message),
	)

	// Idempotent cache for identical prompts within a short window.
	cacheKey := fmt.Sprintf("chat:cache:%s:%x", req.SessionID, hashString(req.Message))
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil && cached != "" {
		s.log.Info("serving cached reply", "session_id", req.SessionID)
		writeJSON(w, http.StatusOK, chatResponse{
			SessionID: req.SessionID,
			Reply:     cached,
			Model:     s.model,
			Cached:    true,
		})
		return
	}

	if err := s.appendMessage(ctx, req.SessionID, "user", req.Message); err != nil {
		s.log.Error("persist user message failed", "error", err)
		http.Error(w, "failed to persist message", http.StatusInternalServerError)
		return
	}

	history, err := s.loadHistory(ctx, req.SessionID)
	if err != nil {
		s.log.Error("load history failed", "error", err)
		http.Error(w, "failed to load history", http.StatusInternalServerError)
		return
	}

	reply, err := s.askClaude(ctx, history)
	if err != nil {
		s.log.Error("anthropic request failed", "error", err, "session_id", req.SessionID)
		http.Error(w, "llm request failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	if err := s.appendMessage(ctx, req.SessionID, "assistant", reply); err != nil {
		s.log.Error("persist assistant message failed", "error", err)
		http.Error(w, "failed to persist reply", http.StatusInternalServerError)
		return
	}

	_ = s.rdb.Set(ctx, cacheKey, reply, 2*time.Minute).Err()
	_ = s.rdb.Set(ctx, "chat:session:"+req.SessionID+":last_active", time.Now().UTC().Format(time.RFC3339), sessionTTL).Err()

	writeJSON(w, http.StatusOK, chatResponse{
		SessionID: req.SessionID,
		Reply:     reply,
		Model:     s.model,
	})
}

func (s *server) askClaude(ctx context.Context, history []historyMessage) (string, error) {
	msgs := make([]anthropic.MessageParam, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case "user":
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	message, err := s.anthropic.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(s.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{
				Type: "text",
				Text: "You are a helpful assistant in a Coralogix OpenTelemetry compile-instrumentation lab. Keep answers concise.",
			},
		},
		Messages: msgs,
	})
	if err != nil {
		return "", err
	}

	var content string
	for _, block := range message.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			content = text.Text
			break
		}
	}
	if content == "" {
		return "", errors.New("no text content in anthropic response")
	}

	s.log.Info("anthropic response",
		"model", message.Model,
		"input_tokens", message.Usage.InputTokens,
		"output_tokens", message.Usage.OutputTokens,
	)
	return content, nil
}

func (s *server) appendMessage(ctx context.Context, sessionID, role, content string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (session_id, role, content, created_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, role, content, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *server) loadHistory(ctx context.Context, sessionID string) ([]historyMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT role, content
		FROM (
			SELECT role, content, id
			FROM messages
			WHERE session_id = ?
			ORDER BY id DESC
			LIMIT ?
		)
		ORDER BY id ASC
	`, sessionID, maxHistoryMessages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []historyMessage
	for rows.Next() {
		var m historyMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func loggingMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		log.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newLogger(levelStr string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hashString(s string) uint32 {
	// FNV-1a 32-bit — good enough for short cache keys.
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
