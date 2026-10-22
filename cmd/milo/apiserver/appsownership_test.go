package app

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
)

func TestAppsOwnershipCannotBeDisabled(t *testing.T) {
	opts := NewOptions().Admission
	require.NoError(t, validateAppsOwnershipAdmission(opts))
	opts.GenericAdmission.DisablePlugins = append(opts.GenericAdmission.DisablePlugins, appsownership.PluginName)
	require.Error(t, validateAppsOwnershipAdmission(opts))
	opts = NewOptions().Admission
	opts.GenericAdmission.DefaultOffPlugins.Insert(appsownership.PluginName)
	require.Error(t, validateAppsOwnershipAdmission(opts))
	opts = NewOptions().Admission
	opts.GenericAdmission.RecommendedPluginOrder = slices.DeleteFunc(opts.GenericAdmission.RecommendedPluginOrder, func(s string) bool { return s == appsownership.PluginName })
	require.Error(t, validateAppsOwnershipAdmission(opts))
	opts = NewOptions().Admission
	opts.PluginNames = []string{"NamespaceLifecycle"}
	require.Error(t, validateAppsOwnershipAdmission(opts))
	require.Error(t, validateAppsOwnershipAdmission(nil))
}
