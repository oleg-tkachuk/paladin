package cel

import (
	"strings"
	"testing"
	"time"
)

// nestedListLen and nestedDepth spell a filter of under a kilobyte whose
// evaluation, unbounded, runs nestedListLen^nestedDepth iterations — 10^8,
// several seconds on every row of a page.
const (
	nestedListLen = 100
	nestedDepth   = 4
	// costRefusalBudget is how long the refusal may take: the bound is what
	// stops the evaluation, so it ends in a sliver of the unbounded time.
	costRefusalBudget = time.Second
)

// nestedComprehension is `[0,…].all(a, [0,…].all(b, … true))`.
func nestedComprehension() string {
	list := "[" + strings.Repeat("0,", nestedListLen-1) + "0]"
	expr := "true"
	for i := range nestedDepth {
		expr = list + ".all(v" + string(rune('a'+i)) + ", " + expr + ")"
	}
	return expr
}

func TestEvaluationCostIsBounded(t *testing.T) {
	expr := nestedComprehension()
	prog, err := NewEvaluator().Compile(ObjectSchema, expr)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	start := time.Now()
	_, err = Match(prog, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "cost limit") {
		t.Fatalf("Match = %v, want the cost limit refusing it", err)
	}
	if took := time.Since(start); took > costRefusalBudget {
		t.Errorf("the refusal took %s, want under %s", took, costRefusalBudget)
	}
}

// A filter a caller actually writes stays far inside the bound.
func TestOrdinaryFilterEvaluatesUnderTheBound(t *testing.T) {
	prog, err := NewEvaluator().Compile(ObjectSchema, `state == "AVAILABLE" && size_bytes > 0 && tags.exists(k, k.startsWith("env"))`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	row := map[string]any{"state": "AVAILABLE", "size_bytes": int64(1), "tags": map[string]string{"env": "prod", "tier": "gold"}}
	if ok, err := Match(prog, row); err != nil || !ok {
		t.Fatalf("Match = (%v, %v), want a match", ok, err)
	}
}
