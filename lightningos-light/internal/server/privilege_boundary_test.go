package server

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type privilegeCallBudget struct {
	runSudo    int
	runSystemd int
	writeSudo  int
	restart    int
	power      int
	shell      int
}

// These are ceilings for the legacy boundary accepted at the start of Phase 0.
// Removing a call is always allowed. Adding or moving one requires an explicit
// inventory review and an intentional update to this table.
var legacyPrivilegeCallBudgets = map[string]privilegeCallBudget{
	"internal/server/app_upgrade.go":              {},
	"internal/server/apps_bark_wallet.go":         {},
	"internal/server/apps_bitcoincore.go":         {},
	"internal/server/apps_btcpay.go":              {},
	"internal/server/apps_cpuminer.go":            {},
	"internal/server/apps_cpuminer_status.go":     {},
	"internal/server/apps_docker.go":              {},
	"internal/server/apps_fedimint.go":            {},
	"internal/server/apps_lnd_access_compat.go":   {},
	"internal/server/apps_mempool.go":             {},
	"internal/server/apps_publicpool.go":          {},
	"internal/server/apps_robosats.go":            {},
	"internal/server/apps_storage_permissions.go": {},
	"internal/server/auth_enable.go":              {},
	"internal/server/bitcoin_local.go":            {},
	"internal/server/elements_mainchain.go":       {},
	"internal/server/elements_status.go":          {},
	"internal/server/firewall_status.go":          {},
	"internal/server/handlers.go":                 {},
	"internal/server/lnd_upgrade.go":              {},
	"internal/server/system_integrations.go":      {},
	"internal/server/terminal_status.go":          {},
	"internal/server/tor_upgrade.go":              {},
	"internal/system/smart.go":                    {},
	"internal/system/system.go":                   {},
}

var legacyWildcardSudoLines = map[string]struct{}{
	`install.sh:system_cmds="${systemctl_path} restart lnd, ${systemctl_path} restart --no-block lnd, ${systemctl_path} restart lightningos-manager, ${systemctl_path} restart postgresql, ${systemctl_path} is-active lightningos-lnd-upgrade, ${systemctl_path} is-active lightningos-app-upgrade, ${systemctl_path} reboot, ${systemctl_path} poweroff, ${LND_FIX_PERMS_SCRIPT}, ${smartctl_path} *, ${tee_path} /etc/lightningos/config.yaml"`: {},
	`install.sh:[[ -n "$apt_get_path" ]] && app_cmds+=("${apt_get_path} *")`:         {},
	`install.sh:[[ -n "$apt_path" ]] && app_cmds+=("${apt_path} *")`:                 {},
	`install.sh:[[ -n "$dpkg_path" ]] && app_cmds+=("${dpkg_path} *")`:               {},
	`install.sh:[[ -n "$systemd_run_path" ]] && app_cmds+=("${systemd_run_path} *")`: {},
	`install.sh:[[ -n "$ufw_path" ]] && app_cmds+=("${ufw_path} *")`:                 {},
	`install_existing.sh:${user} ALL=NOPASSWD: ${smartctl_path} *`:                   {},
	`install_existing.sh:system_cmds="${systemctl_path} restart lnd, ${systemctl_path} restart --no-block lnd, ${systemctl_path} restart lightningos-manager, ${systemctl_path} restart postgresql, ${systemctl_path} is-active lightningos-lnd-upgrade, ${systemctl_path} is-active lightningos-app-upgrade, ${systemctl_path} reboot, ${systemctl_path} poweroff, ${LND_FIX_PERMS_SCRIPT}, ${smartctl_path} *, ${tee_path} /etc/lightningos/config.yaml"`: {},
	`install_existing.sh:[[ -n "$apt_get_path" ]] && app_cmds+=("${apt_get_path} *")`:         {},
	`install_existing.sh:[[ -n "$apt_path" ]] && app_cmds+=("${apt_path} *")`:                 {},
	`install_existing.sh:[[ -n "$dpkg_path" ]] && app_cmds+=("${dpkg_path} *")`:               {},
	`install_existing.sh:[[ -n "$systemd_run_path" ]] && app_cmds+=("${systemd_run_path} *")`: {},
	`install_existing.sh:[[ -n "$ufw_path" ]] && app_cmds+=("${ufw_path} *")`:                 {},
	`install_existing_pi.sh:${user} ALL=NOPASSWD: ${smartctl_path} *`:                         {},
	`install_existing_pi.sh:system_cmds="${systemctl_path} restart lnd, ${systemctl_path} restart --no-block lnd, ${systemctl_path} restart lightningos-manager, ${systemctl_path} restart postgresql, ${systemctl_path} is-active lightningos-lnd-upgrade, ${systemctl_path} is-active lightningos-app-upgrade, ${systemctl_path} reboot, ${systemctl_path} poweroff, ${LND_FIX_PERMS_SCRIPT}, ${smartctl_path} *, ${tee_path} /etc/lightningos/config.yaml"`: {},
	`install_existing_pi.sh:[[ -n "$apt_get_path" ]] && app_cmds+=("${apt_get_path} *")`:         {},
	`install_existing_pi.sh:[[ -n "$apt_path" ]] && app_cmds+=("${apt_path} *")`:                 {},
	`install_existing_pi.sh:[[ -n "$dpkg_path" ]] && app_cmds+=("${dpkg_path} *")`:               {},
	`install_existing_pi.sh:[[ -n "$systemd_run_path" ]] && app_cmds+=("${systemd_run_path} *")`: {},
	`install_existing_pi.sh:[[ -n "$ufw_path" ]] && app_cmds+=("${ufw_path} *")`:                 {},
	`internal/server/assets/upgrade-app.sh:system_cmds="${SYSTEMCTL_BIN} restart lnd, ${SYSTEMCTL_BIN} restart --no-block lnd, ${SYSTEMCTL_BIN} restart lightningos-manager, ${SYSTEMCTL_BIN} restart postgresql, ${SYSTEMCTL_BIN} is-active lightningos-lnd-upgrade, ${SYSTEMCTL_BIN} is-active lightningos-app-upgrade, ${SYSTEMCTL_BIN} reboot, ${SYSTEMCTL_BIN} poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, ${TEE_BIN} /etc/lightningos/config.yaml, ${SMARTCTL_BIN} *"`: {},
	`internal/server/assets/upgrade-app.sh:[[ -n "${APT_GET_BIN:-}" ]] && app_cmds+=("${APT_GET_BIN} *")`:         {},
	`internal/server/assets/upgrade-app.sh:[[ -n "${APT_BIN:-}" ]] && app_cmds+=("${APT_BIN} *")`:                 {},
	`internal/server/assets/upgrade-app.sh:[[ -n "${DPKG_BIN:-}" ]] && app_cmds+=("${DPKG_BIN} *")`:               {},
	`internal/server/assets/upgrade-app.sh:[[ -n "${SYSTEMD_RUN_BIN:-}" ]] && app_cmds+=("${SYSTEMD_RUN_BIN} *")`: {},
	`internal/server/assets/upgrade-app.sh:[[ -n "${UFW_BIN:-}" ]] && app_cmds+=("${UFW_BIN} *")`:                 {},
}

