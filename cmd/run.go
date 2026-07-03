package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/imaan/iscrt/backend"
	"github.com/imaan/iscrt/store"
)

// ExitCodeError carries a child process's exit status up to main, so iscrt
// exits with the same code the child did.
type ExitCodeError struct {
	Code int
}

func (e ExitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

var runCmd = newRunCmd()

// newRunCmd builds the run command. A factory (rather than a package-level
// literal) so tests can drive a fresh command without leaking parsed-flag
// state between cases.
func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run [flags] -- command [args...]",
		Short: "Run a command with project secrets injected into its environment",
		Long: `Run a command with the project's secrets injected into its environment.

Secrets are decrypted in memory and passed to the child process's environment
only: they are never printed, logged, or written to disk by iscrt. The master
password (SCRT_PASSWORD) is always stripped from the child's environment.

The '--' separator is required; everything after it is the child command and
its arguments, passed through untouched.

Note that the child process (and anything it spawns) can read the injected
values. 'iscrt run' prevents accidental leaks through stdout, argv, and .env
files — it does not sandbox the child.`,
		Example: `  iscrt run -- npm run build
  iscrt run --only DATABASE_URL -- npx prisma migrate deploy
  iscrt run --project myapp -- ./deploy.sh`,
	}
	// Everything after "--" is the child command. Require the separator so
	// child flags can never be mistaken for iscrt flags. This must run as the
	// Args validator, not in RunE: root's PersistentPreRunE re-parses
	// os.Args[1:] to pick up backend flags, which clobbers ArgsLenAtDash
	// (the re-parse sees "run" itself as a positional arg before "--").
	// Cobra validates args before that re-parse, while the flagset is
	// pristine.
	c.Args = func(cmd *cobra.Command, args []string) error {
		dash := cmd.ArgsLenAtDash()
		if dash < 0 {
			return fmt.Errorf("missing '--' separator, usage: iscrt run [flags] -- command [args...]")
		}
		if dash > 0 {
			return fmt.Errorf("unexpected argument %q before '--'", args[0])
		}
		if len(args) == 0 {
			return fmt.Errorf("no command specified after '--'")
		}
		return nil
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			name, ok := gitRepoName()
			if !ok {
				return fmt.Errorf("not in a git repository — use --project to specify a project name")
			}
			project = name
		}

		secrets, err := loadProjectSecrets(project)
		if err != nil {
			return err
		}

		required, _ := cmd.Flags().GetString("require")
		if missing := missingKeys(secrets, splitKeys(required)); len(missing) > 0 {
			return fmt.Errorf(
				"required keys missing from project %q: %s",
				project, strings.Join(missing, ", "),
			)
		}

		only, _ := cmd.Flags().GetString("only")
		if allow := splitKeys(only); len(allow) > 0 {
			allowed := make(map[string]bool, len(allow))
			for _, k := range allow {
				allowed[k] = true
			}
			for k := range secrets {
				if !allowed[k] {
					delete(secrets, k)
				}
			}
		}
		except, _ := cmd.Flags().GetString("except")
		for _, k := range splitKeys(except) {
			delete(secrets, k)
		}

		noInherit, _ := cmd.Flags().GetBool("no-inherit")

		c := exec.Command(args[0], args[1:]...)
		c.Env = buildChildEnv(secrets, !noInherit)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr

		if err := c.Start(); err != nil {
			return err
		}

		// Forward signals so Ctrl-C / SIGTERM reach the child, and iscrt
		// stays alive to report the child's exit code.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case sig := <-sigCh:
					_ = c.Process.Signal(sig)
				case <-done:
					return
				}
			}
		}()

		err = c.Wait()
		close(done)
		signal.Stop(sigCh)

		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code := exitErr.ExitCode()
				if code < 0 {
					// Child was killed by a signal: conventional 128+N.
					if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
						code = 128 + int(status.Signal())
					} else {
						code = 1
					}
				}
				// The child already reported its own failure on stderr; do
				// not wrap it in a cobra "Error:" line.
				cmd.SilenceErrors = true
				return ExitCodeError{Code: code}
			}
			return err
		}
		return nil
	}

	c.Flags().String("project", "", "project name (default: git repo name)")
	c.Flags().String("only", "", "comma-separated allowlist: inject only these keys")
	c.Flags().String("except", "", "comma-separated denylist: inject all keys except these")
	c.Flags().String("require", "", "comma-separated keys that must exist in the project, or fail")
	c.Flags().Bool("no-inherit", false, "do not inherit the parent environment (child gets PATH and the secrets only)")

	return c
}

// loadProjectSecrets opens the backend, decrypts the store, and returns the
// project's secrets keyed by env var name (project prefix stripped). Values
// must never leave this process except through the child's environment.
func loadProjectSecrets(project string) (map[string]string, error) {
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
	if len(keys) == 0 {
		return nil, fmt.Errorf("no secrets found for project %q", project)
	}

	secrets := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := s.GetContext(cmdContext, key)
		if err != nil {
			return nil, err
		}
		secrets[strings.TrimPrefix(key, prefix)] = string(val)
	}
	return secrets, nil
}

// buildChildEnv assembles the child's environment: the parent environment
// (unless inherit is false) overlaid with the project secrets — the secret
// wins on collision. SCRT_PASSWORD is always stripped: the master password
// unlocks every project in the store and must never propagate to children.
func buildChildEnv(secrets map[string]string, inherit bool) []string {
	envMap := make(map[string]string)
	if inherit {
		for _, kv := range os.Environ() {
			if i := strings.Index(kv, "="); i >= 0 {
				envMap[kv[:i]] = kv[i+1:]
			}
		}
	} else {
		// Minimal environment: PATH only, so the command can be resolved.
		envMap["PATH"] = os.Getenv("PATH")
	}
	for k, v := range secrets {
		envMap[k] = v
	}
	delete(envMap, "SCRT_PASSWORD")

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}

// missingKeys returns the keys in required that are absent from secrets.
func missingKeys(secrets map[string]string, required []string) []string {
	var missing []string
	for _, k := range required {
		if _, ok := secrets[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// splitKeys splits a comma-separated key list, trimming whitespace and
// dropping empty entries.
func splitKeys(s string) []string {
	if s == "" {
		return nil
	}
	var keys []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			keys = append(keys, p)
		}
	}
	return keys
}

func init() {
	addCommand(runCmd)
}
