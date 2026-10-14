FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Tanay3484/holdfast/internal/buildinfo.Version=${VERSION}" \
    -o /out/holdfast ./cmd/holdfast

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && mkdir -p /var/data
COPY --from=build /out/holdfast /usr/local/bin/holdfast
ENV PORT=8080 \
    HF_DB=/var/data/holdfast.db
EXPOSE 8080
CMD ["holdfast"]