var legacyDockerGroupLines = map[string]struct{}{}

var legacyPrivilegedShellLiteralBudgets = map[string]int{
	// Closed non-root container entrypoints copy the immutable upstream web/API
	// payloads to bounded tmpfs so their roots can remain read-only. No host
	// path, command, argument, image, mount, or identity is request-selectable.
	"internal/appmanifest/mempool.go": 2,
}

func TestPrivilegeBoundaryCallSiteBudgets(t *testing.T) {
	root := moduleRoot(t)
	actual := make(map[string]privilegeCallBudget)
	fset := token.NewFileSet()

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		budget := actual[rel]
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calledFunctionName(call.Fun)
			switch name {
			case "RunCommandWithSudo":
				budget.runSudo++
			case "runSystemd":
				budget.runSystemd++
			case "WriteFileWithSudo":
				budget.writeSudo++
			case "SystemctlRestart", "SystemctlRestartNoBlock":
				budget.restart++
			case "SystemctlPower":
				budget.power++
			}
			if (name == "RunCommandWithSudo" || name == "runSystemd") && isPrivilegedShellCall(call) {
				budget.shell++
			}
			if isDirectDockerCommand(call) {
				position := fset.Position(call.Pos())
				t.Errorf("new direct Docker command at %s:%d; use the reviewed privileged boundary", rel, position.Line)
			}
			return true
		})
		actual[rel] = budget
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for path, got := range actual {
		if got == (privilegeCallBudget{}) {
			continue
		}
		limit, ok := legacyPrivilegeCallBudgets[path]
		if !ok {
			t.Errorf("new privileged call site in unreviewed file %s: %+v", path, got)
			continue
		}
		assertPrivilegeBudget(t, path, "RunCommandWithSudo", got.runSudo, limit.runSudo)
		assertPrivilegeBudget(t, path, "runSystemd", got.runSystemd, limit.runSystemd)
		assertPrivilegeBudget(t, path, "WriteFileWithSudo", got.writeSudo, limit.writeSudo)
		assertPrivilegeBudget(t, path, "systemctl restart", got.restart, limit.restart)
		assertPrivilegeBudget(t, path, "systemctl power", got.power, limit.power)
		assertPrivilegeBudget(t, path, "privileged shell -c", got.shell, limit.shell)
	}
}

