package handler

// Kind strings used by the frontend event colour system (eventStyles.ts).
const (
	kindActivity        = "activity"
	kindDecision        = "decision"
	kindTaskCompleted   = "task_completed"
	kindTaskCreated     = "task_created"
	kindHandoffCreated  = "handoff_created"
	kindHandoffResolved = "handoff_resolved"
)

// classifyActivityKind maps a raw activity_log.action string to a stable
// "kind" the frontend uses for colour coding via eventStyles.ts. Keep the
// mapping conservative — anything we don't recognise falls back to the
// neutral "activity" tier.
func classifyActivityKind(action string) string {
	switch action {
	case "task_completed", "complete_task", "task:completed":
		return kindTaskCompleted
	case "task_created", "add_task", "task:added":
		return kindTaskCreated
	case "task:begin":
		return kindTaskCreated
	case "task:updated":
		return kindActivity
	case "log_decision", "decision", "decision:logged":
		return kindDecision
	case "set_session_handoff", "session:handoff":
		return kindHandoffCreated
	case "resolve_handoff":
		return kindHandoffResolved
	case "plan:confirmed":
		return kindDecision
	case "worksession:started", "worksession:finished", "worksession:checkpointed":
		return kindActivity
	case "dashboard:reconciled":
		return kindActivity
	default:
		return kindActivity
	}
}
