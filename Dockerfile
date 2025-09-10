# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/app

FROM gcr.io/distroless/base-debian12
ENV ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
COPY --from=build /bin/app /app
ENTRYPOINT ["/app"]