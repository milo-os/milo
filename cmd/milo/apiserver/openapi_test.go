package app

import (
	"encoding/json"
	"testing"

	openapi_v2 "github.com/google/gnostic-models/openapiv2"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/kube-openapi/pkg/builder"
	"k8s.io/kube-openapi/pkg/util/proto"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	generatedopenapi "k8s.io/kubernetes/pkg/generated/openapi"

	identityapi "go.miloapis.com/milo/pkg/apis/identity"
	identityopenapi "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

// kubectl validates against the OpenAPI v2 document whenever the server does not serve v3 yet, as during startup, and
// its v2 parser cannot resolve a definition whose name holds a "/". A kind's definition also carries its group, version
// and kind only when its name is the one the scheme reports for it.
func TestIdentityOpenAPIV2ParsesForKubectl(t *testing.T) {
	var names []string
	for name := range identityopenapi.GetOpenAPIDefinitions(func(string) spec.Ref { return spec.Ref{} }) {
		names = append(names, name)
	}
	identityScheme := runtime.NewScheme()
	identityapi.Install(identityScheme)
	config := genericapiserver.DefaultOpenAPIConfig(openAPIDefinitions, openapinamer.NewDefinitionNamer(legacyscheme.Scheme, identityScheme))
	config.Info.Version = "test" // the server sets its own version; the v2 schema requires one

	swagger, err := builder.BuildOpenAPIDefinitionsForResources(config, names...)
	require.NoError(t, err)
	data, err := json.Marshal(swagger)
	require.NoError(t, err)
	document, err := openapi_v2.ParseDocument(data)
	require.NoError(t, err)

	_, err = proto.NewOpenAPIData(document)
	require.NoError(t, err)

	passkey := swagger.Definitions[identityopenapi.Passkey{}.OpenAPIModelName()]
	require.Contains(t, passkey.Extensions, "x-kubernetes-group-version-kind")
}

// The identity package's generated file also carries the apimachinery definitions its types reference. They must
// match Kubernetes' own, so the served documents are the same whichever copy a server keeps.
func TestIdentityOpenAPIMatchesKubernetesForSharedTypes(t *testing.T) {
	ref := func(string) spec.Ref { return spec.Ref{} }
	kubernetes := generatedopenapi.GetOpenAPIDefinitions(ref)
	shared := 0
	for name, definition := range identityopenapi.GetOpenAPIDefinitions(ref) {
		own, ok := kubernetes[name]
		if !ok {
			continue
		}
		shared++
		require.Equal(t, own.Schema, definition.Schema, name)
	}
	require.NotZero(t, shared)
}
