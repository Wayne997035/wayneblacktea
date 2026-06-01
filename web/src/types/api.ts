export type ProjectStatus = 'active' | 'on_hold' | 'completed' | 'archived';
export type TaskStatus    = 'pending' | 'in_progress' | 'completed' | 'cancelled';
export type GoalStatus    = 'active' | 'completed' | 'archived';

export interface Project {
  id: string;
  goal_id?: string | null;
  name: string;
  title: string;
  description?: string | null;
  status: ProjectStatus;
  area: string;
  priority: 1 | 2 | 3 | 4 | 5;
  created_at: string;
  updated_at: string;
}

export interface Task {
  id: string;
  project_id?: string | null;
  title: string;
  description?: string | null;
  status: TaskStatus;
  priority: 1 | 2 | 3 | 4 | 5;
  importance?: number | null;
  assignee?: string | null;
  due_date?: string | null;
  artifact?: string | null;
  created_at: string;
  updated_at: string;
  /** Git branch name associated with this task (migration 000047). */
  branch_name?: string | null;
  /** GitHub PR URL for this task (migration 000047). */
  pr_url?: string | null;
  /** Commit SHAs appended via complete_task or update_task (migration 000047). */
  commit_shas?: string[];
}

export interface Goal {
  id: string;
  title: string;
  description?: string | null;
  status: GoalStatus;
  area?: string | null;
  due_date?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Repo {
  id: string;
  name: string;
  path?: string | null;
  description?: string | null;
  language?: string | null;
  status: string;
  current_branch?: string | null;
  known_issues: string[] | null;
  next_planned_step?: string | null;
  last_activity?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Decision {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  title: string;
  context: string;
  decision: string;
  rationale: string;
  alternatives?: string | null;
  created_at: string;
}

export interface NextAction {
  step: number;
  title: string;
  command?: string;
  expected?: string;
  status: 'pending' | 'done' | 'skipped';
  ref_task_id?: string | null;
}

export interface SessionHandoff {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  intent: string;
  context_summary?: string | null;
  resolved_at?: string | null;
  created_at: string;
  /** Structured next-action checklist (migration 000046). */
  next_actions?: NextAction[];
}

export interface WeeklyProgress {
  completed: number;
  total: number;
}

export interface TodayContext {
  goals: Goal[] | null;
  projects: Project[] | null;
  weekly_progress: WeeklyProgress;
  pending_handoff: SessionHandoff | null;
}

export interface CreateTaskRequest {
  title: string;
  project_id?: string | null;
  priority: 1 | 2 | 3 | 4 | 5;
  due_date?: string | null;
  description?: string | null;
  assignee?: string | null;
  branch_name?: string | null;
  pr_url?: string | null;
  context?: string | null;
  importance?: number | null;
}

export interface DueReview {
  concept_id: string;
  schedule_id: string;
  title: string;
  content: string;
  stability: number;
  difficulty: number;
  due_date: string;
  review_count: number;
}

export interface SubmitReviewRequest {
  rating: 1 | 2 | 3 | 4;
  stability: number;
  difficulty: number;
  review_count: number;
}

export interface CreateConceptRequest {
  title: string;
  content: string;
  tags: string[];
}

export type KnowledgeType = 'article' | 'til' | 'bookmark' | 'zettelkasten';

export interface KnowledgeItem {
  id: string;
  type: KnowledgeType;
  title: string;
  content: string;
  url: string | null;
  tags: string[];
  created_at: string;
  updated_at: string;
  source: string;
  learning_value: number | null;
}

export interface CreateKnowledgeRequest {
  type: KnowledgeType;
  title: string;
  content: string;
  url?: string | null;
  tags?: string[];
  source?: string;
  learning_value?: number | null;
}

export interface LearningSuggestion {
  id: string;
  title: string;
  content: string;
  tags: string[];
  learning_value?: number;
  context?: string;
}

export interface LearningSuggestions {
  knowledge_items: LearningSuggestion[];
  decisions: LearningSuggestion[];
}

export interface SearchResult {
  type: 'knowledge' | 'decision' | 'task' | 'project';
  id: string;
  title: string;
  /** Backend may return either 'snippet' or 'content'. useGlobalSearch normalises to snippet. */
  snippet: string;
  /** content is the legacy field name kept for backward compat with older backend responses. */
  content?: string;
  url: string;
  score?: number | null;
}

export interface SearchResponse {
  query: string;
  results: SearchResult[];
}

export type ProposalType = 'goal' | 'project' | 'task' | 'concept' | 'knowledge' | 'decision'
export type ProposalStatus = 'pending' | 'accepted' | 'rejected'

export interface ConceptCandidatePayload {
  title: string
  content: string
  tags?: string[]
  source_item_id?: string
  source_item_type?: string
}

export interface KnowledgePayload {
  title: string
  content: string
  tags?: string[]
  source_item_id?: string
  source_item_type?: string
}

export interface PendingProposal {
  id: string
  type: ProposalType
  status: ProposalStatus
  payload: ConceptCandidatePayload | KnowledgePayload
  proposed_by: string | null
  created_at: string
  resolved_at: string | null
}

export interface ResolveProposalRequest {
  action: 'accept' | 'reject'
}

// --- Learning History ---

export type ConceptLearningStatus = 'new' | 'learning' | 'reviewing' | 'mastered'

export interface ConceptHistoryItem {
  id: string
  title: string
  tags: string[]
  review_count: number
  first_reviewed_at?: string | null
  last_reviewed_at?: string | null
  interval_days: number
  next_review_at?: string | null
  status: ConceptLearningStatus
}

export interface LearningStats {
  total_reviews: number
  reviews_7d: number
  mastered: number
  total_concepts: number
  streak_days: number
}

// --- Dashboard Stats ---

export interface DashboardStats {
  period: string
  task_completed: number
  task_total: number
  decision_count: number
  pending_proposals: number
  last_updated_at?: string
}

// --- Automation Feed ---

export interface AutomationFeedItem {
  id: string
  action: string
  kind: string
  notes?: string
  occurred_at: string
}

export interface AutomationFeedResponse {
  feed: AutomationFeedItem[]
  fetched_at: string
}

// --- Automation Health (extended) ---

export interface AutomationHealth {
  stale_in_progress: unknown[]
  completion_candidates: unknown[]
  pending_proposals_count: number
  missing_handoff: boolean
  last_reconciled_at?: string
  stale_badge: boolean
}

// --- Timeline (Calendar) ---

export type TimelineKind =
  | 'task_created'
  | 'task_completed'
  | 'task_due'
  | 'decision'
  | 'activity'
  | 'knowledge'
  | 'concept'
  | 'review_submitted'
  | 'handoff_created'
  | 'handoff_resolved'

export interface TimelineEvent {
  kind: TimelineKind
  /** RFC3339 UTC timestamp. */
  occurred_at: string
  ref_id: string
  title: string
  repo_name?: string
  project_id?: string
}

export interface TimelineResponse {
  events: TimelineEvent[]
  /** RFC3339 timestamp echoed by the backend. */
  from: string
  to: string
}

// --- Vision ---

export type VisionStatus = 'open' | 'discussing' | 'maturing' | 'promoted' | 'dismissed'

export interface VisionItem {
  id: string
  workspace_id?: string | null
  repo_name?: string
  project_id?: string | null
  title: string
  why_blocked: string
  depends_on: string[]
  parent_initiative?: string
  status: VisionStatus
  context_md?: string
  promoted_task_id?: string | null
  last_discussed_at?: string | null
  created_at: string
}

export interface VisionItemSummary {
  id: string
  workspace_id?: string | null
  repo_name?: string
  project_id?: string | null
  title: string
  why_blocked: string
  depends_on: string[]
  parent_initiative?: string
  status: VisionStatus
  promoted_task_id?: string | null
  last_discussed_at?: string | null
  created_at: string
}

export interface CreateVisionRequest {
  title: string
  why_blocked: string
  depends_on?: string[]
  parent_initiative?: string
  context_md?: string
  repo_name?: string
}

export interface UpdateVisionRequest {
  status?: VisionStatus
  context_md?: string
  last_discussed_at?: string | null
}

// --- Repo Detail / Workspace Overview ---

export interface RepoOverviewSummary {
  id: string
  name: string
  description?: string
  language?: string
  status: string
  current_branch?: string
  next_planned_step?: string
  last_activity?: string
  path?: string
  known_issues: string[]
}

export interface RepoOverviewCompletedTask {
  id: string
  title: string
  completed_at?: string
  project_id?: string
  artifact?: string
}

export interface RepoOverviewPendingTask {
  id: string
  title: string
  status: string
  importance?: number
  priority: number
  due_date?: string
  project_id?: string
}

export interface RepoOverviewDecision {
  id: string
  title: string
  decision: string
  rationale: string
  created_at?: string
}

export interface RepoOverviewActivity {
  id: string
  action: string
  notes?: string
  created_at?: string
  /** Mapped to a TimelineKind by the backend so the UI can colour-code via eventStyles.ts. */
  kind: string
}

export interface RepoOverviewHandoff {
  id: string
  intent: string
  /** "open" or "resolved". */
  status: string
  created_at?: string
  resolved_at?: string
}

export interface RepoOverview {
  repo: RepoOverviewSummary
  completed_tasks: RepoOverviewCompletedTask[]
  pending_tasks: RepoOverviewPendingTask[]
  recent_decisions: RepoOverviewDecision[]
  recent_activity: RepoOverviewActivity[]
  recent_handoffs: RepoOverviewHandoff[]
}

/** Per-workspace AI model preference (GET/PATCH /api/workspace/settings). */
export interface WorkspaceSettings {
  model_preference: AllowedModel
}

/**
 * Selectable AI models. Mirrors the server's workspace.AllowedModels
 * (internal/workspace/preference.go) — keep the two lists in sync.
 */
export const ALLOWED_MODELS = [
  'claude-haiku-4-5',
  'claude-sonnet-4-6',
  'claude-opus-4-8',
] as const

export type AllowedModel = (typeof ALLOWED_MODELS)[number]
