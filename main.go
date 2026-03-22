package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	bulkDeleteMax   = 100
	fetchLimit      = 100
	bulkMaxAge      = 14 * 24 * time.Hour // Discord's 14-day limit for bulk delete
	defaultInterval = 6 * time.Hour
)

type config struct {
	Token     string
	ChannelID string
	MaxAge    time.Duration
	Interval  time.Duration
}

func loadConfig() (config, error) {
	var cfg config

	cfg.Token = os.Getenv("DISCORD_TOKEN")
	if cfg.Token == "" {
		return cfg, fmt.Errorf("DISCORD_TOKEN is required")
	}

	cfg.ChannelID = os.Getenv("CHANNEL_ID")
	if cfg.ChannelID == "" {
		return cfg, fmt.Errorf("CHANNEL_ID is required")
	}

	maxAge := os.Getenv("MAX_AGE")
	if maxAge == "" {
		return cfg, fmt.Errorf("MAX_AGE is required (e.g. \"720h\")")
	}
	d, err := time.ParseDuration(maxAge)
	if err != nil {
		return cfg, fmt.Errorf("invalid MAX_AGE %q: %w", maxAge, err)
	}
	cfg.MaxAge = d

	cfg.Interval = defaultInterval
	if v := os.Getenv("INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid INTERVAL %q: %w", v, err)
		}
		cfg.Interval = d
	}

	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		slog.Error("failed to create discord session", "error", err)
		os.Exit(1)
	}

	session.Identify.Intents = discordgo.IntentsNone
	session.AddHandler(func(_ *discordgo.Session, r *discordgo.RateLimit) {
		slog.Warn("rate limited", "url", r.URL, "retry_after", r.RetryAfter)
	})
	if err := session.Open(); err != nil {
		slog.Error("failed to open gateway connection", "error", err)
		os.Exit(1)
	}
	defer session.Close()

	if err := session.UpdateStatusComplex(discordgo.UpdateStatusData{
		Activities: []*discordgo.Activity{{
			Name: "cleanup",
			Type: discordgo.ActivityTypeWatching,
		}},
		Status: "online",
	}); err != nil {
		slog.Warn("failed to set status", "error", err)
	}

	slog.Info("starting lethe",
		"channel", cfg.ChannelID,
		"max_age", cfg.MaxAge,
		"interval", cfg.Interval,
	)

	runCleanup(ctx, session, cfg)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-ticker.C:
			runCleanup(ctx, session, cfg)
		}
	}
}

func runCleanup(ctx context.Context, s *discordgo.Session, cfg config) {
	slog.Info("cleanup started")

	messages, err := fetchExpiredMessages(ctx, s, cfg.ChannelID, cfg.MaxAge)
	if err != nil {
		slog.Error("failed to fetch messages", "error", err)
		return
	}

	if len(messages) == 0 {
		slog.Info("no expired messages found")
		return
	}

	now := time.Now()
	var bulkIDs, oldIDs []string
	for _, m := range messages {
		if now.Sub(m.Timestamp) < bulkMaxAge {
			bulkIDs = append(bulkIDs, m.ID)
		} else {
			oldIDs = append(oldIDs, m.ID)
		}
	}

	deleted := 0
	deleted += bulkDelete(s, cfg.ChannelID, bulkIDs)
	deleted += deleteIndividual(ctx, s, cfg.ChannelID, oldIDs)

	slog.Info("cleanup complete", "deleted", deleted, "total_expired", len(messages))
}

func fetchExpiredMessages(ctx context.Context, s *discordgo.Session, channelID string, maxAge time.Duration) ([]*discordgo.Message, error) {
	var expired []*discordgo.Message
	cutoff := time.Now().Add(-maxAge)
	beforeID := ""

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		msgs, err := s.ChannelMessages(channelID, fetchLimit, beforeID, "", "")
		if err != nil {
			return nil, fmt.Errorf("fetching messages: %w", err)
		}

		if len(msgs) == 0 {
			break
		}

		for _, m := range msgs {
			if m.Timestamp.Before(cutoff) {
				expired = append(expired, m)
			}
		}

		beforeID = msgs[len(msgs)-1].ID

		if len(msgs) < fetchLimit {
			break
		}
	}

	slog.Info("fetched expired messages", "count", len(expired))
	return expired, nil
}

func bulkDelete(s *discordgo.Session, channelID string, ids []string) int {
	if len(ids) == 0 {
		return 0
	}

	deleted := 0
	for i := 0; i < len(ids); i += bulkDeleteMax {
		end := i + bulkDeleteMax
		if end > len(ids) {
			end = len(ids)
		}

		chunk := ids[i:end]

		if len(chunk) < 2 {
			if err := s.ChannelMessageDelete(channelID, chunk[0]); err != nil {
				slog.Error("failed to delete message", "id", chunk[0], "error", err)
			} else {
				deleted++
			}
			continue
		}

		if err := s.ChannelMessagesBulkDelete(channelID, chunk); err != nil {
			slog.Error("bulk delete failed", "count", len(chunk), "error", err)
		} else {
			deleted += len(chunk)
			slog.Info("bulk deleted messages", "count", len(chunk))
		}
	}
	return deleted
}

func deleteIndividual(ctx context.Context, s *discordgo.Session, channelID string, ids []string) int {
	total := len(ids)
	deleted := 0
	start := time.Now()
	for i, id := range ids {
		if ctx.Err() != nil {
			slog.Info("shutdown requested, stopping individual deletes", "remaining", total-i)
			return deleted
		}

		if err := s.ChannelMessageDelete(channelID, id); err != nil {
			slog.Error("failed to delete message", "id", id, "error", err)
			continue
		}
		deleted++

		if deleted%10 == 0 || deleted == total {
			elapsed := time.Since(start)
			perMsg := elapsed / time.Duration(deleted)
			remaining := time.Duration(total-deleted) * perMsg
			slog.Info("individual delete progress",
				"deleted", deleted,
				"total", total,
				"eta", remaining.Truncate(time.Second),
			)
		}
	}
	return deleted
}
