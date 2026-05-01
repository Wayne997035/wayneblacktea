package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
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

	// Optional DB connectivity check. TLS errors MUST be logged so a
	// misconfigured PGSSLROOTCERT in production cannot silently fall back to
	// pgx default TLS behaviour — the security posture must be visible to the
	// operator at boot time.
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err == nil {
			tlsCfg, tlsErr := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
			switch {
			case tlsErr != nil:
				slog.Error("discordbot DB TLS config failed; skipping optional connectivity check",
					"err", tlsErr)
			case tlsCfg != nil:
				cfg.ConnConfig.TLSConfig = tlsCfg
				if pool, err := pgxpool.NewWithConfig(context.Background(), cfg); err == nil {
					defer pool.Close()
					slog.Info("database connected")
				}
			default:
				// Non-production with no PGSSLROOTCERT: nil tls.Config means
				// pgx falls back to system CA pool, which is acceptable here.
				if pool, err := pgxpool.NewWithConfig(context.Background(), cfg); err == nil {
					defer pool.Close()
					slog.Info("database connected (system CA)")
				}
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
