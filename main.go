package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/discussion"
	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/logging"
	"github.com/maxdollinger/meeth-councile/internal/memory"
	"github.com/maxdollinger/meeth-councile/internal/persona"
	"github.com/maxdollinger/meeth-councile/internal/prompts"
	"github.com/maxdollinger/meeth-councile/internal/research"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// models are the four models a persona may be assigned in any round.
var models = []string{
	"z-ai/glm-5.3",
	"deepseek/deepseek-v4-pro-0813",
	"x-ai/grok-4.6",
	"openai/gpt-5.6-sol",
}

const (
	defaultTitle = "Kernfrage der Metaethik"
	defaultTopic = `Diese Diskussion behandelt die Kernfrage der Metaethik:

Wenn wir sagen, dass eine Handlung moralisch falsch ist — behaupten wir damit eine Tatsache, die wahr ist, unabhängig davon, was irgendjemand glaubt, fühlt oder vereinbart? Und wenn ja: was macht diese Tatsache wahr? Wenn nein: was tun wir dann eigentlich, wenn wir so sprechen?

Legt eure Position dar, geht auf das ein, was die anderen sagen, und lasst euch nur von Gründen bewegen — nicht davon, wie viele anderer Meinung sind.`
	defaultResearchModel = "deepseek/deepseek-v4-flash-0731"
	defaultDBPath        = "debate.db"
	defaultAddr          = ":8080"
)

func main() {
	logger := logging.New(os.Stderr, os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"))

	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		fatal(logger, "OPENROUTER_API_KEY is required")
	}

	title := env("TITLE", defaultTitle)
	topic := env("TOPIC", defaultTopic)
	dbPath := env("DB_PATH", defaultDBPath)
	researchModel := env("RESEARCH_MODEL", defaultResearchModel)
	addr := env("ADDR", defaultAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(dbPath)
	if err != nil {
		fatal(logger, "open store", "db_path", dbPath, "err", err)
	}
	defer db.Close()
	logger.Info("store opened", "db_path", dbPath)

	turns := store.NewRecorder(db)

	client := llm.New(apiKey, &http.Client{Timeout: 5 * time.Minute}, llm.WithLogger(logger))
	assistant := research.New(client, researchModel, store.NewLog(db), research.WithLogger(logger))

	repo := store.NewMemory(db)
	defs, err := prompts.All()
	if err != nil {
		fatal(logger, "load personas", "err", err)
	}
	speakers := make([]discussion.Speaker, 0, len(defs))
	for _, def := range defs {
		mem, err := memory.New(ctx, repo, def.Name, memory.WithLogger(logger))
		if err != nil {
			fatal(logger, "open memory", "persona", def.Name, "err", err)
		}
		p, err := persona.New(mem, client, models[0], assistant, persona.WithLogger(logger))
		if err != nil {
			fatal(logger, "build persona", "persona", def.Name, "err", err)
		}
		speakers = append(speakers, p)
	}
	if len(speakers) == 0 {
		fatal(logger, "no personas found")
	}

	d, err := discussion.New(
		topic, speakers, models, turns,
		discussion.WithMaxRounds(100),
		discussion.WithLogger(logger),
	)
	if err != nil {
		fatal(logger, "build discussion", "err", err)
	}

	discussionTmpl, err := template.ParseFiles("web/discussion.html")
	if err != nil {
		fatal(logger, "parse discussion template", "err", err)
	}

	indexTmpl, err := template.ParseFiles("web/index.html")
	if err != nil {
		fatal(logger, "parse index template", "err", err)
	}

	srv := &server{
		turns:         turns.Turns,
		title:         title,
		personas:      defs,
		models:        models,
		researchModel: researchModel,
		logger:        logger,
	}
	srv.running.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/discussion.html", srv.handle(discussionTmpl))
	mux.HandleFunc("/{$}", srv.index(indexTmpl))
	mux.Handle("/", http.FileServer(http.Dir("web")))

	httpSrv := &http.Server{Addr: addr, Handler: logging.Middleware(logger, mux)}

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		res, err := d.Run(ctx)
		if err != nil {
			logger.Error("discussion stopped early", "err", err)
		}
		srv.running.Store(false)
		printTranscript(res)
		logger.Info(
			"transcript written",
			"rounds", res.Rounds,
			"ended", string(res.Ended),
			"turns", len(res.Turns),
			"model_calls", len(res.Calls),
			"total_tokens", totalUsage(res).TotalTokens,
			"cost", totalUsage(res).Cost,
		)
	}()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "err", err)
		}
	}()

	logger.Info("listening", "addr", addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(logger, "serve", "addr", addr, "err", err)
	}
	<-finished
}

// server renders the live discussion transcript from the shared turns store.
type server struct {
	turns         *store.Turns
	title         string
	personas      []prompts.Definition
	models        []string
	researchModel string
	logger        *slog.Logger
	running       atomic.Bool
}

// discussionView is the data the discussion.html template renders.
type discussionView struct {
	Title   string
	Entries []store.SpeakEntry
	Running bool
}

// indexView is the data the index.html template renders.
type indexView struct {
	Personas      []prompts.Definition
	Models        []string
	ResearchModel string
}

// index returns the handler that renders the landing page with the roster of
// personas and the position each one argues for.
func (s *server) index(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, indexView{
			Personas:      s.personas,
			Models:        s.models,
			ResearchModel: s.researchModel,
		}); err != nil {
			s.logger.Error("render index", "err", err)
		}
	}
}

// handle returns the handler that renders the whole discussion so far, as every
// speak output ordered by time.
func (s *server) handle(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := s.turns.SpeakEntries(r.Context())
		if err != nil {
			s.logger.Error("load discussion", "err", err)
			http.Error(w, "could not load the discussion", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, discussionView{
			Title:   s.title,
			Entries: entries,
			Running: s.running.Load(),
		}); err != nil {
			s.logger.Error("render discussion", "err", err)
		}
	}
}

// printTranscript writes every speak entry, with its time, to stdout.
func printTranscript(res discussion.Result) {
	fmt.Printf("\n=== %d round(s), ended: %s ===\n", res.Rounds, res.Ended)
	usage := totalUsage(res)
	fmt.Printf("=== %d model calls · %d tokens · $%.6f ===\n\n", len(res.Calls), usage.TotalTokens, usage.Cost)
	for _, e := range res.Entries {
		stamp := e.CreatedAt.Format("2006-01-02 15:04:05")
		if e.Round == 0 {
			fmt.Printf("[opening] %s (%s):\n%s\n\n", e.Speaker, stamp, e.Content)
			continue
		}
		fmt.Printf("%s · round %d · %s · %s (%s):\n%s\n\n", stamp, e.Round, e.Speaker, e.Kind, e.Model, e.Content)
	}
}

// totalUsage sums the tokens and cost of every model call in a run.
func totalUsage(res discussion.Result) llm.Usage {
	var total llm.Usage
	for _, call := range res.Calls {
		total.InputTokens += call.Usage.InputTokens
		total.OutputTokens += call.Usage.OutputTokens
		total.TotalTokens += call.Usage.TotalTokens
		total.Cost += call.Usage.Cost
	}
	return total
}

func fatal(logger *slog.Logger, msg string, args ...any) {
	logger.Error(msg, args...)
	os.Exit(1)
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
