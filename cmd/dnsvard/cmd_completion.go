package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	completionBlockBegin = "# >>> dnsvard completion >>>"
	completionBlockEnd   = "# <<< dnsvard completion <<<"
)

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate and install shell completion scripts",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	installShell := ""
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install completion for current shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shell, err := resolvedCompletionShell(installShell)
			if err != nil {
				return err
			}
			home := configHomeDir()
			target, err := completionInstallTarget(shell, home)
			if err != nil {
				return err
			}
			if strings.TrimSpace(target.scriptPath) != "" {
				script, scriptErr := completionScript(root, shell)
				if scriptErr != nil {
					return scriptErr
				}
				if err := os.MkdirAll(filepath.Dir(target.scriptPath), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(target.scriptPath, []byte(script), 0o644); err != nil {
					return err
				}
			}

			rcUpdatedByPath := map[string]bool{}
			for _, rcPath := range target.rcPaths {
				changed, updateErr := upsertManagedBlock(rcPath, completionBlockBegin, completionBlockEnd, target.rcBlock)
				if updateErr != nil {
					return updateErr
				}
				rcUpdatedByPath[rcPath] = changed
			}

			if strings.TrimSpace(target.scriptPath) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "installed %s completion script: %s\n", shell, userPath(target.scriptPath))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "installed %s completion via eval hook\n", shell)
			}
			if len(target.rcPaths) > 0 {
				for _, rcPath := range target.rcPaths {
					if rcUpdatedByPath[rcPath] {
						fmt.Fprintf(cmd.OutOrStdout(), "updated shell rc: %s\n", userPath(rcPath))
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "shell rc already configured: %s\n", userPath(rcPath))
				}
			}
			if shell == "bash" && bashUsesBashRC(target.rcPaths) {
				fmt.Fprintln(cmd.OutOrStdout(), "note: if your login shell does not load ~/.bashrc, add this to ~/.bash_profile:")
				fmt.Fprintln(cmd.OutOrStdout(), "if [[ -f ~/.bashrc ]]; then")
				fmt.Fprintln(cmd.OutOrStdout(), "  source ~/.bashrc")
				fmt.Fprintln(cmd.OutOrStdout(), "fi")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "open a new shell or run `source` on your rc file")
			return nil
		},
	}
	installCmd.Flags().StringVar(&installShell, "shell", "", "Shell to configure (bash|zsh|fish|powershell); defaults to current shell")

	statusShell := ""
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show completion install status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shell, err := resolvedCompletionShell(statusShell)
			if err != nil {
				return err
			}
			home := configHomeDir()
			target, err := completionInstallTarget(shell, home)
			if err != nil {
				return err
			}

			scriptRequired := strings.TrimSpace(target.scriptPath) != ""
			scriptInstalled := !scriptRequired || fileExists(target.scriptPath)
			rcConfigured := len(target.rcPaths) == 0
			for _, rcPath := range target.rcPaths {
				hasBlock, blockErr := hasManagedBlock(rcPath, completionBlockBegin, completionBlockEnd)
				if blockErr != nil {
					return blockErr
				}
				if hasBlock {
					rcConfigured = true
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "shell: %s\n", shell)
			if scriptRequired {
				fmt.Fprintf(cmd.OutOrStdout(), "script: %s (installed=%t)\n", userPath(target.scriptPath), scriptInstalled)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "mode: eval\n")
			}
			if len(target.rcPaths) > 0 {
				for _, rcPath := range target.rcPaths {
					hasBlock, _ := hasManagedBlock(rcPath, completionBlockBegin, completionBlockEnd)
					fmt.Fprintf(cmd.OutOrStdout(), "rc: %s (configured=%t)\n", userPath(rcPath), hasBlock)
				}
			}
			if scriptInstalled && rcConfigured {
				fmt.Fprintln(cmd.OutOrStdout(), "status: ready")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "status: not ready\n")
			fmt.Fprintf(cmd.OutOrStdout(), "fix: dnsvard completion install --shell %s\n", shell)
			return nil
		},
	}
	statusCmd.Flags().StringVar(&statusShell, "shell", "", "Shell to inspect (bash|zsh|fish|powershell); defaults to current shell")

	uninstallShell := ""
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove completion setup for a shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shell, err := resolvedCompletionShell(uninstallShell)
			if err != nil {
				return err
			}
			home := configHomeDir()
			target, err := completionInstallTarget(shell, home)
			if err != nil {
				return err
			}

			if strings.TrimSpace(target.scriptPath) != "" {
				if err := removeFileIfExists(target.scriptPath); err != nil {
					return err
				}
			}
			for _, rcPath := range completionUninstallRCPaths(shell, home, target.rcPaths) {
				if _, err := removeManagedBlock(rcPath, completionBlockBegin, completionBlockEnd); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s completion setup\n", shell)
			return nil
		},
	}
	uninstallCmd.Flags().StringVar(&uninstallShell, "shell", "", "Shell to clean up (bash|zsh|fish|powershell); defaults to current shell")

	bashCmd := &cobra.Command{
		Use:   "bash",
		Short: "Generate bash completion script",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := completionScript(root, "bash")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		},
	}

	zshCmd := &cobra.Command{
		Use:   "zsh",
		Short: "Generate zsh completion script",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := completionScript(root, "zsh")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		},
	}

	fishCmd := &cobra.Command{
		Use:   "fish",
		Short: "Generate fish completion script",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := completionScript(root, "fish")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		},
	}

	powershellCmd := &cobra.Command{
		Use:   "powershell",
		Short: "Generate PowerShell completion script",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := completionScript(root, "powershell")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		},
	}

	cmd.AddCommand(installCmd, statusCmd, uninstallCmd, bashCmd, zshCmd, fishCmd, powershellCmd)
	return cmd
}

