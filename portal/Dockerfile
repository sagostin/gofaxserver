# Multi-stage build for the fax portal (frontend + backend in one image).
# Build from the REPO ROOT:  docker build -f portal/Dockerfile .
FROM node:20-alpine AS ui
WORKDIR /src/frontend
COPY portal/frontend/package.json portal/frontend/package-lock.json* ./
RUN npm install --no-fund --no-audit
COPY portal/frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src/portal
COPY portal/go.mod portal/go.sum ./
RUN go mod download || true
COPY portal/ ./
COPY --from=ui /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -o /out/gofaxportal ./cmd/portal

FROM alpine:3.20
# ghostscript: fax-accurate B&W previews of prepared documents.
# imagemagick: tiff → PDF normalization (multi-page CCITT fax TIFFs).
# ttf-dejavu: fallback fonts so gs can render PDFs with non-embedded fonts.
# docx/doc conversion is NOT here — that's the Gotenberg sidecar (compose).
RUN adduser -D -u 10001 appuser && \
    apk add --no-cache ca-certificates tzdata ghostscript imagemagick ttf-dejavu
COPY --from=build /out/gofaxportal /usr/local/bin/gofaxportal
USER appuser
EXPOSE 8081
ENTRYPOINT ["gofaxportal"]
CMD ["-c", "/etc/gofaxportal/config.json"]
