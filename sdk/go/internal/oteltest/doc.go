// Package oteltest proves OpenTelemetry plugs into the SDK through the
// extension points it already has. It is a module of its own so that the SDK's
// go.mod requires no OpenTelemetry module: a published module's requirements
// become its consumers', and a test's would raise their OpenTelemetry floor
// (ADR-0043).
package oteltest
