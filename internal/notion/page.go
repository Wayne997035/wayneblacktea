package notion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// dailyBriefingDateProperty is the Notion database property name we use as
// the per-day idempotency key. The user's Notion database MUST have a
// property of this exact name (Date type) for the upsert flow to work — the
// integration setup doc covers the manual database schema.
const dailyBriefingDateProperty = "Date"

// dailyBriefingTitleProperty is the Notion title column. Notion databases
// always have exactly one title-typed property; this name matches what
// CreatePage already uses elsewhere in the package, keeping a single
// schema for both manual sync_to_notion pages and scheduled briefings.
const dailyBriefingTitleProperty = "Name"

// dailyBriefingMaxBlocks caps the number of children blocks per page write.
// Notion allows up to 100 children in a single create-page call; we stay
// well under the limit so a busy day with many tasks + decisions still fits
// in one round trip.
const dailyBriefingMaxBlocks = 90

// dailyBriefingRichTextLimit enforces Notion's 2000-character cap per
// rich_text segment. Anything longer is truncated with an ellipsis so the
// API does not 400.
const dailyBriefingRichTextLimit = 2000

// UpsertDailyPage writes the briefing to the configured Notion database
// using the ISO date (YYYY-MM-DD, in the briefing.Date timezone) as the
// idempotency key:
//
//   - If a page with Date = isoDate exists, PATCH it (refresh title +
//     replace children blocks).
//   - Otherwise, POST a fresh page.
//
// Returns ErrClientNotConfigured when the receiver is nil so callers can
// log-and-skip instead of panicking. Any Notion API error is wrapped and
// returned verbatim — the scheduler upstream is responsible for downgrading
// it to a slog.Warn.
func (c *Client) UpsertDailyPage(ctx context.Context, briefing *DailyBriefing) error {
	if c == nil {
		return ErrClientNotConfigured
	}
	if briefing == nil {
		return errors.New("notion: briefing must not be nil")
	}
	if c.dbID == "" {
		return errors.New("notion: NOTION_DATABASE_ID is empty; cannot upsert daily page")
	}

	isoDate := briefing.Date.Format("2006-01-02")

	pageID, err := c.findDailyPageID(ctx, isoDate)
	if err != nil {
		return fmt.Errorf("notion: locating existing daily page %s: %w", isoDate, err)
	}

	properties := dailyBriefingProperties(isoDate)
	children := dailyBriefingChildren(briefing)

	if pageID == "" {
		return c.createDailyPage(ctx, properties, children)
	}
	return c.updateDailyPage(ctx, pageID, properties, children)
}

// ErrClientNotConfigured indicates UpsertDailyPage was invoked on a nil
// receiver, i.e. NOTION_INTEGRATION_SECRET was not set at startup so
// NewClient returned nil. Callers should treat this as "skip silently".
var ErrClientNotConfigured = errors.New("notion: client not configured (NOTION_INTEGRATION_SECRET unset)")

// notionDateValue is the JSON shape of a Notion Date property value.
type notionDateValue struct {
	Start string `json:"start"`
}

type notionDateProp struct {
	Date notionDateValue `json:"date"`
}

// dailyBriefingProperties builds the property payload shared by create and
// update calls. The title column always carries "Daily Briefing
// <YYYY-MM-DD>"; the Date column is the idempotency key.
func dailyBriefingProperties(isoDate string) map[string]any {
	return map[string]any{
		dailyBriefingTitleProperty: notionTitleProp{
			Title: []notionTextItem{{Text: notionTitleText{Content: "Daily Briefing " + isoDate}}},
		},
		dailyBriefingDateProperty: notionDateProp{
			Date: notionDateValue{Start: isoDate},
		},
	}
}

// findDailyPageID queries the database for any existing page where the Date
// property equals isoDate. Returns "" when no such page exists.
//
// We use the Notion `query` endpoint (POST /databases/:id/query) with a
// filter rather than scanning all pages because the database can grow
// unbounded over time.
func (c *Client) findDailyPageID(ctx context.Context, isoDate string) (string, error) {
	body := map[string]any{
		"filter": map[string]any{
			"property": dailyBriefingDateProperty,
			"date": map[string]any{
				"equals": isoDate,
			},
		},
		"page_size": 1,
	}

	var resp struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := c.do(ctx, http.MethodPost, "/databases/"+c.dbID+"/query", body, &resp); err != nil {
		return "", err
	}
	if len(resp.Results) == 0 {
		return "", nil
	}
	return resp.Results[0].ID, nil
}

