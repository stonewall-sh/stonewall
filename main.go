// Command stonewall launches an AI coding agent inside a kernel-enforced sandbox.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/stonewall-sh/stonewall/v2/internal/policy"
	"github.com/stonewall-sh/stonewall/v2/internal/sandbox"
	"gopkg.in/yaml.v3"
)

// version is stamped by GoReleaser through -ldflags "-X main.version=…". A `go install` build has
// no ldflags but carries the module version in its build info.
var version = "dev"

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

// out is stonewall's own stderr UI. Built plain in main before Execute, then refined by
// PersistentPreRunE once --plain is known. The initial value is used only on flag-parse errors,
// which never reach PersistentPreRunE; it is still TTY-aware.
var out ui

// httpClient fetches remote policies and the index; nil means the default. Tests inject an httptest client.
var httpClient *http.Client

// exitError carries the process exit code out of RunE.
type exitError struct {
	code int
	err  error // nil when only the code matters (agent exit status)
}

func (e exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit %d", e.code)
}

func newRootCmd() *cobra.Command {
	var policyPath string
	var dryRun, plain bool

	cmd := &cobra.Command{
		Use:   "stonewall [options] <agent> [agent args...]",
		Short: "Kernel-enforced sandbox for AI coding agents",
		Long: "Stonewall is a local sandbox for AI coding agents, drastically limiting access to tools, paths and " +
			"project files, based on strictly enforced policies.\n" +
			"\n" +
			"Launches the <agent> inside a sandbox. What the agent can see, change and run is defined in the .stonewall.yml policy.",
		Example: "  stonewall claude                  Run Claude Code in a stonewall sandbox\n" +
			"  stonewall claude --resume         Pass arguments to the agent\n" +
			"  stonewall -n codex                Print sandbox config, launch nothing\n" +
			"  stonewall -p ci.yml codex         Use another policy file\n" +
			"  stonewall sh -c 'ls ~'            Run any command within the sandbox",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			out = newUI(os.Stderr, plain)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return launch(args, policyPath, dryRun)
		},
	}
	cmd.SetVersionTemplate("stonewall {{.Version}}\n")
	cmd.Flags().SetInterspersed(false)
	// --policy and --plain are persistent so the policy subcommands take them too.
	cmd.PersistentFlags().StringVarP(&policyPath, "policy", "p", "", "Use `FILE` instead of discovered "+policy.FileName)
	cmd.PersistentFlags().BoolVar(&plain, "plain", false, "No colour / formatting in output")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Print the sandbox command and exit")
	cmd.Flags().BoolP("version", "v", false, "Print the version") // cobra handles them, these only word the help
	cmd.PersistentFlags().BoolP("help", "h", false, "Show this help")
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// Help pages are cobra's own, rendered from Use, Short, Long and Example, in stonewall's style. Colour
	// follows stdout, where help goes, and the --plain flag, which is parsed before help runs.
	paint := func(code string) func(string) string {
		return func(s string) string { return newUI(os.Stdout, plain).paint(code, s) }
	}
	cobra.AddTemplateFunc("orange", paint("1;38;5;202"))
	cobra.AddTemplateFunc("bold", paint("1"))
	cobra.AddTemplateFunc("dim", paint("2"))
	cobra.AddTemplateFunc("useLine", func(c *cobra.Command) string { return strings.TrimSuffix(c.UseLine(), " [flags]") })
	cmd.SetHelpTemplate(`
{{orange .Root.Name}} {{dim (print .Root.Version "  " .Root.Short)}}

{{with .Long}}{{.}}

{{end}}{{.UsageString}}`)
	cmd.SetUsageTemplate(`{{orange "USAGE"}}{{if or (not .HasParent) (not .HasAvailableSubCommands)}}
  {{useLine .}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <command>{{end}}{{if .HasExample}}

{{orange "EXAMPLES"}}
{{.Example}}{{end}}{{if .HasAvailableLocalFlags}}

{{orange "OPTIONS"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{orange "GLOBAL OPTIONS"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{orange "COMMANDS"}}{{range .Commands}}{{if .IsAvailableCommand}}
  {{bold (rpad .Name .NamePadding)}}  {{.Short}}{{end}}{{end}}

  Use "{{.CommandPath}} <command> --help" for more about a command.{{end}}

Stonewall.sh is open-source software licensed under MIT. Visit {{dim "https://stonewall.sh"}} for more information or contribute on {{dim "https://github.com/stonewall-sh/stonewall"}}.

`)

	policyCmd := &cobra.Command{
		Use:   "policy",
		Short: "Manage the project policy",
		Args:  cobra.NoArgs, // runnable, so an unknown subcommand is an error instead of this help page
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	policyCmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Refresh cached remote policies",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return updatePolicies(policyPath) },
	})
	policyCmd.AddCommand(&cobra.Command{
		Use:   "include <url|path>",
		Short: "Add a policy to the include list; a remote one is reviewed and cached right away",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return includePolicy(policyPath, args[0]) },
	})
	policyCmd.AddCommand(&cobra.Command{
		Use:   "remove <url|path>",
		Short: "Drop a policy from the include list",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return removePolicy(policyPath, args[0]) },
	})
	policyCmd.AddCommand(&cobra.Command{
		Use:   "pick",
		Short: "Pick official policies to include, interactively",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return pickPolicies(policyPath) },
	})
	policyCmd.AddCommand(&cobra.Command{
		Use:   "validate <url|path>",
		Short: "Check a policy against the schema at " + policy.SchemaURL + ", without including it",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return validatePolicy(args[0]) },
	})
	cmd.AddCommand(policyCmd)

	return cmd
}

