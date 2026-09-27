# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.25-alpine AS build
WORKDIR /src

# Download modules first so they're cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Pure-Go SQLite, so no C toolchain is needed (CGO off → fully static binary).
# timetzdata embeds the timezone database so the TZ variable works in the
# minimal runtime image.
RUN CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/attherack . \
 && mkdir -p /out/data

# ---- run ----
# distroless/static: no shell or package manager, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/attherack /app/attherack
# The data directory must be owned by the nonroot user (uid 65532) so a new
# named volume inherits writable permissions.
COPY --from=build --chown=65532:65532 /out/data /data

# Inside the container, listen on all interfaces; control who can reach it
# when you publish the port (see README). The database lives on the volume.
ENV ADDR=:8080 \
    DB_PATH=/data/attherack.db \
    TZ=UTC

WORKDIR /data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/attherack"]
