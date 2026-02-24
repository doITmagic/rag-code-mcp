package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Scenario struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Vars        map[string]any   `json:"vars"`
	Steps       []map[string]any `json:"steps"`
}

type Runner struct {
	Vars         map[string]string
	ContainerCmd string
	Verbose      bool

	LXCContainerName string
	ContainerIP      string

	MCP *MCPClient

	ScenarioName string
	OutBaseDir   string
	Captures     map[string][]byte

	Logf func(format string, args ...any)
}

func NewRunner(logf func(string, ...any)) *Runner {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r := &Runner{Vars: map[string]string{}, OutBaseDir: "tests/e2e/out", Captures: map[string][]byte{}, Logf: logf}
	r.ContainerCmd = detectContainerCmd()
	r.Verbose = strings.TrimSpace(os.Getenv("RAGCODE_E2E_LOG")) == "1"
	return r
}

func detectContainerCmd() string {
	if v := strings.TrimSpace(os.Getenv("RAGCODE_LXC_CMD")); v != "" {
		return v
	}
	if _, err := exec.LookPath("lxc"); err == nil {
		return "lxc"
	}
	if _, err := exec.LookPath("incus"); err == nil {
		return "incus"
	}
	return "lxc"
}

func (r *Runner) containerCmd() string {
	if strings.TrimSpace(r.ContainerCmd) == "" {
		r.ContainerCmd = detectContainerCmd()
	}
	return r.ContainerCmd
}

func (r *Runner) RunScenarioFile(ctx context.Context, path string, overrideVars map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sc Scenario
	if err := json.Unmarshal(b, &sc); err != nil {
		return fmt.Errorf("parse scenario json: %w", err)
	}
	r.ScenarioName = sc.Name
	// Always cleanup ephemeral container created by the scenario.
	defer func() {
		_ = r.cleanupContainer(context.Background())
	}()

	// seed vars from scenario
	r.Vars = map[string]string{}
	for k, v := range sc.Vars {
		r.Vars[k] = fmt.Sprint(v)
	}
	// internal vars
	r.Vars["scenario_name"] = sc.Name
	r.Vars["timestamp"] = fmt.Sprint(time.Now().Unix())
	for k, v := range overrideVars {
		r.Vars[k] = v
	}
	if out, ok := overrideVars["out_base_dir"]; ok && strings.TrimSpace(out) != "" {
		r.OutBaseDir = out
	}

	for i, step := range sc.Steps {
		typeVal, _ := step["type"].(string)
		desc, _ := step["description"].(string)
		if desc != "" {
			r.Logf("[%d/%d] %s: %s", i+1, len(sc.Steps), typeVal, desc)
		} else {
			r.Logf("[%d/%d] %s", i+1, len(sc.Steps), typeVal)
		}

		if whenExpr, ok := step["when"].(string); ok && strings.TrimSpace(whenExpr) != "" {
			ok, err := r.evalWhen(whenExpr)
			if err != nil {
				return fmt.Errorf("step when eval failed: %w", err)
			}
			if !ok {
				r.Logf("  skipped (when=%s)", whenExpr)
				continue
			}
		}

		if err := r.runStep(ctx, step); err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", i+1, typeVal, err)
		}
	}

	return nil
}

func (r *Runner) preflightContainerAccess(ctx context.Context) error {
	cmdName := r.containerCmd()
	cmd := exec.CommandContext(ctx, cmdName, "info")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	msg := out.String()
	// Common LXD snap error when user is not in lxd group or session not refreshed.
	if strings.Contains(msg, "unix socket") && strings.Contains(strings.ToLower(msg), "permission denied") {
		return fmt.Errorf(
			"%s cannot access LXD/Incus socket (permission denied). Fix host permissions and retry. Typical fix:\n"+
				"- sudo lxd init --auto\n"+
				"- sudo usermod -aG lxd $USER\n"+
				"- newgrp lxd (or log out/in)\n"+
				"Then verify: lxc info\n\nRaw error: %s",
			cmdName,
			strings.TrimSpace(msg),
		)
	}

	return fmt.Errorf("%s preflight failed: %v\n%s", cmdName, runErr, strings.TrimSpace(msg))
}

