package mcp

import mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

// catalogHints is each tool's catalog row, keyed by name.
var catalogHints = func() map[string]ToolMeta {
	m := make(map[string]ToolMeta, len(DefaultCatalog))
	for _, t := range DefaultCatalog {
		m[t.Name] = t
	}
	return m
}()

// annotate sets the hints an MCP client decides confirmations by, from the
// catalog and nowhere else. Without them a client must assume the spec's
// defaults — every tool destructive, open-world, not idempotent — and ask
// before each read. The tool gets annotations of its own: registrations share
// none, so one tool's hints cannot leak into another's.
func annotate(tool *mcpsdk.Tool) {
	meta := catalogHints[tool.Name]
	a := &mcpsdk.ToolAnnotations{
		ReadOnlyHint:  !meta.Mutates,
		OpenWorldHint: ptrBool(meta.OpenWorld),
	}
	if meta.Mutates {
		// The spec reads a missing hint as destructive, so an additive tool
		// says it is not.
		a.DestructiveHint = ptrBool(meta.Destructive)
	} else {
		a.IdempotentHint = true
	}
	tool.Annotations = a
}

func ptrBool(v bool) *bool { return &v }