func TestGenericManagerPrivilegeHelpersRemainDeleted(t *testing.T) {
	root := moduleRoot(t)
	forbidden := map[string]bool{
		"RunCommandWithSudo": true,
		"WriteFileWithSudo":  true,
		"runSystemd":         true,
	}
	for _, rel := range []string{"internal/server", "internal/system"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(rel)), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			for _, declaration := range parsed.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || !forbidden[function.Name.Name] {
					continue
				}
				position := fset.Position(function.Pos())
				t.Errorf("generic privileged helper %s reintroduced at %s:%d", function.Name.Name, filepath.ToSlash(path), position.Line)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestBTCPayLifecycleHasNoLegacyDockerExecution(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, "internal", "server", "apps_btcpay.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]struct{}{
		"ensureDocker":      {},
		"ensureDockerImage": {},
		"getComposeStatus":  {},
		"pullDockerImage":   {},
		"runCompose":        {},
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calledFunctionName(call.Fun)
		if _, blocked := forbidden[name]; blocked {
			position := fset.Position(call.Pos())
			t.Errorf("BTCPay legacy Docker call %s at line %d; use the typed broker", name, position.Line)
		}
		return true
	})
}

func TestManagerHasNoLegacyDockerExecution(t *testing.T) {
	root := moduleRoot(t)
	serverRoot := filepath.Join(root, "internal", "server")
	forbiddenHelpers := map[string]bool{
		"runCompose":                 true,
		"getComposeStatus":           true,
		"resolveCompose":             true,
		"composeContainerID":         true,
		"ensureDockerImage":          true,
		"pullDockerImage":            true,
		"installDocker":              true,
		"installComposePluginBinary": true,
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(serverRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calledFunctionName(call.Fun)
			if forbiddenHelpers[name] {
				position := fset.Position(call.Pos())
				t.Errorf("legacy Docker helper %s remains in manager at %s:%d", name, filepath.ToSlash(rel), position.Line)
			}
			if name == "RunCommandWithSudo" {
				args := stringArguments(call)
				if args["docker"] || args["docker-compose"] || args["/usr/bin/docker"] || args["/usr/bin/docker-compose"] {
					position := fset.Position(call.Pos())
					t.Errorf("direct privileged Docker command remains in manager at %s:%d", filepath.ToSlash(rel), position.Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLNbitsLifecycleHasNoLegacyPrivilegedExecution(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, "internal", "server", "apps_lnbits.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]struct{}{
		"ensureDocker":           {},
		"ensureDockerImage":      {},
		"getComposeStatus":       {},
		"pullDockerImage":        {},
		"runCompose":             {},
		"RunCommandWithSudo":     {},
		"ensureLnbitsRestAccess": {},
		"ensureLnbitsUfwAccess":  {},
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calledFunctionName(call.Fun)
		if _, blocked := forbidden[name]; blocked {
			position := fset.Position(call.Pos())
			t.Errorf("LNbits legacy privileged call %s at line %d; use the typed broker", name, position.Line)
		}
		return true
	})
}

func TestNativeBRLNAppsHaveNoOSPrivilegeExecution(t *testing.T) {
	root := moduleRoot(t)
	serverRoot := filepath.Join(root, "internal", "server")
	forbiddenImports := map[string]bool{
		"os/exec":                               true,
		"syscall":                               true,
		"lightningos-light/internal/privileged": true,
		"lightningos-light/internal/system":     true,
	}
	forbiddenCalls := map[string]bool{
		"RunCommandWithSudo":          true,
		"WriteFileWithSudo":           true,
		"runSystemd":                  true,
		"runCompose":                  true,
		"getComposeStatus":            true,
		"ensureDocker":                true,
		"ensureDockerImage":           true,
		"pullDockerImage":             true,
		"AppLifecycleWithBroker":      true,
		"PrepareAppImageWithBroker":   true,
		"EnsureAppFirewallWithBroker": true,
		"Command":                     true,
		"WriteFile":                   true,
		"Remove":                      true,
		"RemoveAll":                   true,
		"Chmod":                       true,
		"Chown":                       true,
		"Mkdir":                       true,
		"MkdirAll":                    true,
		"Rename":                      true,
		"CreateTemp":                  true,
		"OpenFile":                    true,
		"Listen":                      true,
	}

	err := filepath.WalkDir(serverRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		name := entry.Name()
		if name != "apps_loopout_brln.go" && name != "apps_magma.go" &&
			!strings.HasPrefix(name, "loopout_brln_") && !strings.HasPrefix(name, "magma_") {
			return nil
		}

		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range parsed.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if forbiddenImports[value] {
				t.Errorf("native BRLN app imports privileged package %q in %s", value, filepath.ToSlash(path))
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calledFunctionName(call.Fun)
			if forbiddenCalls[name] {
				position := fset.Position(call.Pos())
				t.Errorf("native BRLN app OS-privileged call %s in %s:%d", name, filepath.ToSlash(path), position.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoNewWildcardSudoOrDockerBoundary(t *testing.T) {
	root := moduleRoot(t)
	allowedDockerLines := make(map[string]struct{}, len(legacyDockerGroupLines))
	privilegedShellLiterals := make(map[string]int)
	for line := range legacyDockerGroupLines {
		allowedDockerLines[line] = struct{}{}
	}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "dist" || name == "lnrpc" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sh" && ext != ".service" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "scripts/capture-privilege-baseline.sh" {
			return nil
		}
		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			key := rel + ":" + line
			if isWildcardSudoLine(line) {
				if _, ok := legacyWildcardSudoLines[key]; !ok {
					t.Errorf("new or changed wildcard sudo rule at %s:%d: %s", rel, lineNumber, line)
				}
			}
			if strings.Contains(line, "/var/run/docker.sock") || strings.Contains(line, "/run/docker.sock") {
				t.Errorf("direct Docker socket reference at %s:%d", rel, lineNumber)
			}
			if isDockerGroupLine(line) {
				if _, ok := allowedDockerLines[key]; !ok {
					t.Errorf("new or changed Docker group grant at %s:%d: %s", rel, lineNumber, line)
				}
			}
			if strings.Contains(line, `"/bin/sh", "-c"`) || strings.Contains(line, `"/bin/bash", "-c"`) {
				privilegedShellLiterals[rel]++
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, count := range privilegedShellLiterals {
		limit, ok := legacyPrivilegedShellLiteralBudgets[path]
		if !ok || count > limit {
			t.Errorf("%s has %d privileged shell literal vectors; Phase 0 reviewed ceiling is %d", path, count, limit)
		}
	}
}

func TestPrivilegedBrokerSudoersEntryForbidsArguments(t *testing.T) {
	root := moduleRoot(t)
	paths := []string{
		"install.sh",
		"install_existing.sh",
		"install_existing_pi.sh",
		"internal/server/assets/upgrade-app.sh",
	}
	want := `system_cmds+=", ${PRIVILEGED_BROKER} \"\""`
	wantRootSelfTest := `env -u SUDO_UID -u SUDO_USER -u SUDO_COMMAND "$PRIVILEGED_BROKER"`
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := string(data)
		if !strings.Contains(content, want) {
			t.Errorf("%s does not grant the broker with an explicit empty argument list", rel)
		}
		if strings.Contains(content, `${PRIVILEGED_BROKER} *`) {
			t.Errorf("%s grants wildcard broker arguments", rel)
		}
		if !strings.Contains(content, wantRootSelfTest) {
			t.Errorf("%s does not isolate the direct-root self-test from an outer sudo environment", rel)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve privilege boundary test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func calledFunctionName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func stringArguments(call *ast.CallExpr) map[string]bool {
	values := make(map[string]bool)
	for _, arg := range call.Args {
		literal, ok := arg.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			values[value] = true
		}
	}
	return values
}

func isPrivilegedShellCall(call *ast.CallExpr) bool {
	args := stringArguments(call)
	return args["-c"] && (args["/bin/sh"] || args["/bin/bash"])
}

func isDirectDockerCommand(call *ast.CallExpr) bool {
	name := calledFunctionName(call.Fun)
	if name != "Command" && name != "CommandContext" && name != "RunCommand" {
		return false
	}
	args := stringArguments(call)
	return args["docker"] || args["docker-compose"] || args["/usr/bin/docker"] || args["/usr/bin/docker-compose"]
}

func assertPrivilegeBudget(t *testing.T, path string, capability string, got int, limit int) {
	t.Helper()
	if got > limit {
		t.Errorf("%s has %d %s calls; Phase 0 reviewed ceiling is %d", path, got, capability, limit)
	}
}

func isWildcardSudoLine(line string) bool {
	if !strings.Contains(line, "*") {
		return false
	}
	return strings.Contains(line, "NOPASSWD") ||
		strings.Contains(line, "Cmnd_Alias") ||
		strings.Contains(line, "system_cmds=") ||
		strings.Contains(line, "app_cmds+=(")
}

func isDockerGroupLine(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "docker") {
		return false
	}
	return strings.Contains(lower, "supplementarygroups") ||
		strings.Contains(lower, "ensure_group_member") ||
		strings.Contains(lower, "ensure_system_group") ||
		strings.Contains(lower, "membership_groups") ||
		(strings.Contains(lower, "getent group docker") && strings.Contains(lower, "groups+=("))
}

func (budget privilegeCallBudget) String() string {
	return fmt.Sprintf(
		"sudo=%d systemd=%d write=%d restart=%d power=%d shell=%d",
		budget.runSudo,
		budget.runSystemd,
		budget.writeSudo,
		budget.restart,
		budget.power,
		budget.shell,
	)
}
