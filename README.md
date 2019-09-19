# Registry Image Resource

Supports checking, fetching, and pushing of images to Docker registries.

This resource is intended as a replacement for the [Docker Image
resource](https://github.com/concourse/docker-image-resource). Here are the key
differences:

* This resource is implemented in pure Go and does not use the Docker daemon or
  CLI. This makes it safer (no need for `privileged: true`), more efficient,
  and less error-prone (now that we're using Go APIs and not parsing `docker`
  CLI output).

* This resource has stronger test coverage.

* This resource does not and will never support building - only registry image
  pushing/pulling. Building should instead be done with something like the
  [`builder` task](https://github.com/concourse/builder-task) (or anything
  that can produce OCI image tarballs).

* A goal of this resource is to stay as focused and simple as possible. The
  Docker Image resource grew way too large and complicated. There are simply
  too many ways to build and publish Docker images. It will be easier to
  support many smaller resources + tasks rather than one huge interface.


## Source Configuration

* `repository`: *Required.* The name of the repository, e.g. `alpine`.

* `tag`: *Optional. Default `latest`.* The name of the tag to monitor and
  publish to.

* `username` and `password`: *Optional.* A username and password to use when
  authenticating to the registry. Must be specified for private repos or when
  using `put`.

* `debug`: *Optional. Default `false`.* If set, progress bars will be disabled
  and debugging output will be printed instead.

* `content_trust`: *Optional.* Configuration about content trust.
  * `server`: *Optional.* URL for the notary server. (equal to `DOCKER_CONTENT_TRUST_SERVER`)
  * `repository_key_id`: *Required.* Target key's ID used to sign the trusted collection, could be retrieved by `notary key list`
  * `repository_key`: *Required.* Target key used to sign the trusted collection.
  * `repository_passphrase`: *Required.* The passphrase of the signing/target key. (equal to `DOCKER_CONTENT_TRUST_REPOSITORY_PASSPHRASE`)
  * `tls_key`: *Optional. Default `""`* TLS key for the notary server.
  * `tls_cert`: *Optional. Default `""`* TLS certificate for the notary server.

## Behavior

### `check`: Discover new digests.

Reports the current digest that the registry has for the tag configured in
`source`.


### `in`: Fetch the image's rootfs and metadata.

Fetches an image at a digest.

#### Parameters

* `format`: *Optional. Default `rootfs`.* The format to fetch as.

#### Files created by the resource

The resource will produce the following files:

* `./digest`: A file containing the image's digest, e.g. `sha256:...`.
* `./tag`: A file containing the tag from `source`, e.g. `latest`.

The remaining files depend on the configuration value for `format`:

##### `rootfs`

The `rootfs` format will fetch and unpack the image for use by Concourse task
and resource type images.

This the default for the sake of brevity in pipelines and task configs.

In this format, the resource will produce the following files:

* `./rootfs/...`: the unpacked rootfs produced by the image.
* `./metadata.json`: the runtime information to propagate to Concourse.

##### `oci`

The `oci` format will fetch the image and write it to disk in OCI format. This
is analogous to running `docker save`.

In this format, the resource will produce the following files:

* `./image.tar`: the OCI image tarball, suitable for passing to `docker load`.


### `out`: Push an image up to the registry under the given tags.

Uploads an image to the registry under the tag configured in `source`.
 
If `additional_tags` param is defined then the uploaded image will also be 
tagged with each one of the values specified in that file.

The currently encouraged way to build these images is by using the
[`concourse/builder` task](https://github.com/concourse/builder).

#### Parameters

* `image`: *Required.* The path to the OCI image tarball to upload.
* `additional_tags`: *Optional.* The path to a file with whitespace-separated 
list of tag values to tag the image with (in addition to the tag configured in 
`source`).

## Development

### Prerequisites

* golang is *required* - version 1.11.x or above is required for go mod to work
* docker is *required* - version 17.06.x is tested; earlier versions may also
  work.
* go mod is used for dependency management of the golang packages.

### Running the tests

The tests have been embedded with the `Dockerfile`; ensuring that the testing
environment is consistent across any `docker` enabled platform. When the docker
image builds, the test are run inside the docker container, on failure they
will stop the build.

Run the tests with the following commands for both `alpine` and `ubuntu` images:

```sh
docker build -t registry-image-resource -f dockerfiles/alpine/Dockerfile .
docker build -t registry-image-resource -f dockerfiles/ubuntu/Dockerfile .
```

#### Integration tests

The integration requires 2 docker repos, one private and one public. The `docker build` 
step requires setting `--build-args` so the integration will run.

Run the tests with the following command:

```sh
docker build . -t registry-image-resource -f dockerfiles/alpine/Dockerfile \
  --build-arg DOCKER_PRIVATE_USERNAME="some-username" \
  --build-arg DOCKER_PRIVATE_PASSWORD="some-password" \
  --build-arg DOCKER_PRIVATE_REPO="some/repo" \
  --build-arg DOCKER_PUSH_USERNAME="some-username" \
  --build-arg DOCKER_PUSH_PASSWORD="some-password" \
  --build-arg DOCKER_PUSH_REPO="some/repo"

docker build . -t registry-image-resource -f dockerfiles/ubuntu/Dockerfile \
  --build-arg DOCKER_PRIVATE_USERNAME="some-username" \
  --build-arg DOCKER_PRIVATE_PASSWORD="some-password" \
  --build-arg DOCKER_PRIVATE_REPO="some/repo" \
  --build-arg DOCKER_PUSH_USERNAME="some-username" \
  --build-arg DOCKER_PUSH_PASSWORD="some-password" \
  --build-arg DOCKER_PUSH_REPO="some/repo"
```

### Contributing

Please make all pull requests to the `master` branch and ensure tests pass
locally.