func (r *Runner) stepGoldenCompare(ctx context.Context, step map[string]any) error {
	_ = ctx
	leftPath := r.expand(fmt.Sprint(step["left"]))
	rightPath := r.expand(fmt.Sprint(step["right"]))
	if leftPath == "" || rightPath == "" {
		return errors.New("golden.compare requires left and right")
	}
	left, err := os.ReadFile(leftPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read left: %w", err)
		}
		left = nil
	}
	right, err := os.ReadFile(rightPath)
	if err != nil {
		return fmt.Errorf("read right: %w", err)
	}

	update := false
	if strings.TrimSpace(os.Getenv("RAGCODE_GOLDEN_UPDATE")) == "1" {
		update = true
	}
	if v, ok := step["update"].(bool); ok {
		update = update || v
	}

	norm := strings.TrimSpace(fmt.Sprint(step["normalize"]))
	if norm == "rag_search_code" {
		maxItems := 0
		switch v := step["max_items"].(type) {
		case float64:
			maxItems = int(v)
		case string:
			vv := strings.TrimSpace(r.expand(v))
			if vv != "" {
				// best-effort parse
				if n, err := strconv.Atoi(vv); err == nil {
					maxItems = n
				}
			}
		}
		ln, err := normalizeRagSearchCodeJSON(left, maxItems)
		if err != nil {
			// If golden file is missing, left is empty and normalize may fail.
			if left != nil {
				return fmt.Errorf("normalize left: %w", err)
			}
			ln = ""
		}
		rn, err := normalizeRagSearchCodeJSON(right, maxItems)
		if err != nil {
			return fmt.Errorf("normalize right: %w", err)
		}
		if ln != rn {
			if update {
				if err := os.MkdirAll(filepath.Dir(leftPath), 0o755); err != nil {
					return fmt.Errorf("mkdir golden dir: %w", err)
				}
				if err := os.WriteFile(leftPath, []byte(rn), 0o644); err != nil {
					return fmt.Errorf("write golden: %w", err)
				}
				r.Logf("  golden updated: %s", leftPath)
				return nil
			}
			return fmt.Errorf("golden mismatch (rag_search_code)\nleft=%s\nright=%s", ln, rn)
		}
		return nil
	}

	// Default: raw JSON normalized with MarshalIndent.
	leftNorm := stableJSON(left)
	rightNorm := stableJSON(right)
	if !bytes.Equal(leftNorm, rightNorm) {
		if update {
			if err := os.MkdirAll(filepath.Dir(leftPath), 0o755); err != nil {
				return fmt.Errorf("mkdir golden dir: %w", err)
			}
			if err := os.WriteFile(leftPath, rightNorm, 0o644); err != nil {
				return fmt.Errorf("write golden: %w", err)
			}
			r.Logf("  golden updated: %s", leftPath)
			return nil
		}
		return fmt.Errorf("golden mismatch\nleft=%s\nright=%s", string(leftNorm), string(rightNorm))
	}
	return nil
}

func (r *Runner) cleanupContainer(ctx context.Context) error {
	name := strings.TrimSpace(r.LXCContainerName)
	if name == "" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("RAGCODE_E2E_KEEP")) == "1" {
		r.Logf("  keep container (RAGCODE_E2E_KEEP=1): %s", name)
		return nil
	}
	// Stop/delete best-effort.
	_ = r.lxcRun(ctx, "stop", name, "--force")
	_ = r.lxcRun(ctx, "delete", name)
	return nil
}

func (r *Runner) runStep(ctx context.Context, step map[string]any) error {
	stype, _ := step["type"].(string)
	switch stype {
	case "lxc.ensure_base_snapshot":
		return r.stepLXCEnsureBaseSnapshot(ctx, step)
	case "lxc.create_from_snapshot":
		return r.stepLXCCreateFromSnapshot(ctx, step)
	case "lxc.push_binaries":
		return r.stepLXCPushBinaries(ctx, step)
	case "lxc.exec":
		return r.stepLXCExec(ctx, step)
	case "wait.http_sse":
		return r.stepWaitHTTP(ctx, step)
	case "mcp.connect_sse":
		return r.stepMCPConnect(ctx, step)
	case "mcp.tool_call":
		return r.stepMCPToolCall(ctx, step)
	case "golden.compare":
		return r.stepGoldenCompare(ctx, step)
	default:
		return fmt.Errorf("unknown step type: %s", stype)
	}
}

