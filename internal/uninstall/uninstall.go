package uninstall

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	installDirName = ".ragcode"
	binDirName     = "bin"
)

func logMsg(msg string)     { fmt.Printf("\033[0;34m==>\033[0m %s\n", msg) }
func successMsg(msg string) { fmt.Printf("\033[0;32m✓\033[0m %s\n", msg) }
func warnMsg(msg string)    { fmt.Printf("\033[1;33m!\033[0m %s\n", msg) }

// RunUninstall completely uninstalls RagCode components from the system directly.
func RunUninstall() {
	home, _ := os.UserHomeDir()
	installPath := filepath.Join(home, installDirName)
	binPath := filepath.Join(installPath, binDirName)
	targetBin := filepath.Join(binPath, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		targetBin += ".exe"
	}

	logMsg("Uninstalling RagCode MCP from the system directly...")
	fmt.Println()

	// 1. Stop running processes, but keep our own process alive!
	logMsg("Step 1/7: Stopping running processes...")
	stopRunningProcess(targetBin)
	successMsg("Processes stopped")

	// 2. Clean per-workspace .ragcode/ directories
	logMsg("Step 2/7: Cleaning per-workspace data...")
	cleanWorkspaceData(home)

	// 3. Remove installation directory
	logMsg("Step 3/7: Removing installation directory: " + installPath)
	if _, err := os.Stat(installPath); err == nil {
		if err := os.RemoveAll(installPath); err != nil {
			warnMsg(fmt.Sprintf("Failed to remove %s: %v", installPath, err))
		} else {
			successMsg("Removed " + installPath)
		}
	} else {
		logMsg("Directory not found, skipping: " + installPath)
	}

	// Legacy paths
	legacyPaths := []string{
		filepath.Join(home, ".local", "share", "ragcode"),
		filepath.Join(home, ".local", "state", "ragcode"),
	}
	for _, lp := range legacyPaths {
		if _, err := os.Stat(lp); err == nil {
			if err := os.RemoveAll(lp); err != nil {
				warnMsg(fmt.Sprintf("Failed to remove legacy path %s: %v", lp, err))
			} else {
				successMsg("Removed legacy path: " + lp)
			}
		}
	}

	// 4. Clean PATH
	logMsg("Step 4/7: Cleaning PATH from shell configuration...")
	removeFromShellConfig(home, binPath)
	removeFromShellConfig(home, filepath.Join(home, ".local", "share", "ragcode", "bin"))

	// 5. Remove from IDE MCP configs
	logMsg("Step 5/7: Removing ragcode from IDE configurations...")
	removeFromIDEConfigs(home)

	// 6. Stop and remove Docker containers
	logMsg("Step 6/7: Cleaning up Docker containers...")
	removeDockerResources()

	// 7. Clean Qdrant collections
	logMsg("Step 7/7: Cleaning Qdrant collections...")
	cleanQdrantCollections()

	fmt.Println()
	successMsg("RagCode MCP fully uninstalled!")
}

