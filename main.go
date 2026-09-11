package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/discussion"
	"github.com/maxdollinger/meeth-councile/internal/llm"
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
	defaultTopic         = "Are there mind-independent moral facts, or do we construct morality?"
	defaultResearchModel = "deepseek/deepseek-v4-flash-0731"
	defaultDBPath        = "debate.db"
)

func main() {
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		log.Fatal("OPENROUTER_API_KEY is required")
	}

	topic := env("TOPIC", defaultTopic)
	dbPath := env("DB_PATH", defaultDBPath)
	researchModel := env("RESEARCH_MODEL", defaultResearchModel)
	discussionID := env("DISCUSSION_ID", time.Now().Format("20060102-150405"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer db.Close()

	client := llm.New(apiKey, &http.Client{Timeout: 5 * time.Minute})
	assistant := research.New(client, researchModel, store.NewLog(db))

	repo := store.NewMemory(db)
	names := prompts.Names()
	speakers := make([]discussion.Speaker, 0, len(names))
	for _, name := range names {
		mem, err := memory.New(ctx, repo, discussionID, name)
		if err != nil {
			log.Fatalf("memory %s: %v", name, err)
		}
		p, err := persona.New(mem, client, models[0], assistant)
		if err != nil {
			log.Fatalf("persona %s: %v", name, err)
		}
		speakers = append(speakers, p)
	}
	if len(speakers) == 0 {
		log.Fatal("no personas found")
	}

	d, err := discussion.New(topic, speakers, models, store.NewTurns(db), discussion.WithMaxRounds(1))
	if err != nil {
		log.Fatalf("build discussion: %v", err)
	}

	log.Printf("discussion %s: %d personas, topic %q", discussionID, len(speakers), topic)
	res, err := d.Run(ctx, discussionID)
	if err != nil {
		log.Printf("discussion stopped early: %v", err)
	}
	printTranscript(discussionID, res)
}

// printTranscript writes the simple speaker/what-was-said record to stdout.
func printTranscript(id string, res discussion.Result) {
	fmt.Printf("\n=== discussion %s — %d round(s), ended: %s ===\n\n", id, res.Rounds, res.Ended)
	for _, turn := range res.Turns {
		if turn.Round == 0 {
			fmt.Printf("[opening] %s: %s\n\n", turn.Speaker, turn.Content)
			continue
		}
		fmt.Printf("round %d · %s (%s):\n%s\n\n", turn.Round, turn.Speaker, turn.Model, turn.Content)
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