func (r *Runner) evalWhen(expr string) (bool, error) {
	// Minimal expression: "${var} != ''" or "${var} == ''"
	expr = r.expand(expr)
	re := regexp.MustCompile(`^\s*(.*?)\s*(==|!=)\s*''\s*$`)
	m := re.FindStringSubmatch(expr)
	if len(m) != 4 {
		return false, fmt.Errorf("unsupported when expression: %s", expr)
	}
	left := m[1]
	op := m[2]
	isEmpty := strings.TrimSpace(left) == ""
	if op == "==" {
		return isEmpty, nil
	}
	return !isEmpty, nil
}

func (r *Runner) expand(s string) string {
	// Replace ${var} with r.Vars[var]
	return os.Expand(s, func(key string) string {
		if v, ok := r.Vars[key]; ok {
			return v
		}
		return ""
	})
}

func (r *Runner) stepLXCEnsureBaseSnapshot(ctx context.Context, step map[string]any) error {
	if err := r.preflightContainerAccess(ctx); err != nil {
		return err
	}

	image := r.expand(fmt.Sprint(step["image"]))
	base := r.expand(fmt.Sprint(step["base"]))
	snap := r.expand(fmt.Sprint(step["snapshot"]))

	if image == "" || base == "" || snap == "" {
		return errors.New("missing image/base/snapshot")
	}

	created := false
	if !r.lxcExists(ctx, base) {
		if err := r.lxcRun(ctx, "launch", image, base, "-c", "security.nesting=true"); err != nil {
			return err
		}
		_ = r.lxcExec(ctx, base, []string{"bash", "-lc", "command -v cloud-init >/dev/null 2>&1 && cloud-init status --wait || true"})
		created = true
	}

	// Install deps only once (when base is created).
	if created {
		if install, ok := step["install"].(map[string]any); ok {
			if aptRaw, ok := install["apt"].([]any); ok && len(aptRaw) > 0 {
				pkgs := make([]string, 0, len(aptRaw))
				for _, p := range aptRaw {
					pkgs = append(pkgs, fmt.Sprint(p))
				}
				cmd := "apt-get update && apt-get install -y " + strings.Join(pkgs, " ")
				if err := r.lxcExec(ctx, base, []string{"bash", "-lc", cmd}); err != nil {
					return err
				}
			}
		}
		// Start docker only once (when base is created).
		if docker, ok := step["docker"].(map[string]any); ok {
			if start, _ := docker["start"].(bool); start {
				_ = r.lxcExec(ctx, base, []string{"bash", "-lc", "systemctl enable docker >/dev/null 2>&1 || true; systemctl start docker"})
			}
		}
	}

	if !r.lxcSnapshotExists(ctx, base, snap) {
		if err := r.lxcRun(ctx, "snapshot", base, snap); err != nil {
			// Be idempotent: a previous run may have already created the snapshot.
			msg := err.Error()
			if strings.Contains(strings.ToLower(msg), "already exists") {
				return nil
			}
			return err
		}
	}
	return nil
}

func (r *Runner) stepLXCCreateFromSnapshot(ctx context.Context, step map[string]any) error {
	base := r.expand(fmt.Sprint(step["base"]))
	snap := r.expand(fmt.Sprint(step["snapshot"]))
	name := r.expand(fmt.Sprint(step["name"]))
	if name == "" {
		name = r.Vars["scenario_name"] + "-" + r.Vars["timestamp"]
	}
	name = sanitizeInstanceName(name)
	if name == "" {
		return errors.New("empty instance name after sanitization")
	}
	if err := r.lxcRun(ctx, "copy", base+"/"+snap, name); err != nil {
		return err
	}
	if err := r.lxcRun(ctx, "start", name); err != nil {
		return err
	}
	_ = r.lxcExec(ctx, name, []string{"bash", "-lc", "command -v cloud-init >/dev/null 2>&1 && cloud-init status --wait || true"})

	r.LXCContainerName = name
	ip, err := r.lxcGetIP(ctx, name)
	if err != nil {
		return err
	}
	r.ContainerIP = ip
	r.Vars["container_ip"] = ip
	return nil
}

