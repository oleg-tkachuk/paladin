package apiutil

// RegisteredSentinels is every sentinel MapError resolves: the canonical ones
// and every one a linked package registered.
func RegisteredSentinels() []error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]error, 0, len(canonical)+len(registry))
	for s := range canonical {
		out = append(out, s)
	}
	for s := range registry {
		out = append(out, s)
	}
	return out
}
