// Developer tools the repository runs with `go tool -modfile=tools/go.mod`,
// pinned here so a task does not depend on what a host has installed.
module github.com/oleg-tkachuk/paladin/tools

go 1.27.0

tool (
	github.com/unclebob/crap4go/cmd/crap4go
	github.com/unclebob/dry4go/cmd/dry4go
	github.com/unclebob/mutate4go/cmd/mutate4go
)

require (
	github.com/unclebob/crap4go v0.0.0-20260521190544-bee16dbdadb4 // indirect
	github.com/unclebob/dry4go v0.0.0-20260511205932-78f4b4122b62 // indirect
	github.com/unclebob/mutate4go v0.0.0-20260523153956-9016c7adafc1 // indirect
)
