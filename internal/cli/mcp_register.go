package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// claudeCLITimeout bounds each claude mcp invocation. Five seconds is far
// longer than the CLI's normal startup; if claude is hung we'd rather skip
// registration than block setup.
const claudeCLITimeout = 5 * time.Second

// MCPTransport names the transport claude uses to reach wayneblacktea.
type MCPTransport string

const (
	// MCPTransportHTTP registers an HTTP endpoint (preferred for `wbt setup`
	// since the server runs in the background; one process serves many
	// Claude sessions).
	MCPTransportHTTP MCPTransport = "http"
	// MCPTransportStdio registers a fork-on-each-launch stdio binary.
	// Retained for backward compatibility with `wbt init`.
	MCPTransportStdio MCPTransport = "stdio"
)

// RegisterClaudeMCP runs `claude mcp remove <name>` (best-effort) then
// `claude mcp add ...` to register the wayneblacktea MCP server with the
// user's Claude Code installation at user-global scope (default; no
// --scope project).
//
// transport=http URL is the http endpoint (e.g. http://localhost:8080/mcp).
// transport=stdio invokes `wbt mcp`.
//
// If the `claude` binary is not in PATH the function prints copy-paste
// instructions to stdout and returns nil — registration is a convenience,
// not a hard requirement.
//
// The CLAUDE_BIN env var overrides the binary location for tests only.
func RegisterClaudeMCP(name, urlOrEmpty string, transport MCPTransport) error {
	bin := strings.TrimSpace(os.Getenv("CLAUDE_BIN"))
	if bin == "" {
		var err error
		bin, err = exec.LookPath("claude")
		if err != nil {
			// Intentionally swallow err: missing claude CLI is a soft failure;
			// we print copy-paste instructions and let `wbt setup` complete.
			printManualMCPInstructions(os.Stdout, name, urlOrEmpty, transport)
			return nil //nolint:nilerr // err is informational; missing claude does not block setup
		}
	}
	if err := claudeMCPRemove(bin, name); err != nil {
		// Remove failure is non-fatal: server might not exist yet.
		// We still try the add below.
		fmt.Fprintf(os.Stderr, "wbt: claude mcp remove warning: %v\n", err)
	}
	return claudeMCPAdd(bin, name, urlOrEmpty, transport)
}

// claudeMCPRemove invokes `claude mcp remove <name>`. Any error is
// returned wrapped; "server not found" exit codes are not treated specially
// because the caller logs and ignores the result.
func claudeMCPRemove(bin, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), claudeCLITimeout)
	defer cancel()
	//nolint:gosec // G204: bin from CLAUDE_BIN test override or exec.LookPath("claude"); name is server-defined literal
	cmd := exec.CommandContext(ctx, bin, "mcp", "remove", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude mcp remove %s: %w", name, err)
	}
	return nil
}

// claudeMCPAdd invokes `claude mcp add ...` for the chosen transport.
func claudeMCPAdd(bin, name, url string, transport MCPTransport) error {
	ctx, cancel := context.WithTimeout(context.Background(), claudeCLITimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch transport {
	case MCPTransportHTTP:
		if url == "" {
			return errors.New("RegisterClaudeMCP: url required for http transport")
		}
		//nolint:gosec // G204: bin via CLAUDE_BIN or exec.LookPath; name+url are server-controlled values
		cmd = exec.CommandContext(ctx, bin, "mcp", "add", name, "--transport", "http", url)
	case MCPTransportStdio:
		// `claude mcp add <name> -- wbt mcp` registers a stdio launcher.
		//nolint:gosec // G204: bin via CLAUDE_BIN or exec.LookPath; args are fixed literals
		cmd = exec.CommandContext(ctx, bin, "mcp", "add", name, "--", "wbt", "mcp")
	default:
		return fmt.Errorf("RegisterClaudeMCP: unknown transport %q", transport)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp add %s: %w (output: %s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// printManualMCPInstructions writes a friendly fallback message when the
// `claude` CLI is not installed. Returning nil means setup can still
// succeed — registration is the only missing convenience.
func printManualMCPInstructions(w io.Writer, name, url string, transport MCPTransport) {
	fmt.Fprintf(w,
		"wbt: claude CLI not found in PATH; skipping automatic MCP registration.\n"+
			"     To register manually after installing claude:\n\n",
	)
	switch transport {
	case MCPTransportHTTP:
		fmt.Fprintf(w, "       claude mcp add %s --transport http %s\n\n", name, url)
	case MCPTransportStdio:
		fmt.Fprintf(w, "       claude mcp add %s -- wbt mcp\n\n", name)
	default:
		fmt.Fprintf(w, "       (unknown transport %q)\n\n", transport)
	}
}
