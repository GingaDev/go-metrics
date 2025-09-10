```bash
go mod tidy

# Run 
go run .

# Run with log levels
# request.start (DEBUG), request.end (INFO/WARN/ERROR)
# work.simulating (DEBUG), work.done (INFO), work.failed (ERROR)
# Set log level (debug|info|warn|error) and slow threshold (ms)
export LOG_LEVEL=info
export SLOW_MS=500
LOG_FORMAT=text LOG_LEVEL=debug ADDR=":8080" go run .


curl -s localhost:8080/
curl -s localhost:8080/work
curl -s localhost:8080/metrics | head
```


Types
DEBUG: “Received request with headers {…}”
INFO: “Handled request /work in 120ms”
WARNING: “Cache miss for user=123”
ERROR: “Failed to insert record into database”
FATAL: “Could not start server: port already in use”

•	INFO when the server starts or a request is handled successfully.
•	WARNING when a non-critical issue happens (like retrying).
•	ERROR when an operation fails.
•	DEBUG only during development/testing.