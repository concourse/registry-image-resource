package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"

	"github.com/blang/semver"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
)

type CheckRequest struct {
	Source  resource.Source   `json:"source"`
	Version *resource.Version `json:"version"`
}

type CheckResponse []resource.Version

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	var req CheckRequest
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
	}

	ref, err := getReference(req)
	if err != nil {
		logrus.Errorf("could not resolve repository/tag reference: %s", err)
		os.Exit(1)
		return
	}

	var response CheckResponse
	err = resource.RetryOnRateLimit(func() error {
		var err error
		response, err = check(req, ref)
		return err
	})
	if err != nil {
		logrus.Errorf("check failed: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(response)
}

func check(req CheckRequest, ref name.Reference) (CheckResponse, error) {
	if ref == nil {
		return CheckResponse{}, nil
	}

	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	var missingTag bool
	image, err := remote.Image(ref, imageOpts...)
	if err != nil {
		missingTag = checkMissingManifest(err)
		if !missingTag {
			return CheckResponse{}, fmt.Errorf("get remote image: %w", err)
		}
	}

	var digest v1.Hash
	if !missingTag {
		digest, err = image.Digest()
		if err != nil {
			return CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
		}
	}

	response := CheckResponse{}
	if req.Version != nil && !missingTag && req.Version.Digest != digest.String() {
		digestRef, err := name.ParseReference(req.Source.Repository+"@"+req.Version.Digest, name.WeakValidation)
		if err != nil {
			return CheckResponse{}, fmt.Errorf("resolve repository/digest reference: %w", err)
		}

		digestImage, err := remote.Image(digestRef, imageOpts...)
		var missingDigest bool
		if err != nil {
			missingDigest = checkMissingManifest(err)
			if !missingDigest {
				return CheckResponse{}, fmt.Errorf("get remote image: %w", err)
			}
		}

		if !missingDigest {
			_, err = digestImage.Digest()
			if err != nil {
				return CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
			}

			response = append(response, *req.Version)
		}
	}

	if !missingTag {
		response = append(response, resource.Version{
			Digest: digest.String(),
		})
	}

	return response, nil
}

func checkMissingManifest(err error) bool {
	var missing bool
	if rErr, ok := err.(*transport.Error); ok {
		for _, e := range rErr.Errors {
			if e.Code == transport.ManifestUnknownErrorCode {
				missing = true
				break
			}
		}
	}
	return missing
}

func getReference(req CheckRequest) (name.Reference, error) {
	if req.Source.TagFilter == "" {
		return name.ParseReference(req.Source.Name(), name.WeakValidation)
	}

	re := regexp.MustCompile(req.Source.TagFilter)

	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	repo, err := name.NewRepository(req.Source.Repository)
	if err != nil {
		log.Fatal(err)
	}

	tags, err := remote.List(repo, imageOpts...)
	if err != nil {
		log.Fatal(err)
	}

	filteredTags := [][]string{}
	for _, tag := range tags {
		matches := re.FindStringSubmatch(tag)
		if matches != nil {
			filteredTags = append(filteredTags, matches)
		}
	}

	sort.Slice(filteredTags, func(i, j int) bool {
		v1, v2 := semver.MustParse(filteredTags[i][1]), semver.MustParse(filteredTags[j][1])
		return v1.GT(v2)
	})

	if len(filteredTags) == 0 {
		return nil, nil
	}

	nameRef := fmt.Sprintf("%s:%s", req.Source.Repository, filteredTags[0][0])
	return name.ParseReference(nameRef, name.WeakValidation)
}
