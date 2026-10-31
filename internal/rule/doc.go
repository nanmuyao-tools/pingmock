// Package rule defines how ghostwire answers a ping, and how that is written.
//
// A Spec says how to answer one request: after which Delay (plus or minus
// Jitter), whether to drop it (Loss, or a deterministic Pattern) or to
// answer with destination Unreachable instead of a reply, and with which
// TTL. Its text form is what users write after -rule:
//
//	120ms
//	delay=200ms,jitter=30ms,loss=5%
//	delay=10ms,pattern=..u
//	unreachable=admin
//	timeout
//	delay=30ms,ttl=128
//
// A Scenario is a list of Phases, each a Spec that lasts for a duration or
// a number of requests, so that a rule can change over time:
//
//	30ms for 10 pings; timeout for 5s; 30ms
//
// A plain Spec is a Scenario with one phase that never ends. Decide turns a
// Spec into the Outcome of one request; package engine keeps track of
// which phase each target is in.
//
// The files are:
//
//	outcome.go   Outcome (reply, drop, unreachable) and UnreachCode
//	spec.go      Spec: its text form and Decide
//	scenario.go  Scenario and Phase: their text form
//	json.go      the JSON forms of both, for the API and configuration files
//	target.go    ParsePrefix, for the addresses rules apply to
package rule
