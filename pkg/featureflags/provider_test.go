package featureflags_test

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature/go-sdk/openfeature"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/featureflags"
)

// fakeLister is a stub AllowanceBucketLister for unit tests. It returns a
// fixed set of AllowanceBuckets from its List call, filtered by the
// spec.consumerRef.name and spec.resourceType MatchingFields options when
// present, mirroring the field-selector behaviour of a real kube API server.
type fakeLister struct {
	buckets []quotav1alpha1.AllowanceBucket
	err     error
}

func (f *fakeLister) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if f.err != nil {
		return f.err
	}

	lo := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(lo)
	}

	bucketList, ok := list.(*quotav1alpha1.AllowanceBucketList)
	if !ok {
		return errors.New("fakeLister only supports AllowanceBucketList")
	}

	// Apply MatchingFields filtering — mirrors the indexed field selectors
	// (spec.consumerRef.name, spec.resourceType) that the real API server uses.
	var wantConsumer, wantResourceType string
	if lo.FieldSelector != nil {
		for _, r := range lo.FieldSelector.Requirements() {
			switch r.Field {
			case "spec.consumerRef.name":
				wantConsumer = r.Value
			case "spec.resourceType":
				wantResourceType = r.Value
			}
		}
	}

	var matched []quotav1alpha1.AllowanceBucket
	for _, b := range f.buckets {
		if wantConsumer != "" && b.Spec.ConsumerRef.Name != wantConsumer {
			continue
		}
		if wantResourceType != "" && b.Spec.ResourceType != wantResourceType {
			continue
		}
		matched = append(matched, b)
	}
	bucketList.Items = matched
	return nil
}

// bucket is a test helper that constructs an AllowanceBucket with the given
// consumer name, resourceType, and available quota.
func bucket(consumerName, resourceType string, available int64) quotav1alpha1.AllowanceBucket {
	return quotav1alpha1.AllowanceBucket{
		ObjectMeta: metav1.ObjectMeta{
			Name: consumerName + "-" + resourceType,
		},
		Spec: quotav1alpha1.AllowanceBucketSpec{
			ConsumerRef:  quotav1alpha1.ConsumerRef{Name: consumerName, Kind: "Organization"},
			ResourceType: resourceType,
		},
		Status: quotav1alpha1.AllowanceBucketStatus{
			Available: available,
		},
	}
}

func evalCtxWithOrg(org string) openfeature.FlattenedContext {
	return openfeature.FlattenedContext{
		openfeature.TargetingKey: org,
	}
}