func sanitizeInstanceName(s string) string {
	// LXD/Incus: name can only contain alphanumeric and hyphen.
	// Replace invalid chars with '-', collapse repeats, and trim leading/trailing '-'.
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		isAZ := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		is09 := (r >= '0' && r <= '9')
		if isAZ || is09 {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	out = strings.TrimSpace(out)
	return out
}

func (r *Runner) stepLXCPushBinaries(ctx context.Context, step map[string]any) error {
	name := r.LXCContainerName
	itemsRaw, ok := step["items"].([]any)
	if !ok {
		return errors.New("items missing")
	}
	for _, it := range itemsRaw {
		m, ok := it.(map[string]any)
		if !ok {
			return errors.New("invalid item")
		}
		src := r.expand(fmt.Sprint(m["src"]))
		dst := r.expand(fmt.Sprint(m["dst"]))
		mode := r.expand(fmt.Sprint(m["mode"]))
		if src == "" || dst == "" {
			return errors.New("src/dst missing")
		}
		if err := r.lxcRun(ctx, "file", "push", src, name+dst); err != nil {
			return err
		}
		if mode != "" {
			if err := r.lxcExec(ctx, name, []string{"bash", "-lc", fmt.Sprintf("chmod %s %s", mode, dst)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) stepLXCExec(ctx context.Context, step map[string]any) error {
	name := r.LXCContainerName
	cmdRaw, ok := step["cmd"].([]any)
	if !ok {
		return errors.New("cmd missing")
	}
	cmd := make([]string, 0, len(cmdRaw))
	for _, c := range cmdRaw {
		cmd = append(cmd, r.expand(fmt.Sprint(c)))
	}
	return r.lxcExec(ctx, name, cmd)
}

func (r *Runner) stepWaitHTTP(ctx context.Context, step map[string]any) error {
	url := r.expand(fmt.Sprint(step["url"]))
	timeoutSec := 60
	if v, ok := step["timeout_sec"].(float64); ok {
		timeoutSec = int(v)
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}

	diag := r.collectContainerDiagnostics(ctx)
	if diag != "" {
		return fmt.Errorf("timeout waiting for %s\n\n%s", url, diag)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func (r *Runner) collectContainerDiagnostics(ctx context.Context) string {
	name := strings.TrimSpace(r.LXCContainerName)
	if name == "" {
		return ""
	}
	// Best-effort: never fail diagnostics.
	var b strings.Builder
	b.WriteString("diagnostics (container): ")
	b.WriteString(name)
	b.WriteString("\n")

	appendCmd := func(title string, cmdArgs []string) {
		out, err := r.lxcExecCapture(ctx, name, cmdArgs)
		b.WriteString("--- ")
		b.WriteString(title)
		b.WriteString(" ---\n")
		if err != nil {
			b.WriteString("(error) ")
			b.WriteString(err.Error())
			b.WriteString("\n")
		}
		if strings.TrimSpace(out) == "" {
			b.WriteString("(no output)\n")
			return
		}
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	}

	appendCmd("/root/mcp.log (tail -n 200)", []string{"bash", "-lc", "tail -n 200 /root/mcp.log 2>/dev/null || true"})
	appendCmd("listening ports (ss -lntp)", []string{"bash", "-lc", "ss -lntp || netstat -lntp || true"})
	appendCmd("process list (ps aux | head)", []string{"bash", "-lc", "ps aux | head -n 80"})
	appendCmd("docker ps", []string{"bash", "-lc", "docker ps --no-trunc 2>/dev/null || true"})
	return b.String()
}

func (r *Runner) lxcExecCapture(ctx context.Context, container string, cmdArgs []string) (string, error) {
	args := append([]string{"exec", container, "--"}, cmdArgs...)
	cmd := exec.CommandContext(ctx, r.containerCmd(), args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func (r *Runner) stepMCPConnect(ctx context.Context, step map[string]any) error {
	baseURL := r.expand(fmt.Sprint(step["base_url"]))
	initPayload, ok := step["initialize"].(map[string]any)
	if !ok {
		return errors.New("initialize missing")
	}
	client, err := NewMCPClient(baseURL)
	if err != nil {
		return err
	}
	r.MCP = client

	// initialize
	id := "init-1"
	if err := client.SendJSONRPC(id, "initialize", initPayload); err != nil {
		return err
	}
	if _, err := client.WaitForID(id, 2*time.Minute); err != nil {
		return err
	}
	// notifications/initialized
	_ = client.SendNotification("notifications/initialized", map[string]any{})
	return nil
}

func (r *Runner) stepMCPToolCall(ctx context.Context, step map[string]any) error {
	if r.MCP == nil {
		return errors.New("MCP not connected")
	}
	name := fmt.Sprint(step["name"])
	args, _ := step["arguments"].(map[string]any)
	id := fmt.Sprintf("tool-%d", time.Now().UnixNano())
	payload := map[string]any{
		"name":      name,
		"arguments": r.expandAny(args).(map[string]any),
	}
	if err := r.MCP.SendJSONRPC(id, "tools/call", payload); err != nil {
		return err
	}
	msg, err := r.MCP.WaitForID(id, 10*time.Minute)
	if err != nil {
		return err
	}

	// Extract tool JSON from result.content[0].text
	toolJSON, err := extractToolTextJSON(msg)
	if err != nil {
		return err
	}

	// Assert
	if as, ok := step["assert"].(map[string]any); ok {
		if err := assertJSON(as, toolJSON); err != nil {
			return err
		}
	}

	// capture: write JSON to disk for inspection
	if cap, ok := step["capture"].(map[string]any); ok {
		as := ""
		if v, ok := cap["as"].(string); ok {
			as = strings.TrimSpace(v)
		}
		if as != "" {
			if r.Captures == nil {
				r.Captures = map[string][]byte{}
			}
			r.Captures[as] = append([]byte(nil), toolJSON...)

			outDir := filepath.Join(r.OutBaseDir, r.ScenarioName)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("create out dir: %w", err)
			}
			outFile := filepath.Join(outDir, as+".json")
			pretty := toolJSON
			var tmp any
			if err := json.Unmarshal(toolJSON, &tmp); err == nil {
				if b, err := json.MarshalIndent(tmp, "", "  "); err == nil {
					pretty = b
				}
			}
			if err := os.WriteFile(outFile, pretty, 0o644); err != nil {
				return fmt.Errorf("write capture: %w", err)
			}
			r.Logf("  captured: %s", outFile)
		}
	}

	return nil
}

func (r *Runner) expandAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, vv := range x {
			m[k] = r.expandAny(vv)
		}
		return m
	case []any:
		arr := make([]any, 0, len(x))
		for _, vv := range x {
			arr = append(arr, r.expandAny(vv))
		}
		return arr
	case string:
		return r.expand(x)
	default:
		return v
	}
}

// --- LXC helpers ---

func (r *Runner) lxcExists(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, r.containerCmd(), "info", name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func (r *Runner) lxcSnapshotExists(ctx context.Context, name, snap string) bool {
	cmd := exec.CommandContext(ctx, r.containerCmd(), "info", name+"/"+snap)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func (r *Runner) lxcRun(ctx context.Context, args ...string) error {
	cmdName := r.containerCmd()
	cmd := exec.CommandContext(ctx, cmdName, args...)
	var out bytes.Buffer
	if r.Verbose {
		r.Logf("  exec: %s %s", cmdName, strings.Join(args, " "))
		cmd.Stdout = io.MultiWriter(&out, os.Stdout)
		cmd.Stderr = io.MultiWriter(&out, os.Stderr)
	} else {
		cmd.Stdout = &out
		cmd.Stderr = &out
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w\n%s", r.containerCmd(), strings.Join(args, " "), err, out.String())
	}
	return nil
}

func (r *Runner) lxcExec(ctx context.Context, container string, cmdArgs []string) error {
	args := append([]string{"exec", container, "--"}, cmdArgs...)
	cmdName := r.containerCmd()
	cmd := exec.CommandContext(ctx, cmdName, args...)
	var out bytes.Buffer
	if r.Verbose {
		r.Logf("  exec: %s %s", cmdName, strings.Join(args, " "))
		cmd.Stdout = io.MultiWriter(&out, os.Stdout)
		cmd.Stderr = io.MultiWriter(&out, os.Stderr)
	} else {
		cmd.Stdout = &out
		cmd.Stderr = &out
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s exec failed: %w\ncmd=%v\n%s", r.containerCmd(), err, cmdArgs, out.String())
	}
	return nil
}

func (r *Runner) lxcGetIP(ctx context.Context, container string) (string, error) {
	// lxc list <name> -c4 --format csv
	cmd := exec.CommandContext(ctx, r.containerCmd(), "list", container, "-c", "4", "--format", "csv")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s list ip failed: %w\n%s", r.containerCmd(), err, out.String())
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", errors.New("no ip returned")
	}
	// Output can contain multiple addresses with interface hints, e.g.:
	// 10.123.45.67 (eth0), 172.17.0.1 (docker0)
	parts := strings.Split(text, ",")
	type ipCand struct {
		ip    string
		iface string
	}
	cands := make([]ipCand, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, "\""))
		if p == "" {
			continue
		}
		ip := p
		iface := ""
		if i := strings.Index(p, " "); i != -1 {
			ip = p[:i]
			rest := p[i+1:]
			if l := strings.Index(rest, "("); l != -1 {
				if r := strings.Index(rest[l+1:], ")"); r != -1 {
					iface = rest[l+1 : l+1+r]
				}
			}
		}
		ip = strings.TrimSpace(strings.Trim(ip, "\""))
		if ip == "" {
			continue
		}
		// skip loopback
		if strings.HasPrefix(ip, "127.") {
			continue
		}
		cands = append(cands, ipCand{ip: ip, iface: iface})
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("unable to parse ip from: %q", text)
	}
	// Prefer eth0 (LXD bridge). Avoid docker0.
	for _, c := range cands {
		if c.iface == "eth0" {
			return c.ip, nil
		}
	}
	for _, c := range cands {
		if c.iface != "docker0" {
			return c.ip, nil
		}
	}
	return cands[0].ip, nil
}

// --- MCP client ---

type MCPClient struct {
	BaseURL   string
	SessionID string
	outCh     chan []byte
	resp      *http.Response
	reader    *bufio.Reader
}

func NewMCPClient(baseURL string) (*MCPClient, error) {
	c := &MCPClient{BaseURL: baseURL, outCh: make(chan []byte, 64)}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/sse", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unexpected SSE status %d: %s", resp.StatusCode, string(body))
	}
	c.resp = resp
	c.reader = bufio.NewReader(resp.Body)

	endpoint, err := readSSEEvent(c.reader)
	if err != nil {
		return nil, err
	}
	sid := extractSessionID(endpoint)
	if sid == "" {
		return nil, fmt.Errorf("missing sessionid from SSE endpoint event: %s", string(endpoint))
	}
	c.SessionID = sid
	go c.readLoop()
	return c, nil
}

func (c *MCPClient) Close() {
	if c.resp != nil {
		_ = c.resp.Body.Close()
	}
}

func (c *MCPClient) readLoop() {
	defer close(c.outCh)
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if buf.Len() > 0 {
				payload := append([]byte(nil), buf.Bytes()...)
				c.outCh <- payload
				buf.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			buf.WriteString(data)
		}
	}
}

func (c *MCPClient) SendJSONRPC(id, method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := c.BaseURL + "/messages?sessionid=" + c.SessionID
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/messages status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *MCPClient) SendNotification(method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := c.BaseURL + "/messages?sessionid=" + c.SessionID
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/messages status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *MCPClient) WaitForID(id string, timeout time.Duration) (map[string]any, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for id=%s", id)
		case data, ok := <-c.outCh:
			if !ok {
				return nil, fmt.Errorf("SSE closed before id=%s", id)
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msgID, ok := msg["id"].(string); ok && msgID == id {
				return msg, nil
			}
		}
	}
}

func readSSEEvent(r *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if buf.Len() > 0 {
				return buf.Bytes(), nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			buf.WriteString(data)
		}
	}
}

