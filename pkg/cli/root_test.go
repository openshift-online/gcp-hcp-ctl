package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigUpdatesPersistentOutputFlag(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("output: yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flag := rootCmd.PersistentFlags().Lookup("output")
	previousPath, previousOutput, previousFlagValue, previousChanged := configPath, outputFormat, flag.Value.String(), flag.Changed
	t.Cleanup(func() {
		configPath = previousPath
		if err := flag.Value.Set(previousFlagValue); err != nil {
			t.Errorf("restoring output flag: %v", err)
		}
		outputFormat = previousOutput
		flag.Changed = previousChanged
	})
	configPath = configFile
	flag.Changed = false
	if err := loadConfig(rootCmd); err != nil {
		t.Fatal(err)
	}
	if got := flag.Value.String(); got != "yaml" {
		t.Errorf("persistent output flag = %q, want yaml", got)
	}
	if outputFormat != "yaml" {
		t.Errorf("bound output format = %q, want yaml", outputFormat)
	}
}