// newLoader builds the policy loader with stonewall's own review prompt, reporting a refresh
// accepted at the staleness prompt the way `policy update` does.
func newLoader() policy.Loader {
	return policy.Loader{
		Client: httpClient,
		Ask: func(title, body, question string) bool {
			out.block(title, body)
			return out.confirm(question)
		},
		Updating: func() { fmt.Fprintln(out.w, out.paint("1", "Updating remote policies...")) },
		Report:   func(results []policy.UpdateResult) { reportUpdates(results) },
	}
}

// reportUpdates prints one row per refreshed policy, an error line per failure, and for a refused policy
// the statement with the way out, since that is a decision, not an error.
func reportUpdates(results []policy.UpdateResult) {
	var rows [][2]string
	for _, r := range results {
		if r.Status != policy.Failed && r.Status != policy.Untrusted {
			rows = append(rows, [2]string{r.Status, r.URL})
		}
	}
	out.rows(rows...)
	for _, r := range results {
		switch r.Status {
		case policy.Failed:
			out.error(fmt.Errorf("%s: %w", r.URL, r.Err))
		case policy.Untrusted:
			fmt.Fprintf(out.w, "%s\n\nTo remove the include, please run:\nstonewall policy remove %s\n", out.paint("1", "Policy include "+r.URL+" not trusted."), r.URL)
		}
	}
}

// notTrusted reports whether err is a refused remote policy, which reportUpdates has already explained.
func notTrusted(err error) bool {
	var nt policy.NotTrusted
	return errors.As(err, &nt)
}

// updatePolicies refreshes the cached remote policies of the project's policy file.
func updatePolicies(policyPath string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return exitError{1, err}
	}
	if policyPath == "" {
		policyPath = filepath.Join(policy.FindRoot(cwd), policy.FileName)
	}
	results, err := newLoader().Update(policyPath)
	reportUpdates(results)
	switch {
	case notTrusted(err):
		return exitError{code: 1}
	case err != nil && len(results) > 0:
		return exitError{code: 1} // the failures are already reported
	case err != nil:
		return exitError{1, err}
	}
	return nil
}

// includeRef returns the policy file to edit and inc as it is written there: a path given relative to the
// caller becomes relative to the policy file.
func includeRef(policyPath, inc string) (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	if policyPath == "" {
		policyPath = filepath.Join(policy.FindRoot(cwd), policy.FileName)
	}
	if strings.Contains(inc, "://") || strings.HasPrefix(inc, "~/") || filepath.IsAbs(inc) {
		return policyPath, inc, nil
	}
	abs, err := filepath.Abs(inc)
	if err != nil {
		return "", "", err
	}
	dir, err := filepath.Abs(filepath.Dir(policyPath))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(dir, abs)
	return policyPath, rel, err
}

