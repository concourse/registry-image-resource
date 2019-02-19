FROM concourse/golang-builder as builder
COPY . /src
WORKDIR /src
ENV CGO_ENABLED 0
RUN go get -d ./...
RUN go build -o /assets/in ./cmd/in
RUN go build -o /assets/out ./cmd/out
RUN go build -o /assets/check ./cmd/check
RUN set -e; for pkg in $(go list ./...); do \
		go test -o "/tests/$(basename $pkg).test" -c $pkg; \
	done

FROM ubuntu:bionic AS resource
RUN apt-get update \
      && apt-get install -y --no-install-recommends \
        tzdata \
        ca-certificates \
        unzip \
        zip \
      && rm -rf /var/lib/apt/lists/*
COPY --from=builder assets/ /opt/resource/
RUN chmod +x /opt/resource/*

FROM resource AS tests
COPY --from=builder /tests /tests
ADD . /docker-image-resource
ARG DOCKER_PRIVATE_USERNAME
ARG DOCKER_PRIVATE_PASSWORD
ARG DOCKER_PRIVATE_REPO
ARG DOCKER_PUSH_USERNAME
ARG DOCKER_PUSH_PASSWORD
ARG DOCKER_PUSH_REPO
RUN set -e; for test in /tests/*.test; do \
		$test -ginkgo.v; \
	done

FROM resource
