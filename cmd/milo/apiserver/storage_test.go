package app

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

func TestStorageOptionsAreCommandLocal(t *testing.T) {
	first, second := newStorageOptions(), newStorageOptions()
	flags := pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)
	first.addFlags(flags)
	require.NoError(t, flags.Parse([]string{
		"--storage-engine=cockroachdb",
		"--cockroachdb-dsn=postgresql://localhost/milo",
		"--cockroachdb-group-commit=false",
		"--cockroachdb-max-conns=8",
		"--cockroachdb-watch-replay-window=10m",
		"--cockroachdb-watch-lag-readiness=2m",
	}))
	require.NoError(t, first.validate())
	require.Equal(t, storageEngineCockroachDB, first.engine)
	require.False(t, first.groupCommit)
	require.Equal(t, int32(8), first.maxConns)
	require.Equal(t, 10*time.Minute, first.replayWindow)
	require.Equal(t, 2*time.Minute, first.lagReadiness)
	require.Equal(t, newStorageOptions(), second)
}

func TestStorageOptionsValidation(t *testing.T) {
	for name, change := range map[string]func(*storageOptions){
		"unknown engine":             func(o *storageOptions) { o.engine = "unknown" },
		"missing DSN":                func(o *storageOptions) { o.dsn = "" },
		"zero connections":           func(o *storageOptions) { o.maxConns = 0 },
		"negative project limit":     func(o *storageOptions) { o.projectWrites = -1 },
		"negative transaction limit": func(o *storageOptions) { o.transactions = -1 },
		"no admission wait":          func(o *storageOptions) { o.admissionWait = 0 },
		"no replay window":           func(o *storageOptions) { o.replayWindow = 0 },
		"no replay events":           func(o *storageOptions) { o.replayEvents = 0 },
		"negative lag readiness":     func(o *storageOptions) { o.lagReadiness = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			o := newStorageOptions()
			o.engine, o.dsn = storageEngineCockroachDB, "postgresql://localhost/milo"
			change(&o)
			require.Error(t, o.validate())
		})
	}
	require.NoError(t, newStorageOptions().validate())
}

func TestStorageValidationPreservesUnrelatedErrors(t *testing.T) {
	missingEtcd := options.NewEtcdOptions(storagebackend.NewDefaultConfig("", nil)).Validate()
	require.NotEmpty(t, missingEtcd)
	other := errors.New("invalid serving configuration")
	for _, engine := range []string{storageEngineEtcd, storageEngineCockroachDB} {
		t.Run(engine, func(t *testing.T) {
			o := newStorageOptions()
			o.engine = engine
			got := o.withoutEtcdServersError(append(slices.Clone(missingEtcd), other))
			want := []error{other}
			if engine == storageEngineEtcd {
				want = append(slices.Clone(missingEtcd), other)
			}
			require.Equal(t, want, got)
		})
	}
}
