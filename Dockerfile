FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/reviewd ./cmd/reviewd

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/reviewd /reviewd
COPY static ./static
COPY annotate.js ./annotate.js
EXPOSE 8080
ENTRYPOINT ["/reviewd"]
