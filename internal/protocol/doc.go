// Package protocol implements JSON-RPC 2.0 over stdio: framing, types and error codes.
//
// The protocol is newline-delimited JSON-RPC 2.0 over a child process's stdin
// (requests to the actor) and stdout (responses and notifications from the actor).
package protocol
