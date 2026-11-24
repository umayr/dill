package orchestrator

import "golang.org/x/sync/semaphore"

// Bound registry transfers across concurrent operations in this process.
// Waiting must honor cancellation before contacting an engine.
var pullSlots = semaphore.NewWeighted(2)