type completionTarget struct {
	shell      string
	scriptPath string
	rcPaths    []string
	rcBlock    string
}

func completionInstallTarget(shell string, home string) (completionTarget, error) {
	home = strings.TrimSpace(home)
	if home == "" || home == "." {
		return completionTarget{}, errors.New("cannot resolve home directory for completion install")
	}
	shell, err := completionShellFromValue(shell)
	if err != nil {
		return completionTarget{}, err
	}
	switch shell {
	case "bash":
		rcPath := bashCompletionRCPath(home)
		return completionTarget{
			shell:   shell,
			rcPaths: []string{rcPath},
			rcBlock: "if command -v dnsvard >/dev/null 2>&1; then\n" +
				"  eval \"$(dnsvard completion bash)\"\n" +
				"fi",
		}, nil
	case "zsh":
		return completionTarget{
			shell:   shell,
			rcPaths: []string{filepath.Join(home, ".zshrc")},
			rcBlock: "if command -v dnsvard >/dev/null 2>&1; then\n" +
				"  eval \"$(dnsvard completion zsh)\"\n" +
				"fi",
		}, nil
	case "fish":
		return completionTarget{
			shell:      shell,
			scriptPath: filepath.Join(home, ".config", "fish", "completions", "dnsvard.fish"),
		}, nil
	case "powershell":
		return completionTarget{}, errors.New("powershell install is not supported automatically; run `dnsvard completion powershell` and follow your profile setup")
	default:
		return completionTarget{}, fmt.Errorf("unsupported shell %q", shell)
	}
}

