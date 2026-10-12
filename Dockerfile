# syntax=docker/dockerfile:1

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/knvb-ical ./cmd/knvb-ical

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source="https://github.com/tiesmaster/knvb-matches-to-ical" \
      org.opencontainers.image.description="KNVB match programme → iCal feed on Cloudflare R2"
COPY --from=build /out/knvb-ical /knvb-ical
USER nonroot:nonroot
ENTRYPOINT ["/knvb-ical"]