// includePolicy adds inc to the policy file's include list and runs the update, so a new remote policy is
// reviewed and cached now.
func includePolicy(policyPath, inc string) error {
	policyPath, inc, err := includeRef(policyPath, inc)
	if err != nil {
		return exitError{1, err}
	}
	added, results, err := newLoader().Include(policyPath, inc)
	if added {
		fmt.Fprintln(out.w, out.paint("1", "Added policy include for "+inc+" to "+policyPath+"."))
	} else if err == nil {
		fmt.Fprintln(out.w, out.paint("1", "Policy include for "+inc+" already in "+policyPath+"."))
	}
	reportUpdates(results)
	switch {
	case notTrusted(err):
		return exitError{code: 1}
	case err != nil && len(results) > 0:
		return exitError{code: 1}
	case err != nil:
		return exitError{1, err}
	}
	return nil
}

// pickPolicies lists the official policies from the index as a checkbox list, ticked where the project
// already includes them, and applies the difference: unticked ones are removed, newly ticked ones are added
// and reviewed right away, as `policy include` does.
func pickPolicies(policyPath string) error {
	if !interactive() {
		return exitError{1, errors.New("pick needs a terminal; use stonewall policy include <url>")}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return exitError{1, err}
	}
	if policyPath == "" {
		policyPath = filepath.Join(policy.FindRoot(cwd), policy.FileName)
	}
	local, err := policy.Load(policyPath)
	if err != nil {
		return exitError{1, err}
	}
	l := newLoader()
	index, err := l.Index()
	if err != nil {
		return exitError{1, err}
	}
	if len(index) == 0 {
		return exitError{1, fmt.Errorf("%s lists no policies", policy.IndexURL)}
	}
	items := make([]pickItem, len(index))
	for i, e := range index {
		label := e.Name
		if label == "" {
			label = e.URL
		}
		items[i] = pickItem{Label: label, Detail: e.Description, Checked: slices.Contains(local.Include, e.URL)}
	}
	title := "official policies from " + strings.TrimSuffix(policy.IndexURL, "/_index.yml")
	checked, ok := out.pick(title, "↑↓ move · space toggle · enter apply · esc cancel", items)
	if !ok {
		fmt.Fprintln(out.w, out.paint("1", "Nothing changed."))
		return nil
	}
	added := false
	for i, e := range index {
		switch {
		case checked[i] && !items[i].Checked:
			if _, err := policy.AddInclude(policyPath, e.URL); err != nil {
				return exitError{1, err}
			}
			fmt.Fprintln(out.w, out.paint("1", "Added policy include for "+e.URL+" to "+policyPath+"."))
			added = true
		case !checked[i] && items[i].Checked:
			if _, err := policy.Remove(policyPath, e.URL); err != nil {
				return exitError{1, err}
			}
			fmt.Fprintln(out.w, out.paint("1", "Removed policy include for "+e.URL+" from "+policyPath+"."))
		}
	}
	if !added {
		return nil
	}
	results, err := l.Update(policyPath)
	reportUpdates(results)
	switch {
	case notTrusted(err):
		return exitError{code: 1}
	case err != nil && len(results) > 0:
		return exitError{code: 1}
	case err != nil:
		return exitError{1, err}
	}
	return nil
}

// removePolicy drops inc from the policy file's include list, with its lock entry and cache file.
func removePolicy(policyPath, inc string) error {
	policyPath, inc, err := includeRef(policyPath, inc)
	if err != nil {
		return exitError{1, err}
	}
	removed, err := policy.Remove(policyPath, inc)
	if err != nil {
		return exitError{1, err}
	}
	if !removed {
		return exitError{1, fmt.Errorf("%s is not in the include list of %s", inc, policyPath)}
	}
	fmt.Fprintln(out.w, out.paint("1", "Removed policy include for "+inc+" from "+policyPath+"."))
	return nil
}

