package v1alpha1

import (
	"testing"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestNormalizeWebsite(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
		{name: "bare domain gets https scheme", input: "example.com", want: "https://example.com"},
		{name: "trims surrounding whitespace", input: "  example.com  ", want: "https://example.com"},
		{name: "leaves existing https scheme", input: "https://example.com", want: "https://example.com"},
		{name: "leaves existing http scheme", input: "http://example.com", want: "http://example.com"},
		{name: "leaves existing non-http scheme", input: "ftp://example.com", want: "ftp://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeWebsite(tt.input); got != tt.want {
				t.Fatalf("normalizeWebsite(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateOrganizationContactInfo_Website(t *testing.T) {
	tests := []struct {
		name    string
		website string
		wantErr bool
	}{
		{name: "empty is valid", website: "", wantErr: false},
		{name: "bare domain is valid", website: "example.com", wantErr: false},
		{name: "valid https URL", website: "https://example.com", wantErr: false},
		{name: "valid http URL", website: "http://example.com", wantErr: false},
		{name: "valid URL with path", website: "https://example.com/about", wantErr: false},
		{name: "not a URL", website: "not a url", wantErr: true},
		{name: "scheme with no host", website: "https://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contact := &resourcemanagerv1alpha1.OrganizationContactInfo{
				Email:   "owner@example.com",
				Name:    "Owner",
				Website: tt.website,
			}

			errs := validateOrganizationContactInfo(contact, field.NewPath("spec", "contactInfo"))
			hasWebsiteErr := false
			for _, err := range errs {
				if err.Field == "spec.contactInfo.website" {
					hasWebsiteErr = true
				}
			}

			if hasWebsiteErr != tt.wantErr {
				t.Fatalf("website = %q, errs = %v, wantErr = %v", tt.website, errs, tt.wantErr)
			}
		})
	}
}
