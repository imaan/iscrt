package cmd

import (
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/viper"

	"github.com/imaan/iscrt/backend"
	"github.com/imaan/iscrt/store"
)

const testRunPassword = "toto"

// skipIfWindows: run tests spawn a real sh, so they are Unix-only.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("iscrt run tests require sh")
	}
}

// setupRunStore configures viper and a mock backend serving an encrypted
// store with the given project secrets.
func setupRunStore(t *testing.T, ctrl *gomock.Controller, secrets map[string]string) {
	t.Helper()

	mockBackend := NewMockBackend(ctrl)
	backend.Backends["mock"] = newMockFactory(mockBackend)

	viper.Reset()
	viper.Set(configKeyPassword, testRunPassword)
	viper.Set(configKeyStorage, "mock")

	s := store.NewStore()
	for k, v := range secrets {
		if err := s.Set(k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := store.WriteStore([]byte(testRunPassword), s)
	if err != nil {
		t.Fatal(err)
	}

	mockBackend.EXPECT().ExistsContext(ctxMatcher).Return(true, nil)
	mockBackend.EXPECT().LoadContext(ctxMatcher).Return(data, nil)
}

// execRun drives a fresh run command the way cobra would: parse flags (which
// sets ArgsLenAtDash), validate args, then invoke RunE with the positional
// args. A fresh command per call because pflag never resets argsLenAtDash on
// re-parse.
func execRun(t *testing.T, argv ...string) error {
	t.Helper()

	c := newRunCmd()
	if err := c.ParseFlags(argv); err != nil {
		t.Fatal(err)
	}
	args := c.Flags().Args()
	if err := c.Args(c, args); err != nil {
		return err
	}
	return c.RunE(c, args)
}

// readHijackedStdout closes the hijacked stdout write end and returns
// everything written to it.
func readHijackedStdout(t *testing.T) string {
	t.Helper()
	_ = os.Stdout.Close()
	data, err := io.ReadAll(hijackStdout)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunCmdInjectsSecrets(t *testing.T) {
	skipIfWindows(t)
	hijack()
	defer restore()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

	err := execRun(t,
		"--project", "testproj", "--",
		"sh", "-c", `printf "%s" "$MY_KEY"`,
	)
	if err != nil {
		t.Fatal(err)
	}

	if out := readHijackedStdout(t); out != "world" {
		t.Fatalf("expected %q, got %q", "world", out)
	}
}

func TestRunCmdStripsPassword(t *testing.T) {
	skipIfWindows(t)

	for _, extraFlags := range [][]string{nil, {"--no-inherit"}} {
		name := "inherit"
		if len(extraFlags) > 0 {
			name = "no-inherit"
		}
		t.Run(name, func(t *testing.T) {
			hijack()
			defer restore()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

			t.Setenv("SCRT_PASSWORD", testRunPassword)

			argv := append([]string{"--project", "testproj"}, extraFlags...)
			argv = append(argv, "--", "sh", "-c", `printf "%s" "${SCRT_PASSWORD:-ABSENT}"`)
			err := execRun(t, argv...)
			if err != nil {
				t.Fatal(err)
			}

			if out := readHijackedStdout(t); out != "ABSENT" {
				t.Fatalf("SCRT_PASSWORD leaked into child env: got %q", out)
			}
		})
	}
}

func TestRunCmdOnlyExcept(t *testing.T) {
	skipIfWindows(t)

	secrets := map[string]string{
		"testproj/KEY_A": "aaa",
		"testproj/KEY_B": "bbb",
		"testproj/KEY_C": "ccc",
	}
	script := `printf "%s|%s|%s" "${KEY_A:-X}" "${KEY_B:-X}" "${KEY_C:-X}"`

	cases := []struct {
		name     string
		flags    []string
		expected string
	}{
		{"only", []string{"--only", "KEY_A,KEY_B"}, "aaa|bbb|X"},
		{"except", []string{"--except", "KEY_B"}, "aaa|X|ccc"},
		{"only-and-except", []string{"--only", "KEY_A,KEY_B", "--except", "KEY_A"}, "X|bbb|X"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hijack()
			defer restore()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			setupRunStore(t, ctrl, secrets)

			argv := append([]string{"--project", "testproj"}, tc.flags...)
			argv = append(argv, "--", "sh", "-c", script)
			if err := execRun(t, argv...); err != nil {
				t.Fatal(err)
			}

			if out := readHijackedStdout(t); out != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, out)
			}
		})
	}
}

func TestRunCmdRequireMissing(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

	err := execRun(t,
		"--project", "testproj", "--require", "MY_KEY,DATABASE_URL", "--",
		"sh", "-c", "true",
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected missing key named in error, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "world") {
		t.Fatalf("secret value leaked into error message: %q", err.Error())
	}
}