// validatePolicy checks one policy, local or remote, against the published schema and reports every violation.
func validatePolicy(ref string) error {
	if err := newLoader().Validate(ref); err != nil {
		return exitError{1, fmt.Errorf("%s: %w", ref, err)}
	}
	fmt.Fprintln(out.w, out.paint("1", ref+" is a valid policy."))
	return nil
}

func main() {
	out = newUI(os.Stderr, false)
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			if ee.err != nil {
				out.error(ee.err)
			}
			os.Exit(ee.code)
		}
		out.error(err) // cobra usage/flag error
		fmt.Fprintln(os.Stderr, "Run 'stonewall --help' for usage.")
		os.Exit(2)
	}
}

// launch runs the agent inside the sandbox: root discovery, policy scaffold and include resolution,
// sandbox build, backend selection, exec and signal handling. args[0] is the agent; the rest pass through.
func launch(args []string, policyPath string, dryRun bool) error {
	fail := func(err error) error { return exitError{1, err} }

	cwd, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	root := policy.FindRoot(cwd)
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(err)
	}
	if same(root, home) || root == filepath.Dir(root) {
		return fail(fmt.Errorf("project root %s is your home directory or /; run stonewall inside a project (a directory holding .git or %s)", root, policy.FileName))
	}
	created := false
	if policyPath == "" {
		policyPath = filepath.Join(root, policy.FileName)
		if _, err := os.Stat(policyPath); errors.Is(err, os.ErrNotExist) {
			if err := policy.WriteScaffold(policyPath, policy.Scaffold(args[0])); err != nil {
				return fail(err)
			}
			out.block("Created initial policy "+policyPath, "Change it to your needs.")
			created = true
		}
	}
	eff, localFiles, err := newLoader().Load(policyPath)
	if notTrusted(err) {
		return exitError{code: 1}
	}
	if err != nil {
		return fail(err)
	}
	plan, err := sandbox.Build(eff, root, cwd, append([]string{policyPath}, localFiles...), args)
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(plan.BinDir)

	var cmd *exec.Cmd
	var backend string
	switch runtime.GOOS {
	case "linux":
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return fail(errors.New("bubblewrap is required on Linux and was not found on PATH. Install it with your package manager: apt install bubblewrap, dnf install bubblewrap, or pacman -S bubblewrap"))
		}
		cmd = exec.Command(bwrap, sandbox.BwrapArgs(plan)...)
		backend = "bwrap"
	case "darwin":
		cmd = exec.Command("/usr/bin/sandbox-exec", sandbox.SeatbeltArgs(plan)...)
		backend = "sandbox-exec"
	default:
		return fail(fmt.Errorf("unsupported OS %s: stonewall runs on Linux and macOS", runtime.GOOS))
	}

	policyDisplay := policyPath
	if rel, err := filepath.Rel(root, policyPath); err == nil && !strings.HasPrefix(rel, "..") {
		policyDisplay = rel
	}
	if created {
		policyDisplay += " " + out.paint("32", "(created)")
	}
	if dryRun {
		backend += " (dry run)"
	}
	out.rows(
		[2]string{"project", root},
		[2]string{"policy", policyDisplay},
		[2]string{"sandbox", backend},
	)
	for _, w := range plan.Warnings {
		out.warn(w)
	}

	if dryRun {
		b, err := yaml.Marshal(eff)
		if err != nil {
			return fail(err)
		}
		out.block("effective policy", string(b))
		fmt.Println(shellJoin(cmd.Args))
		return nil
	}
	cmd.Env = plan.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Ctrl-C reaches the agent through the terminal (it already delivered SIGINT to the whole
	// foreground process group); stonewall forwards only termination signals.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	go func() {
		for s := range sigs {
			if s != syscall.SIGINT { // the terminal already sent Ctrl-C to the agent's process group
				cmd.Process.Signal(s)
			}
		}
	}()
	err = cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return exitError{code: 128 + int(ws.Signal())}
		}
		return exitError{code: exit.ExitCode()}
	}
	if err != nil {
		return fail(err)
	}
	return nil
}

// same reports whether two paths name the same directory after resolving symlinks.
func same(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// plainArg matches args that never need shell quoting.
var plainArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellJoin quotes args so the line can be pasted into a shell.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if !plainArg.MatchString(a) {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}
