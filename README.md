# Registry Image Resource

Supports checking, fetching, and (eventually) pushing of images to Docker
registries.

This resource is intended as a replacement for the [Docker Image
resource](https://github.com/concourse/docker-image-resource). Here are the key
differences:

* This resource is implemented in pure Go and does not use the Docker daemon or
  CLI.
* Therefore, it does not need to run privileged, and should be much more
  efficient. It will also be less error-prone (parsing `docker` CLI output was
  janky).
* This resource does *not* support building. This should instead be done with a
  task. Using [Kaniko](https://github.com/GoogleContainerTools/kaniko) would be
  a great idea!
* A goal of this resource is to stay as small and simple as possible. The
  Docker Image resource grew way too large and complicated. Initially, it this
  resource is designed solely to support being used to fetch images for
  Concourse containers (using `image_resource` and `resource_types`).
  * This may need to expand later on as it would be completely reasonable to
    implement `put` here, so we may need to be able to `get` in a different
    format in order to be symmetrical.
* This resource has stronger test coverage.


## Source Configuration

* `repository`: *Required.* The name of the repository, e.g. `alpine`.
* `tag`: *Optional. Default `latest`.* The name of the tag to monitor.
* `debug`: *Optional. Default `false`.* If set, progress bars will be disabled
  and debugging output will be printed instead.


## Behavior

### `check`: Discover new digests.

Reports the current digest that the registry has for the given tag.

### `in`: Fetch the image's rootfs and metadata.

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

**Not implemented yet.** Once implemented, this may take an image in a standard
format (say, whatever `docker save` does) and upload it to the registry to the
tag configured in `source`.
