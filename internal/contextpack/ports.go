package contextpack

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// This file declares the narrow, consumer-owned read ports Assembler
// actually calls (see retrieval.go for every call site). Each port lists
// only the methods contextpack exercises out of the corresponding domain's
// full StoreIface — e.g. gtd.StoreIface has ~25 methods and contextpack uses
// 4 of them. Production stores (*gtd.Store, the SQLite-backed twins, ...)
// already implement every one of these methods as part of their broader
// domain StoreIface, so they satisfy these ports structurally with no
// adapter/wrapper type required — Go checks interface-to-interface
// assignability by method-set superset, so callers can keep passing
// gtd.StoreIface-typed values into NewAssembler unchanged. Tests/evals fakes
// only need to implement the handful of methods actually exercised.

// TaskProjectReadPort is the subset of gtd.StoreIface that
// retrieveCurrentTaskProject and workspaceID() (retrieval.go) call.
type TaskProjectReadPort interface {
	GetTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error)
	GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error)
	ProjectsByRepoName(ctx context.Context, repoName string) ([]db.Project, error)
	WorkspaceID() pgtype.UUID
}

// DecisionReadPort is the subset of decision.StoreIface that
// retrieveDecisions (retrieval.go) calls.
type DecisionReadPort interface {
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
	ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error)
	ByTask(ctx context.Context, taskID uuid.UUID, limit int32) ([]db.Decision, error)
	// All returns the most recent decisions across every repo/project/task —
	// used only when req has no scope signal at all (RepoName == "" &&
	// ProjectID == nil && TaskID == nil). The session-start hook (A5a) is one
	// caller that hits this: it has no current-repo signal to pass — see
	// retrieveDecisions and backend-security-design.md-adjacent dispatch
	// notes on why deriveRepoSlug is deliberately not used to manufacture
	// one. It is NOT the only caller: assemble_context's MCP tool schema
	// (internal/mcp/tools_contextpack.go) marks repo_name/project_id/task_id
	// Optional — only objective is Required — so any MCP client that omits
	// all three (e.g. a bare "what have we decided" query) reaches this
	// branch too, returning workspace-wide decisions instead of the empty
	// set a caller of the pre-A5a code path would have seen. Both All()
	// implementations (Postgres and SQLite) still filter to the caller's own
	// workspace, so this is a scope-widening within one tenant, not a
	// cross-tenant leak — see TestHandleAssembleContext_UnscopedRequestReachesDecisionAll
	// (internal/mcp/tools_contextpack_test.go) for the locked-in contract.
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}

// KnowledgeReadPort is the subset of knowledge.StoreIface that
// retrieveKnowledge (retrieval.go) calls. Deliberately SearchReadOnly only —
// never Search — so assemble_context stays genuinely read-only.
type KnowledgeReadPort interface {
	SearchReadOnly(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
}

// AtomReadPort is the subset of atom.StoreIface that retrieveAtoms
// (retrieval.go) calls.
type AtomReadPort interface {
	Search(ctx context.Context, workspaceID *uuid.UUID, query string, limit int) ([]atom.Atom, error)
}

// ProceduralReadPort is the subset of procedural.StoreIface that
// retrieveProcedural (retrieval.go) calls.
type ProceduralReadPort interface {
	Query(ctx context.Context, f procedural.QueryFilter) ([]procedural.ProceduralMemory, error)
}

// SkillReadPort is the subset of skill.StoreIface that retrieveSkills
// (retrieval.go) calls.
type SkillReadPort interface {
	Search(ctx context.Context, f skill.SearchFilter) ([]*skill.Skill, error)
}

// OutcomeReadPort is the subset of outcome.StoreIface that retrieveOutcomes
// (retrieval.go) calls.
type OutcomeReadPort interface {
	ListFailedOutcomes(ctx context.Context, workspaceID *uuid.UUID, limit int) ([]outcome.Outcome, error)
}

// ReflectionReadPort is the subset of reflection.StoreIface that
// retrieveReflections (retrieval.go) calls.
type ReflectionReadPort interface {
	RecentWithPatterns(ctx context.Context, workspaceID *uuid.UUID, since time.Time, limit int) ([]*reflection.Reflection, error)
}

// BehaviorRuleReadPort is the subset of behaviorrule.StoreIface that
// retrieveBehaviorRules (retrieval.go) calls.
type BehaviorRuleReadPort interface {
	List(ctx context.Context, p behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error)
}

// SessionReadPort is the subset of session.StoreIface that retrieveSession
// (retrieval.go) calls.
type SessionReadPort interface {
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
}

// WorkSessionReadPort is the subset of worksession.StoreIface that
// retrieveCurrentTaskProject (retrieval.go) calls.
type WorkSessionReadPort interface {
	GetActive(ctx context.Context, workspaceID uuid.UUID, repoName string) (*worksession.ActiveSessionResult, error)
}