func extractSessionID(endpoint []byte) string {
	text := string(endpoint)
	idx := strings.Index(text, "sessionid=")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(text[idx+len("sessionid="):])
}

// --- Tool response extraction + asserts ---

func extractToolTextJSON(msg map[string]any) ([]byte, error) {
	res, ok := msg["result"].(map[string]any)
	if !ok {
		return nil, errors.New("missing result")
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		return nil, errors.New("missing content")
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		return nil, errors.New("invalid content")
	}
	text, ok := first["text"].(string)
	if !ok {
		return nil, errors.New("missing text")
	}
	return []byte(text), nil
}

func assertJSON(assert map[string]any, toolJSON []byte) error {
	path := fmt.Sprint(assert["json_path"])
	if path == "" {
		return errors.New("assert.json_path missing")
	}
	var doc any
	if err := json.Unmarshal(toolJSON, &doc); err != nil {
		return fmt.Errorf("tool response is not json: %w", err)
	}
	if exists, ok := assert["exists"].(bool); ok && exists {
		_, err := jsonGet(doc, path)
		if err != nil {
			return fmt.Errorf("assert exists failed: %s (%v)", path, err)
		}
		return nil
	}
	val, err := jsonGet(doc, path)
	if err != nil {
		return err
	}
	if eq, ok := assert["equals"]; ok {
		if fmt.Sprint(val) != fmt.Sprint(eq) {
			return fmt.Errorf("assert failed: %s expected=%v got=%v", path, eq, val)
		}
	}
	if neq, ok := assert["not_equals"]; ok {
		if fmt.Sprint(val) == fmt.Sprint(neq) {
			return fmt.Errorf("assert failed: %s not_equals=%v got=%v", path, neq, val)
		}
	}
	if inRaw, ok := assert["in"].([]any); ok {
		got := fmt.Sprint(val)
		for _, v := range inRaw {
			if got == fmt.Sprint(v) {
				return nil
			}
		}
		return fmt.Errorf("assert failed: %s not in %v (got=%v)", path, inRaw, val)
	}
	if contains, ok := assert["contains"]; ok {
		got := fmt.Sprint(val)
		if !strings.Contains(got, fmt.Sprint(contains)) {
			return fmt.Errorf("assert failed: %s does not contain %v (got=%v)", path, contains, got)
		}
	}
	if reRaw, ok := assert["regex"]; ok {
		re, err := regexp.Compile(fmt.Sprint(reRaw))
		if err != nil {
			return fmt.Errorf("assert regex invalid: %w", err)
		}
		got := fmt.Sprint(val)
		if !re.MatchString(got) {
			return fmt.Errorf("assert failed: %s regex %v did not match (got=%v)", path, reRaw, got)
		}
	}
	return nil
}

