package discordbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Bot listens for commands and bridges Discord → wayneblacktea REST API.
type Bot struct {
	session    *discordgo.Session
	analyzer   *Analyzer
	apiURL     string
	apiKey     string
	guildID    string // if set, slash commands are registered guild-scoped (instant); otherwise global (~1h)
	httpClient *http.Client
}

var slashCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "analyze",
		Description: "Fetch a URL or analyze text, save to knowledge base if valuable",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "input", Description: "URL or text to analyze", Required: true},
		},
	},
	{
		Name:        "note",
		Description: "Save a quick TIL note directly to knowledge base",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "text", Description: "What you learned", Required: true},
		},
	},
	{
		Name:        "search",
		Description: "Search your knowledge base",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "query", Description: "Search query", Required: true},
		},
	},
	{
		Name:        "recent",
		Description: "List your 5 most recently saved knowledge items",
	},
}

// New creates and configures a Bot but does not connect yet.
// guildID: if non-empty, slash commands are registered guild-scoped (visible instantly);
// if empty, they are registered globally (up to 1 hour to propagate).
func New(botToken, groqAPIKey, apiURL, apiKey, guildID string) (*Bot, error) {
	s, err := discordgo.New("Bot " + botToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	b := &Bot{
		session:    s,
		analyzer:   NewAnalyzer(groqAPIKey),
		apiURL:     strings.TrimRight(apiURL, "/"),
		apiKey:     apiKey,
		guildID:    guildID,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	s.AddHandler(b.onMessage)
	s.AddHandler(b.onInteraction)
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent
	return b, nil
}

// Start opens the WebSocket connection and registers slash commands.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord session: %w", err)
	}
	scope := "global"
	if b.guildID != "" {
		scope = "guild:" + b.guildID
	}
	for _, cmd := range slashCommands {
		if _, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, b.guildID, cmd); err != nil {
			slog.Warn("register slash command failed", "cmd", cmd.Name, "scope", scope, "err", err)
		}
	}
	slog.Info("slash commands registered")
	return nil
}

// Stop closes the Discord session gracefully.
func (b *Bot) Stop() {
	_ = b.session.Close()
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	// Acknowledge immediately so Discord doesn't time out
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	data := i.ApplicationCommandData()
	optStr := func(name string) string {
		for _, o := range data.Options {
			if o.Name == name {
				return o.StringValue()
			}
		}
		return ""
	}

	var msg string
	switch data.Name {
	case "analyze":
		msg = b.runAnalyze(optStr("input"))
	case "note":
		msg = b.runNote(optStr("text"))
	case "search":
		msg = b.runSearch(optStr("query"))
	case "recent":
		msg = b.runRecent()
	}

	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: msg})
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	text := strings.TrimSpace(m.Content)
	switch {
	case strings.HasPrefix(text, "!analyze "):
		b.handleAnalyze(s, m.ChannelID, strings.TrimPrefix(text, "!analyze "))
	case strings.HasPrefix(text, "!note "):
		b.handleNote(s, m.ChannelID, strings.TrimPrefix(text, "!note "))
	case strings.HasPrefix(text, "!search "):
		b.handleSearch(s, m.ChannelID, strings.TrimPrefix(text, "!search "))
	case text == "!recent":
		b.handleRecent(s, m.ChannelID)
	}
}

func (b *Bot) handleAnalyze(s *discordgo.Session, channelID, input string) {
	reply(s, channelID, "Fetching...")
	reply(s, channelID, b.runAnalyze(input))
}

func (b *Bot) handleNote(s *discordgo.Session, channelID, text string) {
	reply(s, channelID, b.runNote(text))
}

func (b *Bot) handleSearch(s *discordgo.Session, channelID, query string) {
	reply(s, channelID, b.runSearch(query))
}

func (b *Bot) handleRecent(s *discordgo.Session, channelID string) {
	reply(s, channelID, b.runRecent())
}

