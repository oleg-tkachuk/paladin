package mcp

import mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

// catalogMutates is each tool's Mutates flag, from DefaultCatalog.
var catalogMutates = func() map[string]bool {
	m := make(map[string]bool, len(DefaultCatalog))
	for _, t := range DefaultCatalog {
		m[t.Name] = t.Mutates
	}
	return m
}()

// annotate fills in the hints an MCP client decides confirmations by, from
// the catalog rather than per tool. Without them a client must assume the
// spec's defaults — every tool destructive, open-world, not idempotent — and
// ask before each read. A hint the registration set by hand is kept.
func annotate(tool *mcpsdk.Tool) {
	if tool.Annotations == nil {
		tool.Annotations = &mcpsdk.ToolAnnotations{}
	}
	a := tool.Annotations
	mutates := catalogMutates[tool.Name]
	a.ReadOnlyHint = !mutates
	if a.OpenWorldHint == nil {
		// Every tool acts on this platform's own state.
		a.OpenWorldHint = ptrFalse()
	}
	if !mutates {
		a.IdempotentHint = true
	} else if a.DestructiveHint == nil {
		// The spec reads a missing hint as destructive; the tools that are
		// say so at registration.
		a.DestructiveHint = ptrFalse()
	}
}

func ptrFalse() *bool { v := false; return &v }
