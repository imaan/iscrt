package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imaan/iscrt/backend"
	"github.com/imaan/iscrt/store"
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
	if mode != "merge" && mode != "replace" {
		return fmt.Errorf("invalid mode %q, must be 'merge' or 'replace'", mode)
	}

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

// --- env list ---

type projectInfo struct {
	name  string
	count int
}

type keyEntry struct {
	key         string
	maskedValue string
}

func maskValue(val string, reveal bool) string {
	if reveal {
		return val
	}
	if len(val) <= 6 {
		return "******"
	}
	return val[:4] + "***"
}

var listEnvCmd = &cobra.Command{
	Use:   "list [project]",
	Short: "List projects or keys in a project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, _ := cmd.Flags().GetString("project")
		if project == "" && len(args) > 0 {
			project = args[0]
		}
		reveal, _ := cmd.Flags().GetBool("reveal")

		if project == "" {
			projects, err := envListProjects()
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Println("No projects found.")
				return nil
			}
			for _, p := range projects {
				fmt.Printf("%-20s (%d secrets)\n", p.name, p.count)
			}
			return nil
		}

		entries, err := envListKeys(project, reveal)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Printf("No keys found for project %q.\n", project)
			return nil
		}
		for _, e := range entries {
			fmt.Printf("  %-20s = %s\n", e.key, e.maskedValue)
		}
		return nil
	},
}

func envListProjects() ([]projectInfo, error) {
	storage := viper.GetString(configKeyStorage)
	password := []byte(viper.GetString(configKeyPassword))

	b, err := backend.Backends[storage].NewContext(cmdContext, viper.AllSettings())
	if err != nil {
		return nil, fmt.Errorf("failed to create backend: %w", err)
	}
	exists, err := b.ExistsContext(cmdContext)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("store not initialized, run 'iscrt init' first")
	}

	data, err := b.LoadContext(cmdContext)
	if err != nil {
		return nil, err
	}
	s, err := store.ReadStoreContext(cmdContext, password, data)
	if err != nil {
		return nil, err
	}

	all := s.ListContext(cmdContext)
	counts := make(map[string]int)
	for _, key := range all {
		idx := strings.Index(key, "/")
		if idx < 0 {
			continue
		}
		project := key[:idx]
		counts[project]++
	}

	result := make([]projectInfo, 0, len(counts))
	for name, count := range counts {
		result = append(result, projectInfo{name: name, count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].name < result[j].name
	})
	return result, nil
}

func envListKeys(project string, reveal bool) ([]keyEntry, error) {
	storage := viper.GetString(configKeyStorage)
	password := []byte(viper.GetString(configKeyPassword))

	b, err := backend.Backends[storage].NewContext(cmdContext, viper.AllSettings())
	if err != nil {
		return nil, fmt.Errorf("failed to create backend: %w", err)
	}
	exists, err := b.ExistsContext(cmdContext)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("store not initialized, run 'iscrt init' first")
	}

	data, err := b.LoadContext(cmdContext)
	if err != nil {
		return nil, err
	}
	s, err := store.ReadStoreContext(cmdContext, password, data)
	if err != nil {
		return nil, err
	}

	prefix := project + "/"
	keys := s.ListPrefixContext(cmdContext, prefix)
	sort.Strings(keys)

	entries := make([]keyEntry, 0, len(keys))
	for _, fullKey := range keys {
		val, err := s.GetContext(cmdContext, fullKey)
		if err != nil {
			return nil, err
		}
		envKey := strings.TrimPrefix(fullKey, prefix)
		entries = append(entries, keyEntry{
			key:         envKey,
			maskedValue: maskValue(string(val), reveal),
		})
	}
	return entries, nil
}

func init() {
	addCommand(envCmd)

	envCmd.AddCommand(pushCmd)
	pushCmd.Flags().String("project", "", "project name (default: current directory name)")
	pushCmd.Flags().String("mode", "merge", "push mode: merge or replace")

	envCmd.AddCommand(pullCmd)
	pullCmd.Flags().String("project", "", "project name (default: current directory name)")
	pullCmd.Flags().BoolP("force", "f", false, "overwrite existing file")

	envCmd.AddCommand(listEnvCmd)
	listEnvCmd.Flags().String("project", "", "project name")
	listEnvCmd.Flags().BoolP("reveal", "r", false, "show full values instead of masked")

	envCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().String("project", "", "project name")
	deleteCmd.Flags().StringP("key", "k", "", "delete a single key instead of entire project")
	deleteCmd.Flags().BoolP("force", "f", false, "skip confirmation prompt")
}

// --- env delete ---

var deleteCmd = &cobra.Command{
	Use:   "delete [project]",
	Short: "Delete project secrets from the store",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, _ := cmd.Flags().GetString("project")
		if project == "" && len(args) > 0 {
			project = args[0]
		}
		if project == "" {
			project = currentDirName()
		}
		key, _ := cmd.Flags().GetString("key")
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			var target string
			if key != "" {
				target = fmt.Sprintf("key %q in project %q", key, project)
			} else {
				target = fmt.Sprintf("all secrets in project %q", project)
			}
			fmt.Printf("Delete %s? [y/N] ", target)
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		count, err := envDelete(project, key)
		if err != nil {
			return err
		}
		fmt.Printf("Deleted %d secret(s) from project %q\n", count, project)
		return nil
	},
}

func envDelete(project string, key string) (int, error) {
	storage := viper.GetString(configKeyStorage)
	password := []byte(viper.GetString(configKeyPassword))

	b, err := backend.Backends[storage].NewContext(cmdContext, viper.AllSettings())
	if err != nil {
		return 0, fmt.Errorf("failed to create backend: %w", err)
	}
	exists, err := b.ExistsContext(cmdContext)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("store not initialized, run 'iscrt init' first")
	}

	data, err := b.LoadContext(cmdContext)
	if err != nil {
		return 0, err
	}
	s, err := store.ReadStoreContext(cmdContext, password, data)
	if err != nil {
		return 0, err
	}

	var count int
	if key != "" {
		fullKey := project + "/" + key
		if s.HasContext(cmdContext, fullKey) {
			s.UnsetContext(cmdContext, fullKey)
			count = 1
		}
	} else {
		prefix := project + "/"
		count = s.UnsetPrefixContext(cmdContext, prefix)
	}

	data, err = store.WriteStoreContext(cmdContext, password, s)
	if err != nil {
		return 0, err
	}
	if err := b.SaveContext(cmdContext, data); err != nil {
		return 0, err
	}

	return count, nil
}
