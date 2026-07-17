package app

import "testing"

func TestNewCommand_RegistersPasskeysProviderFlags(t *testing.T) {
	cmd := NewCommand()

	for _, name := range []string{
		"passkeys-provider-url",
		"passkeys-provider-ca-file",
		"passkeys-provider-client-cert",
		"passkeys-provider-client-key",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}
