FROM golang:alpine AS build
ENV CGO_ENABLED=0
RUN apk add --no-cache git
COPY . /go/src/github.com/liquidweb/acronis-policy-exporter
WORKDIR /go/src/github.com/liquidweb/acronis-policy-exporter
RUN go install \
  -ldflags "-X main.version=$(git describe --tags|head -n1)" \
  github.com/liquidweb/acronis-policy-exporter

FROM alpine:latest
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /go/bin/acronis-policy-exporter /acronis-policy-exporter
ENTRYPOINT ["/acronis-policy-exporter"]