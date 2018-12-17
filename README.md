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
  [`concourse/builder` task](https://github.com/concourse/builder) (or anything
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


## Behavior

### `check`: Discover new digests.

Reports the current digest that the registry has for the tag configured in
`source`.


### `in`: Fetch the image's rootfs and metadata.

Fetches an image.

#### Parameters

* `format`: *Optional. Default `rootfs`.* The format to fetch as. (See below.)

#### Formats

##### `rootfs`

The `rootfs` format will fetch and unpack the image for use by Concourse task
and resource type images.

This the default for the sake of brevity in pipelines and task configs.

In this format, the resource will produce the following files:

* `rootfs/...`: the unpacked rootfs produced by the image.
* `metadata.json`: the runtime information to propagate to Concourse.

##### `oci`

The `oci` format will fetch the image and write it to disk in OCI format. This
is analogous to running `docker save`.

In this format, the resource will produce the following files:

* `image.tar`: the OCI image tarball, suitable for passing to `docker load`.


### `out`: Push an image up to the registry under the given tags.

Uploads an image to the registry under the tag configured in `source`.

The currently encouraged way to build these images is by using the
[`concourse/builder` task](https://github.com/concourse/builder).

#### Parameters

* `image`: *Required.* The path to the OCI image tarball to upload.