func (b *Bot) runAnalyze(input string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var title, content string
	isURL := strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://")

	if isURL {
		var err error
		title, content, err = FetchURL(ctx, input)
		if err != nil {
			return fmt.Sprintf("Fetch failed: %v", err)
		}
		slog.Info("fetched URL", "url", input, "title", title, "content_len", len(content))
		if len(content) < 100 {
			return fmt.Sprintf("Fetch failed: extracted content too short (%d chars) — page may require login or is JS-rendered", len(content))
		}
	} else {
		title = truncate(input, 80)
		content = input
	}

	result, err := b.analyzer.Analyze(ctx, content)
	if err != nil {
		return fmt.Sprintf("Analysis failed: %v", err)
	}

	if !result.WorthSaving {
		return fmt.Sprintf("[%d/5] Skipped\nReason: %s", result.LearningValue, result.SkipReason)
	}

	var stored strings.Builder
	stored.WriteString(result.Summary)
	if len(result.KeyConcepts) > 0 {
		fmt.Fprintf(&stored, "\n\nKey concepts: %s", strings.Join(result.KeyConcepts, ", "))
	}
	if isURL {
		fmt.Fprintf(&stored, "\n\nSource: %s", input)
	}

	if err := b.saveKnowledge(ctx, saveParams{
		Type: result.SuggestedType, Title: title, Content: stored.String(),
		URL: input, Tags: result.Tags, Source: "discord", LearningValue: result.LearningValue,
	}); err != nil {
		var dupErr *errDuplicate
		if errors.As(err, &dupErr) {
			return fmt.Sprintf("Already saved similar content: %s", dupErr.message)
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Sprintf("Analysis done but save failed: %s", urlErr.Err)
		}
		return fmt.Sprintf("Analysis done but save failed: %v", err)
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "[%d/5] %s\n", result.LearningValue, title)
	fmt.Fprintf(&msg, "Summary: %s\n", result.Summary)
	if len(result.KeyConcepts) > 0 {
		fmt.Fprintf(&msg, "Concepts: %s\n", strings.Join(result.KeyConcepts, ", "))
	}
	fmt.Fprintf(&msg, "Saved — type: %s | tags: %s", result.SuggestedType, strings.Join(result.Tags, ", "))
	return msg.String()
}

func (b *Bot) runNote(text string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	title := truncate(text, 80)
	if err := b.saveKnowledge(ctx, saveParams{
		Type: "til", Title: title, Content: text, Source: "discord", LearningValue: 3,
	}); err != nil {
		return fmt.Sprintf("Save failed: %v", err)
	}
	return fmt.Sprintf("Saved to knowledge base\nType: til | Source: discord\n\n%s", title)
}

func (b *Bot) runSearch(query string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/knowledge/search?q=%s&limit=3", b.apiURL, url.QueryEscape(query)), nil)
	if err != nil {
		return fmt.Sprintf("Build request failed: %v", err)
	}
	req.Header.Set("X-API-Key", b.apiKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("Search failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var items []struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err := json.Unmarshal(body, &items); err != nil || len(items) == 0 {
		return "No results found."
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Results for \"%s\":\n", query)
	for i, item := range items {
		fmt.Fprintf(&out, "\n%d. %s\n   %s", i+1, item.Title, truncate(item.Content, 120))
	}
	return out.String()
}

func (b *Bot) runRecent() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/knowledge?limit=5", b.apiURL), nil)
	if err != nil {
		return fmt.Sprintf("Build request failed: %v", err)
	}
	req.Header.Set("X-API-Key", b.apiKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("Request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var items []struct {
		Title     string `json:"title"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err := json.Unmarshal(body, &items); err != nil || len(items) == 0 {
		return "No items found."
	}

	var out strings.Builder
	out.WriteString("Recent knowledge:\n")
	for i, item := range items {
		t, _ := time.Parse(time.RFC3339, item.CreatedAt)
		fmt.Fprintf(&out, "\n%d. [%s] %s (%s)", i+1, item.Type, item.Title, t.Format("01/02"))
	}
	return out.String()
}

type saveParams struct {
	Type          string
	Title         string
	Content       string
	URL           string
	Tags          []string
	Source        string
	LearningValue int
}

// errDuplicate is a sentinel used by saveKnowledge to signal a 409 Conflict response.
type errDuplicate struct{ message string }

func (e *errDuplicate) Error() string { return e.message }

func (b *Bot) saveKnowledge(ctx context.Context, p saveParams) error {
	payload := map[string]any{
		"type":           p.Type,
		"title":          p.Title,
		"content":        p.Content,
		"tags":           p.Tags,
		"source":         p.Source,
		"learning_value": p.LearningValue,
	}
	if strings.HasPrefix(p.URL, "http://") || strings.HasPrefix(p.URL, "https://") {
		payload["url"] = p.URL
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL+"/api/knowledge", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build save request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", b.apiKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("save knowledge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode == http.StatusConflict {
		// Extract the error message from the API response.
		var apiResp struct {
			Error string `json:"error"`
		}
		msg := "already saved similar content"
		if jsonErr := json.Unmarshal(raw, &apiResp); jsonErr == nil && apiResp.Error != "" {
			msg = apiResp.Error
		}
		return &errDuplicate{message: msg}
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("api error %d: %s", resp.StatusCode, raw)
	}
	return nil
}

func reply(s *discordgo.Session, channelID, msg string) {
	if _, err := s.ChannelMessageSend(channelID, msg); err != nil {
		slog.Warn("discord send failed", "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
