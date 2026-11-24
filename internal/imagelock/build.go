package imagelock

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Resolver returns the registry's top-level digest for an image.
type Resolver func(context.Context, string) (string, error)

// RegistryResolver fetches the manifest or index without selecting a platform.
func RegistryResolver(ctx context.Context, image string) (string, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", err
	}
	desc, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

// Build constructs a lock. Selected names are refreshed independently in each
// section; nil selections reuse all matching entries. Removed entries are dropped.
// Resolve on the resulting lock enforces exact coverage during deployment.
func Build(ctx context.Context, services, jobs map[string]string, previous *Lock, selectedServices, selectedJobs map[string]bool, resolver Resolver) (*Lock, error) {
	if previous != nil {
		if err := previous.validate(); err != nil {
			return nil, err
		}
	}
	if resolver == nil {
		resolver = RegistryResolver
	}
	out := &Lock{Version: 1, Services: map[string]Entry{}, Jobs: map[string]Entry{}}
	build := func(input map[string]string, old map[string]Entry, selected map[string]bool, dst map[string]Entry) error {
		keys := make([]string, 0, len(input))
		for k := range input {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			image := input[k]
			if k == "" {
				return fmt.Errorf("empty key")
			}
			named, err := reference.ParseNormalizedNamed(image)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			repo := reference.TrimNamed(named).Name()
			if e, ok := old[k]; ok && e.Image == image && !selected[k] {
				dst[k] = e
				continue
			}
			var digest string
			if pinned, ok := named.(reference.Digested); ok {
				digest = pinned.Digest().String()
			} else {
				digest, err = resolver(ctx, image)
				if err != nil {
					return fmt.Errorf("%s: %w", k, err)
				}
			}
			// Accept only a bare SHA256, and bind it to the normalized repository.
			if strings.Contains(digest, "@") {
				return fmt.Errorf("%s: expected bare digest", k)
			}
			entry := Entry{Image: image, Digest: repo + "@" + digest}
			if !digestPattern.MatchString(entry.Digest) {
				return fmt.Errorf("%s: invalid digest %q", k, digest)
			}
			dst[k] = entry
		}
		return nil
	}
	var oldServices, oldJobs map[string]Entry
	if previous != nil {
		oldServices, oldJobs = previous.Services, previous.Jobs
	}
	if err := build(services, oldServices, selectedServices, out.Services); err != nil {
		return nil, err
	}
	if err := build(jobs, oldJobs, selectedJobs, out.Jobs); err != nil {
		return nil, err
	}
	return out, nil
}
