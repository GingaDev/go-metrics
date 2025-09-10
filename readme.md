```bash
go mod tidy
go run .

curl -s localhost:8080/
curl -s localhost:8080/work
curl -s localhost:8080/metrics | head
```