func completionScript(root *cobra.Command, shell string) (string, error) {
	shell, err := completionShellFromValue(shell)
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	switch shell {
	case "bash":
		if err := root.GenBashCompletion(buf); err != nil {
			return "", err
		}
		return bashCompletionCompatShim + "\n" + buf.String(), nil
	case "zsh":
		if err := root.GenZshCompletion(buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	case "fish":
		if err := root.GenFishCompletion(buf, true); err != nil {
			return "", err
		}
		return buf.String(), nil
	case "powershell":
		if err := root.GenPowerShellCompletionWithDesc(buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

func completionShellFromValue(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "auto":
		return "", nil
	case "bash", "zsh", "fish", "powershell":
		return v, nil
	case "pwsh", "powershell.exe":
		return "powershell", nil
	default:
		return "", fmt.Errorf("unsupported shell %q (use bash|zsh|fish|powershell)", v)
	}
}

func resolvedCompletionShell(v string) (string, error) {
	normalized, err := completionShellFromValue(v)
	if err != nil {
		return "", err
	}
	if normalized != "" {
		return normalized, nil
	}
	fromEnv := filepath.Base(strings.TrimSpace(os.Getenv("SHELL")))
	normalized, err = completionShellFromValue(fromEnv)
	if err != nil || normalized == "" {
		return "", errors.New("unable to detect shell; rerun with --shell bash|zsh|fish|powershell")
	}
	return normalized, nil
}

func upsertManagedBlock(path string, begin string, end string, block string) (bool, error) {
	begin = strings.TrimSpace(begin)
	end = strings.TrimSpace(end)
	block = strings.TrimSpace(block)
	if begin == "" || end == "" || block == "" {
		return false, errors.New("invalid managed block input")
	}
	existing, err := readTextFileOrEmpty(path)
	if err != nil {
		return false, err
	}
	replacement := begin + "\n" + block + "\n" + end
	next, changed := replaceManagedBlock(existing, begin, end, replacement)
	if !changed {
		return false, nil
	}
	if err := writeTextFile(path, next); err != nil {
		return false, err
	}
	return true, nil
}

func removeManagedBlock(path string, begin string, end string) (bool, error) {
	existing, err := readTextFileOrEmpty(path)
	if err != nil {
		return false, err
	}
	next, changed := removeManagedBlockText(existing, strings.TrimSpace(begin), strings.TrimSpace(end))
	if !changed {
		return false, nil
	}
	if err := writeTextFile(path, next); err != nil {
		return false, err
	}
	return true, nil
}

func hasManagedBlock(path string, begin string, end string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	v := string(b)
	begin = strings.TrimSpace(begin)
	end = strings.TrimSpace(end)
	start := strings.Index(v, begin)
	stop := strings.Index(v, end)
	return start >= 0 && stop > start, nil
}

func replaceManagedBlock(existing string, begin string, end string, replacement string) (string, bool) {
	beginIdx := strings.Index(existing, begin)
	endIdx := strings.Index(existing, end)
	if beginIdx >= 0 && endIdx > beginIdx {
		replaceEnd := endIdx + len(end)
		next := existing[:beginIdx] + replacement + existing[replaceEnd:]
		next = normalizeTextForWrite(next)
		if next == normalizeTextForWrite(existing) {
			return existing, false
		}
		return next, true
	}
	base := strings.TrimRight(existing, "\n")
	if strings.TrimSpace(base) == "" {
		return replacement + "\n", true
	}
	return base + "\n\n" + replacement + "\n", true
}

func removeManagedBlockText(existing string, begin string, end string) (string, bool) {
	beginIdx := strings.Index(existing, begin)
	endIdx := strings.Index(existing, end)
	if beginIdx < 0 || endIdx <= beginIdx {
		return existing, false
	}
	replaceEnd := endIdx + len(end)
	next := existing[:beginIdx] + existing[replaceEnd:]
	next = normalizeTextForWrite(next)
	return next, true
}

func normalizeTextForWrite(v string) string {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	v = strings.TrimLeft(v, "\n")
	v = strings.TrimRight(v, "\n")
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return v + "\n"
}

func readTextFileOrEmpty(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func writeTextFile(path string, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), 0o644)
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

func fileExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return true
}

func bashCompletionRCPath(home string) string {
	bashProfile := filepath.Join(home, ".bash_profile")
	bashRC := filepath.Join(home, ".bashrc")
	profileExists := fileExists(bashProfile)
	rcExists := fileExists(bashRC)

	if profileExists && bashProfileSourcesBashRC(bashProfile) {
		return bashRC
	}
	if profileExists {
		return bashProfile
	}
	if rcExists {
		return bashRC
	}
	return bashProfile
}

func bashProfileSourcesBashRC(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(b)), ".bashrc")
}

func bashUsesBashRC(paths []string) bool {
	for _, p := range paths {
		if filepath.Base(strings.TrimSpace(p)) == ".bashrc" {
			return true
		}
	}
	return false
}

func completionUninstallRCPaths(shell string, home string, rcPaths []string) []string {
	paths := append([]string{}, rcPaths...)
	if shell == "bash" {
		paths = append(paths, filepath.Join(home, ".bash_profile"), filepath.Join(home, ".bashrc"))
	}
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		unique = append(unique, p)
	}
	return unique
}

const bashCompletionCompatShim = `# dnsvard: fallback helper for bash 3.x sessions without bash-completion runtime.
# keep @ inside a completion token (workspace/<name>@<project>)
COMP_WORDBREAKS=${COMP_WORDBREAKS//[@]/}
if ! declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
_get_comp_words_by_ref()
{
    local cur_ prev_ cword_
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -n)
                shift 2
                ;;
            --)
                shift
                break
                ;;
            -*)
                shift
                ;;
            *)
                break
                ;;
        esac
    done
    cword_=${COMP_CWORD:-0}
    words=("${COMP_WORDS[@]}")
    cur_="${words[$cword_]}"
    prev_=""
    if [ "$cword_" -gt 0 ]; then
        prev_="${words[$((cword_ - 1))]}"
    fi
    while [ "$#" -gt 0 ]; do
        case "$1" in
            cur) cur=$cur_ ;;
            prev) prev=$prev_ ;;
            words) words=("${words[@]}") ;;
            cword) cword=$cword_ ;;
        esac
        shift
    done
}
fi
`