func (c *Client) createDailyPage(ctx context.Context, properties map[string]any, children []map[string]any) error {
	body := map[string]any{
		"parent":     map[string]any{"database_id": c.dbID},
		"properties": properties,
		"children":   children,
	}
	return c.do(ctx, http.MethodPost, "/pages", body, nil)
}

// updateDailyPage refreshes the page properties then replaces all child
// blocks with the new briefing content. We:
//  1. PATCH /pages/:id   — rewrites title + Date property
//  2. PATCH /blocks/:id/children — appends the fresh briefing blocks
//
// Notion does not provide a "replace children" endpoint; the practical
// idempotent approach for a briefing surface is to delete the existing
// children first then append the new ones. We use the archive operation
// (PATCH each child with archived=true) — see deletePageChildren.
func (c *Client) updateDailyPage(
	ctx context.Context,
	pageID string,
	properties map[string]any,
	children []map[string]any,
) error {
	if err := c.do(ctx, http.MethodPatch, "/pages/"+pageID, map[string]any{
		"properties": properties,
	}, nil); err != nil {
		return fmt.Errorf("patching page properties: %w", err)
	}

	if err := c.deletePageChildren(ctx, pageID); err != nil {
		return fmt.Errorf("clearing existing page children: %w", err)
	}

	body := map[string]any{"children": children}
	if err := c.do(ctx, http.MethodPatch, "/blocks/"+pageID+"/children", body, nil); err != nil {
		return fmt.Errorf("appending fresh children: %w", err)
	}
	return nil
}

// deletePageChildren archives every existing child of the page so the next
// append produces a clean replacement. We page through the children list
// once (bounded by dailyBriefingMaxBlocks * 2 = ample headroom for any past
// briefing) and archive each block id with PATCH /blocks/:id.
func (c *Client) deletePageChildren(ctx context.Context, pageID string) error {
	var resp struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}

	cursor := ""
	for safety := 0; safety < 10; safety++ { // hard upper bound on pagination
		path := "/blocks/" + pageID + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return fmt.Errorf("listing children (cursor=%q): %w", cursor, err)
		}

		// Archive each block. Notion's archive flow is PATCH with archived=true.
		for _, r := range resp.Results {
			body := map[string]any{"archived": true}
			if err := c.do(ctx, http.MethodPatch, "/blocks/"+r.ID, body, nil); err != nil {
				return fmt.Errorf("archiving block %s: %w", r.ID, err)
			}
		}

		if !resp.HasMore || resp.NextCursor == "" {
			return nil
		}
		cursor = resp.NextCursor
		resp.Results = nil
	}
	return errors.New("notion: deletePageChildren exceeded pagination safety limit (10 pages)")
}

// dailyBriefingChildren renders the briefing into Notion block children.
// Layout:
//
//	## In Progress (N)
//	  - [importance] Title — context
//
//	## Past 24h Decisions (N)
//	  - Title (repo)
//
//	## Pending Proposals (N)
//	  - Type — proposed_by
//
//	## Due Reviews (N)
//	  - Title (due YYYY-MM-DD)
//
//	## System Health
//	  - Stuck tasks: N
//	  - Weekly: completed/total
func dailyBriefingChildren(b *DailyBriefing) []map[string]any {
	blocks := make([]map[string]any, 0, dailyBriefingMaxBlocks)
	blocks = appendTaskSection(blocks, b.InProgressTasks)
	if len(blocks) >= dailyBriefingMaxBlocks {
		return blocks
	}
	blocks = appendDecisionSection(blocks, b.RecentDecisions)
	if len(blocks) >= dailyBriefingMaxBlocks {
		return blocks
	}
	blocks = appendProposalSection(blocks, b.PendingProposals)
	if len(blocks) >= dailyBriefingMaxBlocks {
		return blocks
	}
	blocks = appendReviewSection(blocks, b.DueReviews)
	if len(blocks) >= dailyBriefingMaxBlocks {
		return blocks
	}
	return appendHealthSection(blocks, b.SystemHealth)
}

// appendTaskSection adds the "In Progress" heading + task bullets.
func appendTaskSection(blocks []map[string]any, tasks []TaskBlock) []map[string]any {
	blocks = append(blocks, headingBlock(fmt.Sprintf("In Progress (%d)", len(tasks))))
	if len(tasks) == 0 {
		return append(blocks, bulletBlock("(none — clear queue)"))
	}
	// Sort: importance ascending (1 = highest), then alphabetical.
	sorted := append([]TaskBlock(nil), tasks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Importance != sorted[j].Importance {
			return sorted[i].Importance < sorted[j].Importance
		}
		return sorted[i].Title < sorted[j].Title
	})
	for _, t := range sorted {
		line := fmt.Sprintf("[P%d] %s", t.Importance, t.Title)
		if t.Context != "" {
			line += " — " + t.Context
		}
		blocks = append(blocks, bulletBlock(line))
		if len(blocks) >= dailyBriefingMaxBlocks {
			return blocks
		}
	}
	return blocks
}

