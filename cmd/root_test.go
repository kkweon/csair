package cmd

import "testing"

// TestUseTLSClientEnvToggle pins the monitor's opt-in path: CSAIR_USE_TLS_CLIENT
// must flip useTLSClient() through viper's env mapping (dash→underscore), since
// the scheduled workflow sets the env var rather than passing --use-tls-client.
func TestUseTLSClientEnvToggle(t *testing.T) {
	t.Run("env on", func(t *testing.T) {
		t.Setenv("CSAIR_USE_TLS_CLIENT", "1")
		initConfig()
		if !useTLSClient() {
			t.Error("CSAIR_USE_TLS_CLIENT=1 → useTLSClient()=false, want true")
		}
	})
	t.Run("env off", func(t *testing.T) {
		t.Setenv("CSAIR_USE_TLS_CLIENT", "")
		initConfig()
		if useTLSClient() {
			t.Error("env unset → useTLSClient()=true, want false")
		}
	})
}
