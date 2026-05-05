// Package featureflags provides an OpenFeature provider backed by the Milo
// AllowanceBucket API.
//
// Feature flags are org-level boolean entitlements. A flag is enabled for an
// org when an AllowanceBucket exists with status.available > 0 for that
// (org, resourceType) pair. The resourceType is
// "features.miloapis.com/<flagKey>".
package featureflags

import (
	"context"
	"fmt"

	"github.com/open-feature/go-sdk/openfeature"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

const (
	// ProviderName is the name reported in OpenFeature metadata.
	ProviderName = "milo-allowance-bucket"

	// featureResourceTypePrefix is prepended to a flag key to form the
	// AllowanceBucket spec.resourceType used for feature entitlements.
	featureResourceTypePrefix = "features.miloapis.com/"
)

// AllowanceBucketLister is the subset of the controller-runtime client needed
// by Provider. It is satisfied by any sigs.k8s.io/controller-runtime/pkg/client.Client.
type AllowanceBucketLister interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// Provider implements the OpenFeature FeatureProvider interface. It resolves
// boolean flags by querying AllowanceBuckets: a flag is enabled when an
// AllowanceBucket for (org, features.miloapis.com/<flagKey>) has
// status.available > 0.
//
// All non-boolean flag types return a TYPE_MISMATCH error because feature flags
// are exclusively boolean entitlements.
type Provider struct {
	lister AllowanceBucketLister
}

// NewProvider returns a Provider that uses the given AllowanceBucketLister to
// query flag entitlements. Pass any controller-runtime client as the lister.
func NewProvider(lister AllowanceBucketLister) *Provider {
	return &Provider{lister: lister}
}

// Metadata returns the OpenFeature provider metadata.
func (p *Provider) Metadata() openfeature.Metadata {
	return openfeature.Metadata{Name: ProviderName}
}

// Hooks returns no hooks; the provider does not require lifecycle callbacks.
func (p *Provider) Hooks() []openfeature.Hook {
	return []openfeature.Hook{}
}

// BooleanEvaluation resolves a feature flag for an organisation.
//
// Resolution rules:
//   - evalCtx[targetingKey] is the organisation name. An empty or missing
//     targeting key returns defaultValue with reason DEFAULT.
//   - The flag is enabled (true, TARGETING_MATCH) when an AllowanceBucket
//     exists for (org, features.miloapis.com/<flag>) with status.available > 0.
//   - Any API error returns defaultValue with reason DEFAULT (no panic).
//   - A missing bucket, or a bucket with status.available == 0, returns
//     defaultValue with reason DEFAULT.
func (p *Provider) BooleanEvaluation(
	ctx context.Context,
	flag string,
	defaultValue bool,
	evalCtx openfeature.FlattenedContext,
) openfeature.BoolResolutionDetail {
	org, ok := targetingKey(evalCtx)
	if !ok || org == "" {
		return boolDefault(defaultValue)
	}

	resourceType := featureResourceTypePrefix + flag

	var bucketList quotav1alpha1.AllowanceBucketList
	if err := p.lister.List(ctx, &bucketList,
		client.MatchingFields{
			"spec.consumerRef.name": org,
			"spec.resourceType":     resourceType,
		},
	); err != nil {
		// Return the default value on API errors; do not propagate the error
		// as a panic or a hard failure — the caller must decide whether the
		// feature should be on or off by default.
		return boolDefaultWithError(defaultValue, fmt.Errorf("failed to list AllowanceBuckets for org %q flag %q: %w", org, flag, err))
	}

	for i := range bucketList.Items {
		if bucketList.Items[i].Status.Available > 0 {
			return openfeature.BoolResolutionDetail{
				Value: true,
				ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
					Reason: openfeature.TargetingMatchReason,
				},
			}
		}
	}

	return boolDefault(defaultValue)
}

// StringEvaluation returns a TYPE_MISMATCH error. Feature flags are boolean
// entitlements and cannot be resolved as strings.
func (p *Provider) StringEvaluation(
	_ context.Context,
	_ string,
	defaultValue string,
	_ openfeature.FlattenedContext,
) openfeature.StringResolutionDetail {
	return openfeature.StringResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: openfeature.NewTypeMismatchResolutionError(
				"feature flags are boolean entitlements; use BooleanEvaluation",
			),
			Reason: openfeature.ErrorReason,
		},
	}
}

// FloatEvaluation returns a TYPE_MISMATCH error. Feature flags are boolean
// entitlements and cannot be resolved as floats.
func (p *Provider) FloatEvaluation(
	_ context.Context,
	_ string,
	defaultValue float64,
	_ openfeature.FlattenedContext,
) openfeature.FloatResolutionDetail {
	return openfeature.FloatResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: openfeature.NewTypeMismatchResolutionError(
				"feature flags are boolean entitlements; use BooleanEvaluation",
			),
			Reason: openfeature.ErrorReason,
		},
	}
}

// IntEvaluation returns a TYPE_MISMATCH error. Feature flags are boolean
// entitlements and cannot be resolved as integers.
func (p *Provider) IntEvaluation(
	_ context.Context,
	_ string,
	defaultValue int64,
	_ openfeature.FlattenedContext,
) openfeature.IntResolutionDetail {
	return openfeature.IntResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: openfeature.NewTypeMismatchResolutionError(
				"feature flags are boolean entitlements; use BooleanEvaluation",
			),
			Reason: openfeature.ErrorReason,
		},
	}
}

// ObjectEvaluation returns a TYPE_MISMATCH error. Feature flags are boolean
// entitlements and cannot be resolved as objects.
func (p *Provider) ObjectEvaluation(
	_ context.Context,
	_ string,
	defaultValue any,
	_ openfeature.FlattenedContext,
) openfeature.InterfaceResolutionDetail {
	return openfeature.InterfaceResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: openfeature.NewTypeMismatchResolutionError(
				"feature flags are boolean entitlements; use BooleanEvaluation",
			),
			Reason: openfeature.ErrorReason,
		},
	}
}

// targetingKey extracts the targeting key from a flattened evaluation context.
// The second return value is false when the key is absent or not a string.
func targetingKey(evalCtx openfeature.FlattenedContext) (string, bool) {
	v, ok := evalCtx[openfeature.TargetingKey]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// boolDefault returns a BoolResolutionDetail with DEFAULT reason.
func boolDefault(value bool) openfeature.BoolResolutionDetail {
	return openfeature.BoolResolutionDetail{
		Value: value,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// boolDefaultWithError returns a BoolResolutionDetail with DEFAULT reason and a
// general resolution error. The default value is returned so callers can
// continue operating; the error is surfaced via ResolutionError for
// observability.
func boolDefaultWithError(value bool, err error) openfeature.BoolResolutionDetail {
	return openfeature.BoolResolutionDetail{
		Value: value,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: openfeature.NewGeneralResolutionError(err.Error()),
			Reason:          openfeature.DefaultReason,
		},
	}
}
