ARG base_image=alpine:latest
ARG builder_image=concourse/golang-builder

FROM ${builder_image} as builder
WORKDIR /src

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
ENV CGO_ENABLED 0
RUN go build -o /assets/in ./cmd/in
RUN go build -o /assets/out ./cmd/out
RUN go build -o /assets/check ./cmd/check
RUN set -e; for pkg in $(go list ./...); do \
		go test -o "/tests/$(basename $pkg).test" -c $pkg; \
	done

FROM ${base_image} AS resource
RUN apk update && apk upgrade
RUN apk add --no-cache bash tzdata ca-certificates unzip zip gzip tar
COPY --from=builder assets/ /opt/resource/
RUN chmod +x /opt/resource/*
# Ensure /etc/hosts is honored
# https://github.com/golang/go/issues/22846
# https://github.com/gliderlabs/docker-alpine/issues/367
RUN echo "hosts: files dns" > /etc/nsswitch.conf

FROM resource AS tests
COPY --from=builder /tests /tests
ADD . /docker-image-resource
ARG DOCKER_PRIVATE_USERNAME
ARG DOCKER_PRIVATE_PASSWORD
ARG DOCKER_PRIVATE_REPO
ARG DOCKER_PUSH_USERNAME
ARG DOCKER_PUSH_PASSWORD
ARG DOCKER_PUSH_REPO
ARG GCR_PUSH_SERVICE_ACCOUNT_KEY
ARG GCR_PUSH_REPO
RUN set -e; for test in /tests/*.test; do \
		$test -ginkgo.v; \
	done

FROM resource