func TestBooleanEvaluation(t *testing.T) {
	const (
		org      = "acme-corp"
		flagKey  = "some-feature"
		resType  = "features.miloapis.com/" + flagKey
		otherOrg = "other-org"
	)

	tests := []struct {
		name         string
		buckets      []quotav1alpha1.AllowanceBucket
		listerErr    error
		evalCtx      openfeature.FlattenedContext
		defaultValue bool
		wantValue    bool
		wantReason   openfeature.Reason
	}{
		{
			name: "flag enabled when bucket exists with available > 0",
			buckets: []quotav1alpha1.AllowanceBucket{
				bucket(org, resType, 1),
			},
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  true,
			wantReason: openfeature.TargetingMatchReason,
		},
		{
			name: "flag disabled when no bucket matches org and flag",
			buckets: []quotav1alpha1.AllowanceBucket{
				bucket(otherOrg, resType, 1),
			},
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name: "flag disabled when bucket exists but available is zero",
			buckets: []quotav1alpha1.AllowanceBucket{
				bucket(org, resType, 0),
			},
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name:       "returns defaultValue on API error without panic",
			listerErr:  errors.New("api server unavailable"),
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name:       "returns defaultValue on API error with non-default default",
			listerErr:  errors.New("api server unavailable"),
			evalCtx:    evalCtxWithOrg(org),
			defaultValue: true,
			wantValue:  true,
			wantReason: openfeature.DefaultReason,
		},
		{
			name:       "returns defaultValue when targetingKey is absent",
			evalCtx:    openfeature.FlattenedContext{},
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name: "returns defaultValue when targetingKey is empty string",
			evalCtx: openfeature.FlattenedContext{
				openfeature.TargetingKey: "",
			},
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name: "bucket for wrong org does not enable flag",
			buckets: []quotav1alpha1.AllowanceBucket{
				bucket(otherOrg, resType, 5),
			},
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
		{
			name: "bucket for correct org but wrong resourceType does not enable flag",
			buckets: []quotav1alpha1.AllowanceBucket{
				bucket(org, "features.miloapis.com/different-feature", 5),
			},
			evalCtx:    evalCtxWithOrg(org),
			wantValue:  false,
			wantReason: openfeature.DefaultReason,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeLister{buckets: tc.buckets, err: tc.listerErr}
			p := featureflags.NewProvider(lister)

			got := p.BooleanEvaluation(context.Background(), flagKey, tc.defaultValue, tc.evalCtx)

			if got.Value != tc.wantValue {
				t.Errorf("Value: got %v, want %v", got.Value, tc.wantValue)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason: got %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

func TestNonBooleanEvaluationsReturnTypeMismatch(t *testing.T) {
	lister := &fakeLister{}
	p := featureflags.NewProvider(lister)
	ctx := context.Background()
	evalCtx := evalCtxWithOrg("acme-corp")

	t.Run("StringEvaluation returns TYPE_MISMATCH", func(t *testing.T) {
		got := p.StringEvaluation(ctx, "flag", "default", evalCtx)
		if got.ResolutionError.Error() == "" {
			t.Error("expected a non-empty ResolutionError for StringEvaluation")
		}
		if got.Reason != openfeature.ErrorReason {
			t.Errorf("Reason: got %q, want %q", got.Reason, openfeature.ErrorReason)
		}
		if got.Value != "default" {
			t.Errorf("Value: got %q, want \"default\"", got.Value)
		}
	})

	t.Run("FloatEvaluation returns TYPE_MISMATCH", func(t *testing.T) {
		got := p.FloatEvaluation(ctx, "flag", 3.14, evalCtx)
		if got.ResolutionError.Error() == "" {
			t.Error("expected a non-empty ResolutionError for FloatEvaluation")
		}
		if got.Reason != openfeature.ErrorReason {
			t.Errorf("Reason: got %q, want %q", got.Reason, openfeature.ErrorReason)
		}
	})

	t.Run("IntEvaluation returns TYPE_MISMATCH", func(t *testing.T) {
		got := p.IntEvaluation(ctx, "flag", int64(42), evalCtx)
		if got.ResolutionError.Error() == "" {
			t.Error("expected a non-empty ResolutionError for IntEvaluation")
		}
		if got.Reason != openfeature.ErrorReason {
			t.Errorf("Reason: got %q, want %q", got.Reason, openfeature.ErrorReason)
		}
	})

	t.Run("ObjectEvaluation returns TYPE_MISMATCH", func(t *testing.T) {
		got := p.ObjectEvaluation(ctx, "flag", map[string]any{"k": "v"}, evalCtx)
		if got.ResolutionError.Error() == "" {
			t.Error("expected a non-empty ResolutionError for ObjectEvaluation")
		}
		if got.Reason != openfeature.ErrorReason {
			t.Errorf("Reason: got %q, want %q", got.Reason, openfeature.ErrorReason)
		}
	})
}

func TestProviderMetadata(t *testing.T) {
	p := featureflags.NewProvider(&fakeLister{})
	meta := p.Metadata()
	if meta.Name != featureflags.ProviderName {
		t.Errorf("Metadata.Name: got %q, want %q", meta.Name, featureflags.ProviderName)
	}
}

func TestProviderHooksEmpty(t *testing.T) {
	p := featureflags.NewProvider(&fakeLister{})
	if hooks := p.Hooks(); len(hooks) != 0 {
		t.Errorf("Hooks: expected empty slice, got %d hooks", len(hooks))
	}
}

// TestProviderImplementsFeatureProvider verifies at compile time that *Provider
// satisfies the openfeature.FeatureProvider interface.
var _ openfeature.FeatureProvider = (*featureflags.Provider)(nil)
