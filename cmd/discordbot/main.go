package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	envFile := flag.String("env", ".env", "env file to load")
	flag.Parse()
	if err := godotenv.Load(*envFile); err != nil {
		log.Fatalf("loading %s: %v", *envFile, err)
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN not set")
	}
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		return fmt.Errorf("GROQ_API_KEY not set")
	}
	apiURL := os.Getenv("WAYNEBLACKTEA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return fmt.Errorf("API_KEY not set")
	}
	guildID := os.Getenv("DISCORD_GUILD_ID") // optional; enables instant guild-scoped slash commands

	// Optional DB connectivity check
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err == nil {
			cfg.ConnConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
			if pool, err := pgxpool.NewWithConfig(context.Background(), cfg); err == nil {
				defer pool.Close()
				slog.Info("database connected")
			}
		}
	}

	bot, err := discordbot.New(botToken, groqKey, apiURL, apiKey, guildID)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}
	if err := bot.Start(); err != nil {
		return fmt.Errorf("start bot: %w", err)
	}
	defer bot.Stop()

	slog.Info("discord bot running — commands: !analyze <url|text>, !note <text>, !search <query>, !recent")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	return nil
}
