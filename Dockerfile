FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /out/nanny-ogg ./cmd/nanny-ogg


FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/nanny-ogg /usr/local/bin/nanny-ogg
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/nanny-ogg"]