func TestRunCmdRequireAfterFiltering(t *testing.T) {
	skipIfWindows(t)

	// --require must be evaluated after --only/--except: a key that exists
	// in the store but is filtered out must fail the require check, because
	// it never reaches the child.
	cases := []struct {
		name  string
		flags []string
	}{
		{"except-drops-required", []string{"--require", "MY_KEY", "--except", "MY_KEY"}},
		{"only-drops-required", []string{"--require", "MY_KEY", "--only", "OTHER_KEY"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			setupRunStore(t, ctrl, map[string]string{
				"testproj/MY_KEY":    "world",
				"testproj/OTHER_KEY": "other",
			})

			argv := append([]string{"--project", "testproj"}, tc.flags...)
			argv = append(argv, "--", "sh", "-c", "true")
			err := execRun(t, argv...)
			if err == nil {
				t.Fatal("expected require to fail for a filtered-out key")
			}
			if !strings.Contains(err.Error(), "MY_KEY") {
				t.Fatalf("expected MY_KEY named in error, got %q", err.Error())
			}
		})
	}
}

func TestRunCmdNoInherit(t *testing.T) {
	skipIfWindows(t)
	hijack()
	defer restore()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

	t.Setenv("ISCRT_TEST_PARENT_VAR", "leaked")

	err := execRun(t,
		"--project", "testproj", "--no-inherit", "--",
		"sh", "-c", `printf "%s|%s" "${ISCRT_TEST_PARENT_VAR:-ABSENT}" "$MY_KEY"`,
	)
	if err != nil {
		t.Fatal(err)
	}

	if out := readHijackedStdout(t); out != "ABSENT|world" {
		t.Fatalf("expected %q, got %q", "ABSENT|world", out)
	}
}

func TestRunCmdExitCode(t *testing.T) {
	skipIfWindows(t)
	hijack()
	defer restore()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

	err := execRun(t,
		"--project", "testproj", "--",
		"sh", "-c", "exit 7",
	)
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if exitErr.Code != 7 {
		t.Fatalf("expected exit code 7, got %d", exitErr.Code)
	}

	// iscrt's own output must never contain a secret value.
	if out := readHijackedStdout(t); strings.Contains(out, "world") {
		t.Fatalf("secret value leaked into output: %q", out)
	}
}

func TestRunCmdChildFlagsUntouched(t *testing.T) {
	skipIfWindows(t)
	hijack()
	defer restore()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "world"})

	// Flags after "--" must reach the child untouched, even ones that
	// collide with iscrt's own flags (-p, --storage).
	err := execRun(t,
		"--project", "testproj", "--",
		"sh", "-c", `printf "%s %s" "$1" "$2"`, "sh", "-p", "--storage",
	)
	if err != nil {
		t.Fatal(err)
	}

	if out := readHijackedStdout(t); out != "-p --storage" {
		t.Fatalf("expected %q, got %q", "-p --storage", out)
	}
}

func TestRunCmdMissingDash(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	viper.Reset()
	viper.Set(configKeyPassword, testRunPassword)
	viper.Set(configKeyStorage, "mock")

	err := execRun(t, "sh", "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--") {
		t.Fatalf("expected missing '--' error, got %q", err.Error())
	}
}

func TestRunCmdNoCommand(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	viper.Reset()
	viper.Set(configKeyPassword, testRunPassword)
	viper.Set(configKeyStorage, "mock")

	err := execRun(t, "--")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Fatalf("expected 'no command' error, got %q", err.Error())
	}
}

func TestRunCmdArgBeforeDash(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	viper.Reset()
	viper.Set(configKeyPassword, testRunPassword)
	viper.Set(configKeyStorage, "mock")

	err := execRun(t, "stray", "--", "sh", "-c", "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "stray") {
		t.Fatalf("expected stray-argument error, got %q", err.Error())
	}
}

func TestRunCmdNotGitRepo(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	viper.Reset()
	viper.Set(configKeyPassword, testRunPassword)
	viper.Set(configKeyStorage, "mock")

	t.Chdir(t.TempDir())

	err := execRun(t, "--", "sh", "-c", "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Fatalf("expected not-a-git-repo error, got %q", err.Error())
	}
}

func TestRunCmdNoSecretsForProject(t *testing.T) {
	skipIfWindows(t)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"otherproj/MY_KEY": "world"})

	err := execRun(t, "--project", "testproj", "--", "sh", "-c", "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "testproj") {
		t.Fatalf("expected no-secrets error, got %q", err.Error())
	}
}

func TestRunCmdSecretOverridesParentEnv(t *testing.T) {
	skipIfWindows(t)
	hijack()
	defer restore()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	setupRunStore(t, ctrl, map[string]string{"testproj/MY_KEY": "from-store"})

	t.Setenv("MY_KEY", "from-parent")

	err := execRun(t,
		"--project", "testproj", "--",
		"sh", "-c", `printf "%s" "$MY_KEY"`,
	)
	if err != nil {
		t.Fatal(err)
	}

	if out := readHijackedStdout(t); out != "from-store" {
		t.Fatalf("expected secret to win on collision, got %q", out)
	}
}