func jsonGet(doc any, path string) (any, error) {
	// Only supports $.field.subfield
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("unsupported json_path: %s", path)
	}
	parts := strings.Split(strings.TrimPrefix(path, "$."), ".")
	cur := doc
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("json_path not a map at %s", p)
		}
		cur, ok = m[p]
		if !ok {
			return nil, fmt.Errorf("json_path missing key %s", p)
		}
	}
	return cur, nil
}

func stableJSON(raw []byte) []byte {
	var tmp any
	if err := json.Unmarshal(raw, &tmp); err != nil {
		return bytes.TrimSpace(raw)
	}
	b, err := json.MarshalIndent(tmp, "", "  ")
	if err != nil {
		return bytes.TrimSpace(raw)
	}
	return b
}

func normalizeRagSearchCodeJSON(raw []byte, maxItems int) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", fmt.Errorf("empty input")
	}
	// Tool returns JSON string. We compare only stable fields in data[]:
	// file_path|name|kind|start_line|end_line|_graph_expansion
	var tr struct {
		Status string           `json:"status"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("unmarshal tool response: %w", err)
	}
	if tr.Status != "success" && tr.Status != "no_results" {
		return tr.Status, nil
	}
	items := make([]string, 0, len(tr.Data))
	for _, d := range tr.Data {
		filePath, _ := d["file_path"].(string)
		name, _ := d["name"].(string)
		kind, _ := d["kind"].(string)
		startLine := fmt.Sprint(d["start_line"])
		endLine := fmt.Sprint(d["end_line"])
		graph, _ := d["_graph_expansion"].(string)
		key := strings.Join([]string{filePath, name, kind, startLine, endLine, graph}, "|")
		items = append(items, key)
	}
	sort.Strings(items)
	if maxItems > 0 && len(items) > maxItems {
		items = items[:maxItems]
	}
	return tr.Status + "\n" + strings.Join(items, "\n"), nil
}

func init() {
	// Compile-time check that regex in evalWhen is valid
	_ = regexp.MustCompile
}
