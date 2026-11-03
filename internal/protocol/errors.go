package protocol

import (
	"encoding/json"
	"fmt"
)

// Error codes defined by the actor protocol.
const (
	// CodeOperationFailed means the subject under test failed. Not broken.
	CodeOperationFailed = 1000
	// CodeActorFault means the actor itself failed. Broken.
	CodeActorFault = 1001
	// CodeMethodNotFound is a standard JSON-RPC error. Broken.
	CodeMethodNotFound = -32601
	// CodeInvalidParams is a standard JSON-RPC error. Broken.
	CodeInvalidParams = -32602
)

// BrokenError indicates the actor is broken and cannot continue.
// All pending and future calls fail with this error once set.
type BrokenError struct {
	Reason string
}

func (e *BrokenError) Error() string {
	return fmt.Sprintf("actor broken: %s", e.Reason)
}

// OperationFailedError indicates the subject under test failed (code 1000).
// This is not a broken state — it is evidence about the thing under test.
type OperationFailedError struct {
	Message string
	Data    json.RawMessage
}

func (e *OperationFailedError) Error() string {
	return fmt.Sprintf("operation failed: %s", e.Message)
}
