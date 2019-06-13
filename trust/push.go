package trust

import (
	"encoding/hex"
	"sort"

	"github.com/sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/theupdateframework/notary/client"
	"github.com/theupdateframework/notary/tuf/data"

	resource "github.com/concourse/registry-image-resource"
)

func PushTrustedReference(src string, ref name.Reference, img v1.Image, auth authn.Authenticator, digest v1.Hash, ct resource.ContentTrust) error {
	target := &client.Target{}
	h, err := hex.DecodeString(digest.Hex)
	if err != nil {
		logrus.Errorf("failed to decode digest.Hex: %s", err)
		target = nil
		return err
	}
	target.Name = ref.Identifier()
	target.Hashes = data.Hashes{digest.Algorithm: h}
	manifest, _ := img.RawManifest()
	pushResultSize := len(manifest)
	target.Length = int64(pushResultSize)

	if target == nil {
		return errors.Errorf("no targets found, please provide a specific tag in order to sign it")
	}

	logrus.Info("Signing and pushing trust metadata")

	registry := ref.Context().Registry
	repo, err := GetNotaryRepository(src, ref, auth, &registry, ct)
	if err != nil {
		logrus.Errorf("failed to get notary repository %s", err)
		return err
	}
	logrus.Info("Signing and pushing trust metadata")
	_, err = repo.ListTargets()

	switch err.(type) {
	case client.ErrRepoNotInitialized, client.ErrRepositoryNotExist:
		keys := repo.GetCryptoService().ListKeys(data.CanonicalRootRole)
		var rootKeyID string
		// always select the first root key
		if len(keys) > 0 {
			sort.Strings(keys)
			rootKeyID = keys[0]
		} else {
			rootPublicKey, err := repo.GetCryptoService().Create(data.CanonicalRootRole, "", data.ECDSAKey)
			if err != nil {
				logrus.Errorf("error: %s", err)
			}
			rootKeyID = rootPublicKey.ID()
		}
		// Initialize the notary repository with a remotely managed snapshot key
		if err := repo.Initialize([]string{rootKeyID}, data.CanonicalSnapshotRole); err != nil {
			logrus.Errorf("error: %s", err)
		}

		logrus.Infof("Finished initializing %s\n", ref.Context().Name())
		err = repo.AddTarget(target, data.CanonicalTargetsRole)
	case nil:
		// already initialized and we have successfully downloaded the latest metadata
		err = AddTargetToAllSignableRoles(repo, target)
	default:
		return NotaryError(registry.Name(), err)
	}

	if err == nil {
		err = repo.Publish()
	}

	if err != nil {
		logrus.Infof("failed to sign: %s", err)
	}
	logrus.Infof("Successfully signed %s:%s\n", ref.Context().Name(), ref.Identifier())
	return nil
}

// AddTargetToAllSignableRoles attempts to add the image target to all the top level delegation roles we can
// (based on whether we have the signing key and whether the role's path allows
// us to).
// If there are no delegation roles, we add to the targets role.
func AddTargetToAllSignableRoles(repo client.Repository, target *client.Target) error {
	signableRoles, err := GetSignableRoles(repo, target)
	if err != nil {
		return err
	}

	return repo.AddTarget(target, signableRoles...)
}
