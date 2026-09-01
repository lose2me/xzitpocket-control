FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/control ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/control /app/control
COPY web /app/web
COPY migrations /app/migrations
VOLUME ["/app/data"]
EXPOSE 8080
ENTRYPOINT ["/app/control"]
