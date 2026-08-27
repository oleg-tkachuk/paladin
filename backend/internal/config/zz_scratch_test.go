package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	goyaml "gopkg.in/yaml.v3"
)

func TestScratchDiff(t *testing.T) {
	body, _ := os.ReadFile("../../deploy/chart/values.yaml")
	var wrap struct {
		Config map[string]any `yaml:"config"`
	}
	_ = goyaml.Unmarshal(body, &wrap)
	chart := map[string]bool{}
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			chart[p] = true
			if sub, ok := v.(map[string]any); ok {
				walk(sub, p)
			}
		}
	}
	walk(wrap.Config, "")
	g, _ := os.ReadFile(goldenPath)
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(g)), "\n") {
		if line != "" && !chart[line] {
			missing = append(missing, line)
		}
	}
	sort.Strings(missing)
	fmt.Println("MISSING:", len(missing))
	for _, m := range missing {
		fmt.Println(" ", m)
	}
}