// appendDecisionSection adds the "Past 24h Decisions" heading + decision bullets.
func appendDecisionSection(blocks []map[string]any, decisions []DecisionBlock) []map[string]any {
	blocks = append(blocks, headingBlock(fmt.Sprintf("Past 24h Decisions (%d)", len(decisions))))
	if len(decisions) == 0 {
		return append(blocks, bulletBlock("(none)"))
	}
	for _, d := range decisions {
		line := d.Title
		if d.RepoName != "" {
			line += " (" + d.RepoName + ")"
		}
		blocks = append(blocks, bulletBlock(line))
		if len(blocks) >= dailyBriefingMaxBlocks {
			return blocks
		}
	}
	return blocks
}

// appendProposalSection adds the "Pending Proposals" heading + proposal bullets.
func appendProposalSection(blocks []map[string]any, proposals []ProposalBlock) []map[string]any {
	blocks = append(blocks, headingBlock(fmt.Sprintf("Pending Proposals (%d)", len(proposals))))
	if len(proposals) == 0 {
		return append(blocks, bulletBlock("(none)"))
	}
	for _, p := range proposals {
		line := p.Type
		if p.ProposedBy != "" {
			line += " — " + p.ProposedBy
		}
		blocks = append(blocks, bulletBlock(line))
		if len(blocks) >= dailyBriefingMaxBlocks {
			return blocks
		}
	}
	return blocks
}

// appendReviewSection adds the "Due Reviews" heading + review bullets.
func appendReviewSection(blocks []map[string]any, reviews []ReviewBlock) []map[string]any {
	blocks = append(blocks, headingBlock(fmt.Sprintf("Due Reviews (%d)", len(reviews))))
	if len(reviews) == 0 {
		return append(blocks, bulletBlock("(none)"))
	}
	for _, r := range reviews {
		line := r.Title
		if !r.DueDate.IsZero() {
			line += " (due " + r.DueDate.Format("2006-01-02") + ")"
		}
		blocks = append(blocks, bulletBlock(line))
		if len(blocks) >= dailyBriefingMaxBlocks {
			return blocks
		}
	}
	return blocks
}

// appendHealthSection adds the "System Health" heading + health summary bullets.
func appendHealthSection(blocks []map[string]any, h HealthBlock) []map[string]any {
	blocks = append(blocks, headingBlock("System Health"))
	stuck := fmt.Sprintf("Stuck tasks: %d", h.StuckTaskCount)
	if len(h.StuckTaskIDs) > 0 {
		stuck += " — " + strings.Join(h.StuckTaskIDs, ", ")
	}
	blocks = append(blocks, bulletBlock(stuck))
	return append(blocks, bulletBlock(fmt.Sprintf(
		"Weekly progress: %d completed / %d active",
		h.WeeklyCompletedTask, h.WeeklyTotalActive,
	)))
}

// headingBlock returns a heading_2 block — large enough to scan on a phone
// but smaller than the page title.
func headingBlock(text string) map[string]any {
	return map[string]any{
		"object": "block",
		"type":   "heading_2",
		"heading_2": map[string]any{
			"rich_text": []map[string]any{
				{
					"type": "text",
					"text": map[string]any{"content": truncateForNotion(text)},
				},
			},
		},
	}
}

// bulletBlock returns a bulleted_list_item block.
func bulletBlock(text string) map[string]any {
	return map[string]any{
		"object": "block",
		"type":   "bulleted_list_item",
		"bulleted_list_item": map[string]any{
			"rich_text": []map[string]any{
				{
					"type": "text",
					"text": map[string]any{"content": truncateForNotion(text)},
				},
			},
		},
	}
}

// truncateForNotion enforces Notion's 2000-character per-rich_text limit.
// Uses rune-aware truncation so multi-byte characters (CJK, emoji) are never
// split mid-sequence. Truncated strings get an ellipsis so the reader knows.
func truncateForNotion(s string) string {
	r := []rune(s)
	if len(r) <= dailyBriefingRichTextLimit {
		return s
	}
	return string(r[:dailyBriefingRichTextLimit-3]) + "..."
}
