package gtd

import "context"

// WithUpdateTaskStatusGuardedTestHook attaches hook to ctx so a subsequent
// UpdateTaskStatusGuarded call runs it synchronously between its pre-read
// and its guarded SQL UPDATE — see updateTaskStatusGuardedHookKeyType's doc
// comment (store.go) for why this is the only deterministic way to exercise
// [F0930-07]'s reread-branch status-vs-assignee ordering fix. File ends in
// _test.go so none of this ships in the production binary (same
// export_test.go pattern as internal/storage/sqlite/export_test.go).
func WithUpdateTaskStatusGuardedTestHook(ctx context.Context, hook func()) context.Context {
	return context.WithValue(ctx, updateTaskStatusGuardedHookKey, hook)
}
