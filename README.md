# Registry Image Resource

Supports checking, fetching, and pushing of images to Docker registries.
This resource can be used in two ways: [with `tag`
specified](#check-with-tag-discover-new-digests-for-the-tag) and [without
`tag`](#check-without-tag-discover-semver-tags).

With `tag` specified, `check` will detect changes to the digest the tag points
to, and `out` will always push to the specified tag. This is to be used in
simpler cases where no real versioning exists.

With `tag` omitted, `check` will instead detect tags based on semver versions
(e.g. `1.2.3`) and return them in semver order. With `variant` included,
`check` will only detect semver tags that include the variant suffix (e.g.
`1.2.3-stretch`).

## Comparison to `docker-image` resource

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
  [`oci-build` task](https://github.com/vito/oci-build-task) (or anything
  that can produce OCI image tarballs).

* A goal of this resource is to stay as focused and simple as possible. The
  Docker Image resource grew way too large and complicated. There are simply
  too many ways to build and publish Docker images. It will be easier to
  support many smaller resources + tasks rather than one huge interface.


## Source Configuration

* `repository`: *Required.* The name of the repository, e.g. `alpine` or
  `concourse/concourse`.

  *Note: If using ecr you only need the repository name, not the full URI e.g.
  `alpine` not `012345678910.dkr.ecr.us-east-1.amazonaws.com/alpine`*

* `insecure`: *Optional. Default `false`* Allow insecure registry.

* `tag`: *Optional.* Instead of monitoring semver tags, monitor a single tag
  for changes (based on digest).

* `variant`: *Optional.* Detect only tags matching this variant suffix, and
  push version tags with this suffix applied. For example, a value of `stretch`
  would be used for tags like `1.2.3-stretch`. This is typically used *without*
  `tag` - if it is set, this value will only used for pushing, not checking.

* `semver_constraint`: *Optional.* Constrain the returned semver tags according
  to a semver constraint, e.g. `"~1.2.x"`, `">= 1.2 < 3.0.0 || >= 4.2.3"`.
  Follows the rules outlined in https://github.com/Masterminds/semver#checking-version-constraints.

* `username` and `password`: *Optional.* A username and password to use when
  authenticating to the registry. Must be specified for private repos or when
  using `put`.

* `aws_access_key_id`: *Optional. Default `""`.* The access key ID to use for
  authenticating with ECR.

* `aws_secret_access_key`: *Optional. Default `""`.* The secret access key to
  use for authenticating with ECR.

* `aws_session_token`: *Optional. Default `""`.* The session token to use
  for authenticating with STS credentials with ECR.

* `aws_region`: *Optional. Default `""`.* The region to use for accessing ECR. This is required if you are using ECR. This region will help determine the full repository URL you are accessing (e.g., `012345678910.dkr.ecr.us-east-1.amazonaws.com`)

* `aws_role_arn`: *Optional. Default `""`.* If set, then this role will be
   assumed before authenticating to ECR. An error will occur if `aws_role_arns`
   is also specified. This is kept for backward compatibility.

* `aws_role_arns`: *Optional. Default `""`.* An array of AWS IAM roles.
  If set, these roles will be assumed in the specified order before authenticating to ECR.
  An error will occur if `aws_role_arn` is also specified.

* `debug`: *Optional. Default `false`.* If set, progress bars will be disabled
  and debugging output will be printed instead.

* `registry_mirror`: *Optional.*
  * `host`: *Required.* A hostname pointing to a Docker registry mirror service. Note that this is only used if no registry hostname prefix is specified in the `repository`. If the `repository` contains a registry hostname prefix -- such as `my-registry.com/foo/bar` -- the `registry_mirror` is ignored and the explicitly declared registry in the `repository` value is used.
  * `username` and `password`: *Optional.* A username and password to use when
  authenticating to the mirror.

* `content_trust`: *Optional.* Configuration about content trust.
  * `server`: *Optional.* URL for the notary server. (equal to `DOCKER_CONTENT_TRUST_SERVER`)
  * `repository_key_id`: *Required.* Target key's ID used to sign the trusted collection, could be retrieved by `notary key list`
  * `repository_key`: *Required.* Target key used to sign the trusted collection.
  * `repository_passphrase`: *Required.* The passphrase of the signing/target key. (equal to `DOCKER_CONTENT_TRUST_REPOSITORY_PASSPHRASE`)
  * `tls_key`: *Optional. Default `""`* TLS key for the notary server.
  * `tls_cert`: *Optional. Default `""`* TLS certificate for the notary server.

* `ca_certs`: *Optional.* An array of PEM-encoded CA certificates:

  ```yaml
  ca_certs:
  - |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
  - |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
  ```

  Each entry specifies the x509 CA certificate for the trusted docker registry.
  This is used to validate the certificate of the docker registry when the
  registry's certificate is signed by a custom authority (or itself).

### Signing with Docker Hub

Configure Docker Content Trust for use with the [Docker Hub](https:/hub.docker.io) and Notary service by specifying the above source parameters as follows:

* `repository_key` should be set to the contents of the DCT key file located in your ~/.docker/trust/private directory.
* `repository_key_id` should be set to the full key itself, which is also the filename of the key file mentioned above, without the .key extension.

Consider the following resource:

```yaml
resources:
- name: trusted-image
  type: registry-image
  source:
    repository: docker.io/foo/bar
    username: ((registry_user))
    password: ((registry_pass))
    content_trust:
      repository_key_id: ((registry_key_id))
      repository_key: ((registry_key))
      repository_passphrase: ((registry_passphrase))
```

Specify the values for these variables as shown in the following static variable file, or preferrably in a configured [credential manager](https://concourse-ci.org/creds.html):

```yaml
registry_user: jertel
registry_pass: my_docker_hub_token
registry_passphrase: my_dct_key_passphrase
registry_key_id: 1452a842871e529ffc2be29a012618e1b2a0e6984a89e92e34b5a0fc21a04cd
registry_key: |
  -----BEGIN ENCRYPTED PRIVATE KEY-----
  role: jertel

  MIhsj2sd41fwaa...
  -----END ENCRYPTED PRIVATE KEY-----
```

**NOTE** This configuration only applies to the `out` action. `check` & `in` aren't impacted. Hence, it would be possible to `check` or use `in` to get unsigned images.

## Behavior

### `check` with `tag`: discover new digests for the tag

Reports the current digest that the registry has for the tag configured in
`source`.

### `check` without `tag`: discover semver tags

Detects tags which contain semver version numbers. Version numbers do not
need to contain all 3 segments (major/minor/patch).

Each unique digest will be returned only once, with the most specific version
tag available. This is to handle "alias" tags like `1`, `1.2` pointing to
`1.2.3`.

Note: the initial `check` call will return *all valid versions*, which is
unlike most resources which only return the latest version. This is an
intentional choice which will become the normal behavior for resources in
the future (per concourse/rfcs#38).

Example:

```yaml
resources:
- name: concourse
  type: registry-image
  source: {repository: concourse/concourse}
```

The above resource definition would detect the following versions:

```json
[
  {
    "tag": "1.6.0",
    "digest": "sha256:e1ad01d3227569ad869bdb6bd68cf1ea54057566c25bae38b99d92bbe9f28d78"
  },
  {
    "tag": "2.0.0",
    "digest": "sha256:9ab8d1021d97c6602abbb2c40548eab67aa7babca22f6fe33ab80f4cbf8ea92c"
  },
  // ...
]
```

#### Variant tags

Docker repositories have a pretty common convention of adding `-SUFFIX` to
tags to denote "variant" images, i.e. the same version but with a different
base image or dependency. For example, `1.2.3` vs `1.2.3-alpine`.

With a `variant` value specified, only semver tags with the matching variant
will be detected. With `variant` omitted, tags which include a variant are
ignored.

Note: some image tags actually include *mutliple* variants, e.g.
`1.2.3-php7.3-apache`. With a variant of only `apache` configured, these tags
will be skipped to avoid accidentally using multiple variants. In order to
use these tags, you must specify the full variant combination, e.g.
`php7.3-apache`.

Example:

```yaml
resources:
- name: concourse
  type: registry-image
  source:
    repository: concourse/concourse
    variant: ubuntu
```

The above resource definition would detect the following versions:

```json
[
  {
    "tag": "5.2.1-ubuntu",
    "digest": "sha256:91f5d180d84ee4b2cedfae45771adac62c67c3f5f615448d3c34057c09404f27"
  },
  {
    "tag": "5.2.2-ubuntu",
    "digest": "sha256:cb631d788797f0fbbe72a00afd18e5e4bced356e1b988d1862dc9565130a6226"
  },
  // ...
]
```

#### Pre-release versions

By default, pre-release versions are ignored. With `pre_releases: true`, they
will be included.

Note however that variants and pre-releases both use the same syntax:
`1.2.3-alpine` is technically also valid syntax for a Semver prerelease. For
this reason, the resource will only consider prerelease data starting with
`alpha`, `beta`, or `rc` as a proper prerelease, treating anything else as
a variant.


### `in`: fetch an image

Fetches an image at the exact digest specified by the version.

#### Parameters

* `format`: *Optional. Default `rootfs`.* The format to fetch as.
* `skip_download`: *Optional. Default `false`.* Skip downloading the image.
  Useful only to trigger a job without using the object.

#### Files created by the resource

The resource will produce the following files:

* `./repository`: A file containing the image's full repository name, e.g. `concourse/concourse`.
  For ECR images, this will include the registry the image was pulled from.
* `./tag`: A file containing the tag from the version.
* `./digest`: A file containing the digest from the version, e.g. `sha256:...`.

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


### `out`: push and tag an image

Pushes an image to the registry as the specified tags.

The currently encouraged way to build these images is by using the
[`oci-build-task`](https://github.com/vito/oci-build-task).

Tags may be specified in multiple ways:

* With `tag` configured in `source`, the configured tag will always be pushed.
* With `version` given in `params`, the image will be pushed using the version
  number as a tag, optionally with a `variant` suffix (configured in `source`).
* With `additional_tags` given in `params`, the image will be pushed as each
  tag listed in the file (whitespace separated).

#### Parameters

* `image`: *Required.* The path to the OCI image tarball to upload. Expanded
  with [`filepath.Glob`](https://golang.org/pkg/path/filepath/#Glob).

* `version`: *Optional.* A version number to use as a tag.

* `bump_aliases`: *Optional. Default `false`.* When set to `true` and `version`
  is specified, automatically bump alias tags for the version.

  For example, when pushing version `1.2.3`, push the same image to the
  following tags:

  * `1.2`, if 1.2.3 is the latest version of 1.2.x.
  * `1`, if 1.2.3 is the latest version of 1.x.
  * `latest`, if 1.2.3 is the latest version overall.

  If `variant` is configured as `foo`, push the same image to the following
  tags:

  * `1.2-foo`, if 1.2.3 is the latest version of 1.2.x with `foo`.
  * `1-foo`, if 1.2.3 is the latest version of 1.x with `foo`.
  * `foo`, if 1.2.3 is the latest version overall for `foo`.

  Determining which tags to bump is done by comparing to the existing tags
  that exist on the registry.

* `additional_tags`: *Optional.* The path to a file with whitespace-separated
  list of tag values to tag the image with (in addition to the tag configured
  in `source`).

### Use in tasks

Images used as
[image resources](https://concourse-ci.org/tasks.html#schema.task.image_resource)
in tasks are called
[anonymous resources](https://concourse-ci.org/tasks.html#schema.anonymous_resource).
Anonymous resources can specify
[a version](https://concourse-ci.org/tasks.html#schema.anonymous_resource.version),
which is the image digest. For example:


```
image_resource:
  type: docker-image
  source:
    repository: golang
  version:
    digest: 'sha256:5f640aeb8b78e9876546a9d06b928d2ad0c6e51900bcba10ff4e12dc57f6f265'
```

This is useful when the registry image does not have tags, or when the tags are
going to be re-used.

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
docker build -t registry-image-resource --target tests -f dockerfiles/alpine/Dockerfile .
docker build -t registry-image-resource --target tests -f dockerfiles/ubuntu/Dockerfile .
```

#### Integration tests

The integration requires 3 docker repos: one private dockerhub repo, one public
dockerhub repo, and one GCR repo. The `docker build` step requires setting
`--build-args` so the integration will run.

Run the tests with the following command:

```sh
docker build . -t registry-image-resource --target tests -f dockerfiles/alpine/Dockerfile \
  --build-arg DOCKER_PRIVATE_USERNAME="some-username" \
  --build-arg DOCKER_PRIVATE_PASSWORD="some-password" \
  --build-arg DOCKER_PRIVATE_REPO="some/repo" \
  --build-arg DOCKER_PUSH_USERNAME="some-username" \
  --build-arg DOCKER_PUSH_PASSWORD="some-password" \
  --build-arg DOCKER_PUSH_REPO="some/repo" \
  --build-arg GCR_PUSH_SERVICE_ACCOUNT_KEY='{"some":"json"}' \
  --build-arg GCR_PUSH_REPO="some/repo"

docker build . -t registry-image-resource --target tests -f dockerfiles/ubuntu/Dockerfile \
  --build-arg DOCKER_PRIVATE_USERNAME="some-username" \
  --build-arg DOCKER_PRIVATE_PASSWORD="some-password" \
  --build-arg DOCKER_PRIVATE_REPO="some/repo" \
  --build-arg DOCKER_PUSH_USERNAME="some-username" \
  --build-arg DOCKER_PUSH_PASSWORD="some-password" \
  --build-arg DOCKER_PUSH_REPO="some/repo" \
  --build-arg GCR_PUSH_SERVICE_ACCOUNT_KEY='{"some":"json"}' \
  --build-arg GCR_PUSH_REPO="some/repo"
```

Note that you may omit any of the repo credentials in order to skip those
integration tests.

### Contributing

Please make all pull requests to the `master` branch and ensure tests pass
locally.