// stopRunningProcess excludes os.Getpid()
func stopRunningProcess(binPath string) {
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		return
	}

	myPID := strconv.Itoa(os.Getpid())
	logMsg(fmt.Sprintf("Stopping instances of: %s (excluding our PID %s)", binPath, myPID))

	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s", filepath.Base(binPath)), "/NH", "/FO", "CSV")
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\",\"")
				if len(parts) >= 2 {
					pidStr := strings.Trim(parts[1], "\"")
					if pidStr != myPID && pidStr != "" {
						_ = exec.Command("taskkill", "/F", "/PID", pidStr).Run()
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
		return
	}

	// Fallback using lsof to find PIDs mapping this binary
	if _, err := exec.LookPath("lsof"); err == nil {
		cmd := exec.Command("lsof", "-t", binPath)
		if output, err := cmd.Output(); err == nil {
			pids := strings.Fields(string(output))
			for _, pid := range pids {
				if pid != myPID {
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
		}
	} else if _, err := exec.LookPath("pgrep"); err == nil {
		cmd := exec.Command("pgrep", "-f", binPath)
		if output, err := cmd.Output(); err == nil {
			pids := strings.Fields(string(output))
			for _, pid := range pids {
				if pid != myPID {
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
		}
	}

	time.Sleep(500 * time.Millisecond)
}

func removeFromShellConfig(home, binDir string) {
	shellConfigs := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}

	for _, cfg := range shellConfigs {
		content, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}

		original := string(content)
		if !strings.Contains(original, "RagCode") && !strings.Contains(original, binDir) {
			continue
		}

		lines := strings.Split(original, "\n")
		var cleaned []string
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "# RagCode MCP" {
				skip = true
				continue
			}
			if skip && strings.Contains(line, binDir) {
				skip = false
				continue
			}
			skip = false
			cleaned = append(cleaned, line)
		}

		newContent := strings.Join(cleaned, "\n")
		if newContent != original {
			if err := os.WriteFile(cfg, []byte(newContent), 0644); err != nil {
				warnMsg("Failed to clean " + cfg + ": " + err.Error())
			} else {
				successMsg("Cleaned PATH from " + cfg)
			}
		}
	}
}

func removeFromIDEConfigs(home string) {
	paths := resolveIDEPaths(home)
	for key, ide := range paths {
		if key == "zed" {
			removeZedRagcodeEntry(ide.displayName, ide.path)
			continue
		}
		removeRagcodeFromJSON(ide.displayName, ide.path)
	}
}

func removeRagcodeFromJSON(displayName, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(data, &configMap); err != nil {
		return
	}

	modified := false
	for _, key := range []string{"mcpServers", "servers"} {
		servers, ok := configMap[key].(map[string]interface{})
		if !ok {
			continue
		}
		if _, exists := servers["ragcode"]; exists {
			delete(servers, "ragcode")
			configMap[key] = servers
			modified = true
		}
	}

	if modified {
		newData, _ := json.MarshalIndent(configMap, "", "  ")
		if err := os.WriteFile(path, newData, 0644); err != nil {
			warnMsg("Failed to update " + displayName + ": " + err.Error())
		} else {
			successMsg("Removed ragcode from " + displayName + " (" + path + ")")
		}
	}
}

func removeZedRagcodeEntry(displayName, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(data, &configMap); err != nil {
		return
	}

	servers, ok := configMap["context_servers"].(map[string]interface{})
	if !ok {
		return
	}
	if _, exists := servers["ragcode"]; !exists {
		return
	}

	delete(servers, "ragcode")
	configMap["context_servers"] = servers

	newData, _ := json.MarshalIndent(configMap, "", "  ")
	if err := os.WriteFile(path, newData, 0644); err != nil {
		warnMsg("Failed to update " + displayName + ": " + err.Error())
	} else {
		successMsg("Removed ragcode from " + displayName + " (" + path + ")")
	}
}

func removeDockerResources() {
	if _, err := exec.LookPath("docker"); err != nil {
		logMsg("Docker not found, skipping container cleanup")
		return
	}

	containers := []string{"ragcode-qdrant", "ragcode-ollama"}
	for _, name := range containers {
		check := exec.Command("docker", "inspect", name)
		if err := check.Run(); err != nil {
			continue // Doesn't exist
		}
		_ = exec.Command("docker", "stop", name).Run()
		if err := exec.Command("docker", "rm", "-f", name).Run(); err != nil {
			warnMsg("Failed to remove container " + name + ": " + err.Error())
		} else {
			successMsg("Removed Docker container: " + name)
		}
	}

	volumes := []string{"ragcode-qdrant-data"}
	for _, vol := range volumes {
		check := exec.Command("docker", "volume", "inspect", vol)
		if err := check.Run(); err != nil {
			continue
		}
		if err := exec.Command("docker", "volume", "rm", vol).Run(); err != nil {
			warnMsg("Failed to remove volume " + vol + ": " + err.Error())
		} else {
			successMsg("Removed Docker volume: " + vol)
		}
	}
}

func cleanWorkspaceData(home string) {
	registryPath := filepath.Join(home, installDirName, "registry.json")

	var registryRoots []string
	data, err := os.ReadFile(registryPath)
	if err == nil {
		registryRoots = extractWorkspaceRoots(data)
	}

	// Step 1: direct delete for each workspace known to the registry.
	if len(registryRoots) > 0 {
		for _, wsPath := range registryRoots {
			ragDir := filepath.Join(wsPath, ".ragcode")
			if _, err := os.Stat(ragDir); err == nil {
				if err := os.RemoveAll(ragDir); err != nil {
					warnMsg(fmt.Sprintf("Failed to remove %s: %v", ragDir, err))
				} else {
					successMsg("Removed workspace data (from registry): " + ragDir)
				}
			}
		}
	} else {
		logMsg("No valid registry found or no per-workspace .ragcode/ directories in registry.")
	}

	// Step 2: catch anything the registry missed — orphaned dirs, workspaces
	// outside $HOME, or roots the registry entry was lost for.
	// Scan roots are derived from: registry parent dirs, Qdrant file_path payloads,
	// IDE project lists, and a shallow $HOME scan.
	logMsg("Scanning for any orphaned .ragcode/ directories not covered by registry...")
	qdrantRoots := extractWorkspaceRootsFromQdrant()
	scanAndCleanRagcodeDirs(home, append(registryRoots, qdrantRoots...))
}

// extractWorkspaceRoots tries to parse the registry in all known formats
// and returns all workspace root paths found.
func extractWorkspaceRoots(data []byte) []string {
	// V2 format: {"version":"v2", "entries":[{"root":"/path",...}], ...}
	var v2Store struct {
		Version string `json:"version"`
		Entries []struct {
			Root string `json:"root"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &v2Store); err == nil && v2Store.Version == "v2" && len(v2Store.Entries) > 0 {
		roots := make([]string, 0, len(v2Store.Entries))
		for _, e := range v2Store.Entries {
			if e.Root != "" {
				roots = append(roots, e.Root)
			}
		}
		if len(roots) > 0 {
			logMsg(fmt.Sprintf("Found %d workspace(s) in V2 registry", len(roots)))
			return roots
		}
	}

	// V1 format: [{"root":"/path",...},...]
	var v1Entries []struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal(data, &v1Entries); err == nil && len(v1Entries) > 0 {
		roots := make([]string, 0, len(v1Entries))
		for _, e := range v1Entries {
			if e.Root != "" {
				roots = append(roots, e.Root)
			}
		}
		if len(roots) > 0 {
			logMsg(fmt.Sprintf("Found %d workspace(s) in V1 registry", len(roots)))
			return roots
		}
	}

	// Legacy flat map format: {"/path/to/ws": {...}, ...}
	var flatMap map[string]interface{}
	if err := json.Unmarshal(data, &flatMap); err == nil {
		roots := make([]string, 0, len(flatMap))
		for key := range flatMap {
			// Skip known non-path keys from V2 format
			if key == "version" || key == "entries" || key == "candidates" {
				continue
			}
			if key != "" {
				roots = append(roots, key)
			}
		}
		if len(roots) > 0 {
			logMsg(fmt.Sprintf("Found %d workspace(s) in legacy registry", len(roots)))
			return roots
		}
	}

	return nil
}

// scanAndCleanRagcodeDirs scans for orphaned .ragcode/ directories not covered
// by the registry cleanup above.
//
// Strategy: instead of guessing user-specific folder names (Projects, code, dev…),
// we derive search roots from data we already know:
//  1. Parent directories of every registered workspace root — any sibling or
//     leftover from a workspace that was unregistered but still has cache.
//  2. $HOME itself at depth 1 — catches any .ragcode/ that was accidentally
//     written directly under the user's home directory.
//
// This approach works for any user, on any OS, with any folder structure.
func scanAndCleanRagcodeDirs(home string, registryRoots []string) {
	// Build a de-duplicated set of directories to scan.
	seen := make(map[string]struct{})
	var searchRoots []string

	add := func(dir string) {
		dir = filepath.Clean(dir)
		if dir == "." || dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		searchRoots = append(searchRoots, dir)
	}

	// Derive parent directories from every registered workspace.
	// These are the most likely places to find orphaned cache dirs.
	for _, root := range registryRoots {
		if root != "" {
			add(filepath.Dir(root))
		}
	}

	// Derive parent directories from IDE project lists.
	// IDEs keep authoritative lists of opened projects in known config files
	// — much more reliable than guessing folder names.
	for _, ideRoot := range detectIDEProjectParents(home) {
		add(ideRoot)
	}

	// Always include $HOME (depth-1 only) to catch top-level orphans.
	add(home)

	cleaned := 0
	for _, root := range searchRoots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}

		// isHomeRoot controls scan depth: for $HOME we only look 1 level deep
		// to avoid scanning the entire filesystem; for parent dirs derived from
		// known workspace paths we allow a few levels to catch nested leftovers.
		maxDepth := 3
		if filepath.Clean(root) == filepath.Clean(home) {
			maxDepth = 1
		}

		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if strings.Count(rel, string(os.PathSeparator)) > maxDepth {
				return filepath.SkipDir
			}
			if info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" {
					return filepath.SkipDir
				}
				if name == ".ragcode" {
					if err := os.RemoveAll(path); err != nil {
						warnMsg(fmt.Sprintf("Failed to remove %s: %v", path, err))
					} else {
						successMsg("Removed orphaned workspace data: " + path)
						cleaned++
					}
					return filepath.SkipDir
				}
			}
			return nil
		})
	}

	if cleaned == 0 {
		logMsg("No orphaned .ragcode/ directories found")
	}
}

// detectIDEProjectParents returns the *parent directories* of projects known
// to installed IDEs. We read IDE config files that list recently opened
// projects — this is authoritative and works regardless of how the user
// organises their filesystem.
//
// Supported:
//   - JetBrains family (IntelliJ, GoLand, PyCharm, WebStorm, Rider…)
//     Linux/macOS: ~/.config/JetBrains/*/options/recentProjects.xml
//     macOS legacy: ~/Library/Application Support/JetBrains/*/options/recentProjects.xml
//   - VSCode / VSCodium / Cursor / Windsurf
//     Linux: ~/.config/{Code,VSCodium,Cursor,Windsurf}/User/globalStorage/storage.json
//     macOS: ~/Library/Application Support/{Code,VSCodium,Cursor,Windsurf}/User/...
func detectIDEProjectParents(home string) []string {
	seen := make(map[string]struct{})
	var parents []string

	addProject := func(projectPath string) {
		if projectPath == "" {
			return
		}
		parent := filepath.Dir(filepath.Clean(projectPath))
		if _, ok := seen[parent]; ok {
			return
		}
		seen[parent] = struct{}{}
		parents = append(parents, parent)
	}

	// ── JetBrains ─────────────────────────────────────────────────────────────
	// recentProjects.xml contains <entry key="$USER_HOME$/path/to/project">.
	type jbEntry struct {
		Key string `xml:"key,attr"`
	}
	type jbMap struct {
		Entries []jbEntry `xml:"entry"`
	}
	type jbComponent struct {
		Name string `xml:"name,attr"`
		Map  jbMap  `xml:"map"`
	}
	type jbApplication struct {
		Components []jbComponent `xml:"component"`
	}

	jetbrainsConfigDirs := []string{
		filepath.Join(home, ".config", "JetBrains"),
		filepath.Join(home, "Library", "Application Support", "JetBrains"),
	}
	for _, jbBase := range jetbrainsConfigDirs {
		productDirs, _ := os.ReadDir(jbBase)
		for _, pd := range productDirs {
			if !pd.IsDir() {
				continue
			}
			recent := filepath.Join(jbBase, pd.Name(), "options", "recentProjects.xml")
			data, err := os.ReadFile(recent)
			if err != nil {
				continue
			}
			var app jbApplication
			if err := xml.Unmarshal(data, &app); err != nil {
				continue
			}
			for _, comp := range app.Components {
				if comp.Name != "RecentProjectsManager" && comp.Name != "RecentDirectoryProjectsManager" {
					continue
				}
				for _, entry := range comp.Map.Entries {
					path := strings.ReplaceAll(entry.Key, "$USER_HOME$", home)
					addProject(path)
				}
			}
		}
	}

	// ── VSCode family ─────────────────────────────────────────────────────────
	// storage.json has {"openedPathsList": {"workspaces3": ["/path", ...]}}
	type vscodeStorage struct {
		OpenedPathsList struct {
			Workspaces  []string `json:"workspaces3"`
			Folders     []string `json:"workspaceFolder"`
		} `json:"openedPathsList"`
	}

	vscodeApps := []string{"Code", "VSCodium", "Cursor", "Windsurf"}
	vscodeConfigBases := []string{
		filepath.Join(home, ".config"),
		filepath.Join(home, "Library", "Application Support"),
	}
	for _, base := range vscodeConfigBases {
		for _, app := range vscodeApps {
			storagePath := filepath.Join(base, app, "User", "globalStorage", "storage.json")
			data, err := os.ReadFile(storagePath)
			if err != nil {
				continue
			}
			var st vscodeStorage
			if err := json.Unmarshal(data, &st); err != nil {
				continue
			}
			for _, p := range st.OpenedPathsList.Workspaces {
				// Entries may be "file:///path" URIs.
				p = strings.TrimPrefix(p, "file://")
				addProject(p)
			}
			for _, p := range st.OpenedPathsList.Folders {
				p = strings.TrimPrefix(p, "file://")
				addProject(p)
			}
		}
	}

	// ── AI IDE config directories ─────────────────────────────────────────────
	// resolveIDEPaths already knows the canonical config-file locations for
	// every AI IDE (Windsurf, Cursor, Copilot, Antigravity, Claude, Zed…).
	// The *parent* of each config file is the IDE's own data directory
	// (e.g. ~/.codeium/windsurf, ~/.cursor). Projects opened in those IDEs
	// are often stored directly under the grandparent of that config
	// (e.g. ~/.codeium, ~/.config/zed), so we add both levels.
	for _, ide := range resolveIDEPaths(home) {
		if ide.path == "" {
			continue
		}
		// config file's parent dir (the IDE data dir, e.g. ~/.cursor)
		addProject(filepath.Dir(ide.path))
	}

	return parents
}

// extractWorkspaceRootsFromQdrant queries Qdrant (if reachable) to discover
// workspace roots that were indexed but whose .ragcode/registry entry may have
// been lost.
//
// Strategy: for each ragcode-* collection, fetch a single point via Scroll and
// read its file_path payload field. Then walk upward from that file_path until
// we find a directory that contains .git or .ragcode — that is the workspace root.
// We return those roots so the caller can delete their .ragcode/ dirs.
func extractWorkspaceRootsFromQdrant() []string {
	const qdrantAddr = "http://localhost:6333"

	conn, err := net.DialTimeout("tcp", "127.0.0.1:6333", 2*time.Second)
	if err != nil {
		return nil // Qdrant not running — skip silently
	}
	conn.Close()

	// List all collections.
	cmd := exec.Command("curl", "-s", qdrantAddr+"/collections")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var listResp struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &listResp); err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var roots []string

	for _, col := range listResp.Result.Collections {
		if !strings.HasPrefix(col.Name, "ragcode-") {
			continue
		}

		// Scroll 1 point with payload to get a file_path sample.
		scrollPayload := `{"limit":1,"with_payload":true,"with_vector":false}`
		scrollCmd := exec.Command("curl", "-s", "-X", "POST",
			qdrantAddr+"/collections/"+col.Name+"/points/scroll",
			"-H", "Content-Type: application/json",
			"-d", scrollPayload)
		scrollOut, err := scrollCmd.Output()
		if err != nil {
			continue
		}

		var scrollResp struct {
			Result struct {
				Points []struct {
					Payload map[string]interface{} `json:"payload"`
				} `json:"points"`
			} `json:"result"`
		}
		if err := json.Unmarshal(scrollOut, &scrollResp); err != nil {
			continue
		}
		if len(scrollResp.Result.Points) == 0 {
			continue
		}

		filePath, _ := scrollResp.Result.Points[0].Payload["file_path"].(string)
		if filePath == "" {
			continue
		}

		// Walk upward from filePath to find workspace root.
		wsRoot := findWorkspaceRootFromFilePath(filePath)
		if wsRoot == "" {
			continue
		}
		if _, ok := seen[wsRoot]; ok {
			continue
		}
		seen[wsRoot] = struct{}{}
		roots = append(roots, wsRoot)
	}

	return roots
}

// findWorkspaceRootFromFilePath walks upward from a file path until it finds
// a directory containing .git or .ragcode — the canonical workspace root markers.
func findWorkspaceRootFromFilePath(filePath string) string {
	dir := filepath.Dir(filePath)
	for {
		for _, marker := range []string{".git", ".ragcode"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return ""
}

func cleanQdrantCollections() {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6333", 2*time.Second)
	if err != nil {
		logMsg("Qdrant not reachable on port 6333, skipping collection cleanup")
		return
	}
	conn.Close()

	cmd := exec.Command("curl", "-s", "http://localhost:6333/collections")
	output, err := cmd.Output()
	if err != nil {
		warnMsg("Could not list Qdrant collections: " + err.Error())
		return
	}

	var resp struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		warnMsg("Could not parse Qdrant response: " + err.Error())
		return
	}

	deleted := 0
	for _, col := range resp.Result.Collections {
		if strings.HasPrefix(col.Name, "ragcode-") {
			delCmd := exec.Command("curl", "-s", "-X", "DELETE", "http://localhost:6333/collections/"+col.Name)
			if err := delCmd.Run(); err != nil {
				warnMsg("Failed to delete collection " + col.Name + ": " + err.Error())
			} else {
				successMsg("Deleted Qdrant collection: " + col.Name)
				deleted++
			}
		}
	}

	if deleted == 0 {
		logMsg("No ragcode collections found in Qdrant")
	}
}

type idePath struct {
	path        string
	displayName string
}

func resolveIDEPaths(home string) map[string]idePath {
	paths := map[string]idePath{
		"windsurf": {
			path:        filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
			displayName: "Windsurf",
		},
		"cursor": {
			path:        filepath.Join(home, ".cursor", "mcp.config.json"),
			displayName: "Cursor",
		},
		"copilot": {
			path:        filepath.Join(home, ".aitk", "mcp.json"),
			displayName: "GitHub Copilot",
		},
		"antigravity": {
			path:        filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			displayName: "Antigravity",
		},
		"mcp-cli": {
			path:        filepath.Join(home, ".config", "mcp-servers.json"),
			displayName: "MCP CLI / Generic",
		},
		"claude-cli": {
			path:        filepath.Join(home, ".claude.json"),
			displayName: "Claude Code CLI",
		},
		"gemini-cli": {
			path:        filepath.Join(home, ".gemini", "settings.json"),
			displayName: "Gemini CLI",
		},
	}

	switch runtime.GOOS {
	case "darwin":
		paths["claude"] = idePath{filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), "Claude Desktop"}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			paths["claude"] = idePath{filepath.Join(appData, "Claude", "claude_desktop_config.json"), "Claude Desktop"}
		}
	default:
		paths["claude"] = idePath{filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), "Claude Desktop"}
	}

	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			paths["zed"] = idePath{filepath.Join(appData, "Zed", "settings.json"), "Zed Editor"}
		}
	default:
		paths["zed"] = idePath{filepath.Join(home, ".config", "zed", "settings.json"), "Zed Editor"}
	}

	if vsPath, ok := determineVSCodePath(home); ok {
		paths["vs-code"] = vsPath
	}

	return paths
}

func determineVSCodePath(home string) (idePath, bool) {
	var userDir string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return idePath{}, false
		}
		userDir = filepath.Join(appData, "Code", "User")
	case "darwin":
		userDir = filepath.Join(home, "Library", "Application Support", "Code", "User")
	default:
		userDir = filepath.Join(home, ".config", "Code", "User")
	}

	newPath := filepath.Join(userDir, "mcp.json")
	return idePath{path: newPath, displayName: "VS Code"}, true
}
