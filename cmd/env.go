package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iminaii/iscrt/backend"
	"github.com/iminaii/iscrt/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage .env files with project-scoped secrets",
}

var pushCmd = &cobra.Command{
	Use:   "push [file]",
	Short: "Push .env file secrets into encrypted store",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file := ".env"
		if len(args) > 0 {
			file = args[0]
		}

		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			project = currentDirName()
		}

		mode, _ := cmd.Flags().GetString("mode")

		return envPush(file, project, mode)
	},
}

// envPush reads a .env file and stores all keys under the project prefix.
func envPush(file string, project string, mode string) error {
	vars, err := parseEnvFile(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file, err)
	}

	if len(vars) == 0 {
		fmt.Println("No secrets found in", file)
		return nil
	}

	storage := viper.GetString(configKeyStorage)
	password := []byte(viper.GetString(configKeyPassword))

	b, err := backend.Backends[storage].NewContext(cmdContext, viper.AllSettings())
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}

	exists, err := b.ExistsContext(cmdContext)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("store not initialized, run 'iscrt init' first")
	}

	data, err := b.LoadContext(cmdContext)
	if err != nil {
		return err
	}
	s, err := store.ReadStoreContext(cmdContext, password, data)
	if err != nil {
		return err
	}

	prefix := project + "/"

	// In replace mode, track existing keys to remove after setting new ones
	var existingKeys []string
	if mode == "replace" {
		existingKeys = s.ListPrefixContext(cmdContext, prefix)
	}

	newCount, updatedCount, unchangedCount := 0, 0, 0

	for key, val := range vars {
		fullKey := prefix + key
		if s.HasContext(cmdContext, fullKey) {
			existing, _ := s.GetContext(cmdContext, fullKey)
			if string(existing) == val {
				unchangedCount++
				continue
			}
			updatedCount++
		} else {
			newCount++
		}
		if err := s.SetContext(cmdContext, fullKey, []byte(val)); err != nil {
			return fmt.Errorf("failed to set %s: %w", fullKey, err)
		}
	}

	// Replace mode: remove keys not in the source file
	removedCount := 0
	if mode == "replace" {
		sourceKeys := make(map[string]bool)
		for key := range vars {
			sourceKeys[prefix+key] = true
		}
		for _, existingKey := range existingKeys {
			if !sourceKeys[existingKey] {
				s.UnsetContext(cmdContext, existingKey)
				removedCount++
			}
		}
	}

	data, err = store.WriteStoreContext(cmdContext, password, s)
	if err != nil {
		return err
	}
	if err := b.SaveContext(cmdContext, data); err != nil {
		return err
	}

	fmt.Printf("Pushed %d secrets to project %q (%d new, %d updated, %d unchanged",
		newCount+updatedCount+unchangedCount, project, newCount, updatedCount, unchangedCount)
	if removedCount > 0 {
		fmt.Printf(", %d removed", removedCount)
	}
	fmt.Println(")")

	return nil
}

// currentDirName returns the basename of the current working directory.
func currentDirName() string {
	dir, err := os.Getwd()
	if err != nil {
		return "default"
	}
	return filepath.Base(dir)
}

// --- env pull ---

var pullCmd = &cobra.Command{
	Use:   "pull [file]",
	Short: "Pull project secrets into a .env file",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file := ".env"
		if len(args) > 0 {
			file = args[0]
		}
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			project = currentDirName()
		}
		force, _ := cmd.Flags().GetBool("force")
		return envPull(file, project, force)
	},
}

func envPull(file string, project string, force bool) error {
	// If file exists and not force, error
	if !force {
		if _, err := os.Stat(file); err == nil {
			return fmt.Errorf("%s already exists, use --force to overwrite", file)
		}
	}

	storage := viper.GetString(configKeyStorage)
	password := []byte(viper.GetString(configKeyPassword))

	b, err := backend.Backends[storage].NewContext(cmdContext, viper.AllSettings())
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}
	exists, err := b.ExistsContext(cmdContext)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("store not initialized, run 'iscrt init' first")
	}

	data, err := b.LoadContext(cmdContext)
	if err != nil {
		return err
	}
	s, err := store.ReadStoreContext(cmdContext, password, data)
	if err != nil {
		return err
	}

	prefix := project + "/"
	keys := s.ListPrefixContext(cmdContext, prefix)
	if len(keys) == 0 {
		return fmt.Errorf("no secrets found for project %q", project)
	}

	vars := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := s.GetContext(cmdContext, key)
		if err != nil {
			return err
		}
		envKey := strings.TrimPrefix(key, prefix)
		vars[envKey] = string(val)
	}

	if err := writeEnvFile(file, vars, project); err != nil {
		return err
	}

	fmt.Printf("Pulled %d secrets from project %q to %s\n", len(vars), project, file)
	return nil
}

func init() {
	addCommand(envCmd)

	envCmd.AddCommand(pushCmd)
	pushCmd.Flags().String("project", "", "project name (default: current directory name)")
	pushCmd.Flags().String("mode", "merge", "push mode: merge or replace")

	envCmd.AddCommand(pullCmd)
	pullCmd.Flags().String("project", "", "project name (default: current directory name)")
	pullCmd.Flags().BoolP("force", "f", false, "overwrite existing file")
}
