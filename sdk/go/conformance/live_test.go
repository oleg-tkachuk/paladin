//go:build sdkconformance

package conformance

import (
	"os"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The environment the stack gate sets.
const (
	envDataURL    = "PALADIN_SDK_DATA_URL"
	envToken      = "PALADIN_SDK_TOKEN"
	envCollection = "PALADIN_SDK_COLLECTION"
)

func setup(t *testing.T) env {
	t.Helper()
	url, token, collection := os.Getenv(envDataURL), os.Getenv(envToken), os.Getenv(envCollection)
	if url == "" || token == "" || collection == "" {
		t.Fatalf("set %s, %s and %s: under the sdkconformance tag a missing server is a failure, not a skip",
			envDataURL, envToken, envCollection)
	}
	name, err := paladin.ParseCollectionName(collection)
	if err != nil {
		t.Fatal(err)
	}
	p, err := paladin.Connect(paladin.Endpoints{Data: url}, paladin.WithTokens(paladin.StaticToken(token)))
	if err != nil {
		t.Fatal(err)
	}
	return env{data: p.Data, collection: name}
}

func TestScenarios(t *testing.T) {
	e := setup(t)
	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) { run(t, e) })
	}
}
