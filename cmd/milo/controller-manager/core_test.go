package app

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestProjectIgnoredResources(t *testing.T) {
	events := schema.GroupResource{Resource: "events"}
	base := map[schema.GroupResource]struct{}{events: {}}

	got := projectIgnoredResources(base, []string{
		"flowschemas.flowcontrol.apiserver.k8s.io",
		" configmaps ",
		"",
	})

	assert.Equal(t, map[schema.GroupResource]struct{}{
		events: {},
		{Group: "flowcontrol.apiserver.k8s.io", Resource: "flowschemas"}: {},
		{Resource: "configmaps"}: {},
	}, got)
	assert.Equal(t, map[schema.GroupResource]struct{}{events: {}}, base)
}

func TestProjectGarbageCollectorFlag(t *testing.T) {
	defaults := append([]string(nil), projectGCIgnoredResources...)
	t.Cleanup(func() { projectGCIgnoredResources = defaults })

	tests := []struct {
		name string
		args []string
		want map[schema.GroupResource]struct{}
	}{
		{
			name: "defaults ignore priority and fairness configuration",
			want: map[schema.GroupResource]struct{}{
				{Group: "flowcontrol.apiserver.k8s.io", Resource: "flowschemas"}:                 {},
				{Group: "flowcontrol.apiserver.k8s.io", Resource: "prioritylevelconfigurations"}: {},
			},
		},
		{
			name: "override replaces defaults",
			args: []string{"--project-gc-ignored-resources=endpointslices.discovery.k8s.io"},
			want: map[schema.GroupResource]struct{}{
				{Group: "discovery.k8s.io", Resource: "endpointslices"}: {},
			},
		},
		{
			name: "empty value watches every type",
			args: []string{"--project-gc-ignored-resources="},
			want: map[schema.GroupResource]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectGCIgnoredResources = append([]string(nil), defaults...)
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			addProjectGarbageCollectorFlags(fs)
			require.NoError(t, fs.Parse(tt.args))

			assert.Equal(t, tt.want, projectIgnoredResources(nil, projectGCIgnoredResources))
		})
	}
}
