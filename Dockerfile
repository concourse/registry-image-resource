FROM golang:alpine as builder
COPY . /go/src/github.com/concourse/registry-image-resource
ENV CGO_ENABLED 0
RUN go build -o /assets/in github.com/concourse/registry-image-resource/cmd/in
RUN go build -o /assets/out github.com/concourse/registry-image-resource/cmd/out
RUN go build -o /assets/check github.com/concourse/registry-image-resource/cmd/check
WORKDIR /go/src/github.com/concourse/registry-image-resource
RUN set -e; for pkg in $(go list ./...); do \
		go test -o "/tests/$(basename $pkg).test" -c $pkg; \
	done

FROM alpine:edge AS resource
RUN apk add --no-cache bash tzdata ca-certificates unzip zip gzip tar
COPY --from=builder assets/ /opt/resource/
RUN chmod +x /opt/resource/*

FROM resource
