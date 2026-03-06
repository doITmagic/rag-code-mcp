package react

import (
	"regexp"
	"strings"
)

var (
	// React hooks (built-in)
	builtinHooks = map[string]bool{
		"useState": true, "useEffect": true, "useContext": true,
		"useReducer": true, "useCallback": true, "useMemo": true,
		"useRef": true, "useImperativeHandle": true, "useLayoutEffect": true,
		"useDebugValue": true, "useDeferredValue": true, "useTransition": true,
		"useId": true, "useSyncExternalStore": true, "useInsertionEffect": true,
	}

	// React Navigation hooks
	rnHooks = map[string]bool{
		"useNavigation": true, "useRoute": true, "useFocusEffect": true,
		"useIsFocused": true, "useNavigationState": true, "useLinkTo": true,
		"useScrollToTop": true, "useTheme": true,
	}

	// React Native modules for detection
	rnModules = map[string]string{
		"AsyncStorage":       "storage",
		"Linking":            "linking",
		"Platform":           "platform",
		"PermissionsAndroid": "permissions",
		"AppState":           "lifecycle",
		"Keyboard":           "input",
		"Clipboard":          "clipboard",
		"Share":              "sharing",
		"Vibration":          "hardware",
		"BackHandler":        "navigation",
		"NativeModules":      "native",
		"Appearance":         "theme",
	}

	// Patterns
	reHookCall      = regexp.MustCompile(`\buse[A-Z]\w*\s*\(`)
	reCreateContext = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\w+)\s*=\s*(?:React\.)?createContext\s*\(`)
	reJSXElement    = regexp.MustCompile(`<[A-Z]\w*[\s/>]`)
	reReactImport   = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]react['"]`)
	reRNImport      = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]react-native['"]`)
	reRNNavImport   = regexp.MustCompile(`(?m)^import\s+.*\s+from\s+['"]@react-navigation/`)
	rePropsType     = regexp.MustCompile(`\(\s*\{([^}]+)\}\s*(?::\s*\w+)?\s*\)`)

	reFuncComponent  = regexp.MustCompile(`(?m)^(export\s+)?(default\s+)?(?:const|function)\s+([A-Z]\w*)\s*`)
	reClassComponent = regexp.MustCompile(`(?m)class\s+(\w+)\s+extends\s+(?:React\.)?(?:Component|PureComponent)`)

	// React Native specific
	reStyleSheet   = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\w+)\s*=\s*StyleSheet\.create\s*\(\s*\{`)
	reStyleKey     = regexp.MustCompile(`(?m)^\s+(\w+)\s*:\s*\{`)
	reScreenReg    = regexp.MustCompile(`<(\w+)\.Screen\s+name=['"]([^'"]+)['"](?:\s+component=\{(\w+)\})?`)
	reNavigator    = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\w+)\s*=\s*create(\w+)Navigator\s*\(`)
	reNativeModule = regexp.MustCompile(`(?m)\b(AsyncStorage|Linking|Platform|PermissionsAndroid|AppState|Keyboard|Clipboard|Share|Vibration|BackHandler|NativeModules|Appearance)\b`)
)

// Analyzer detects React-specific patterns
type Analyzer struct{}

// NewAnalyzer creates a new React analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// IsReactFile checks if the file imports React or contains JSX
func IsReactFile(source string) bool {
	return reReactImport.MatchString(source) || reJSXElement.MatchString(source)
}

// IsReactNativeFile checks if the file imports from react-native
func IsReactNativeFile(source string) bool {
	return reRNImport.MatchString(source) || reRNNavImport.MatchString(source)
}

// Analyze performs React and React Native analysis
// Uses tree-sitter AST as primary engine, with regex fallback
func (a *Analyzer) Analyze(source string, filePath string) *ReactInfo {
	// Try tree-sitter first (accurate AST-based detection)
	tsAnalyzer := NewTreeSitterAnalyzer()
	info := tsAnalyzer.Analyze([]byte(source), filePath)
	if info != nil && (len(info.Components) > 0 || len(info.Hooks) > 0 || len(info.Contexts) > 0) {
		return info
	}

	// Fallback to regex-based detection
	if !IsReactFile(source) && !IsReactNativeFile(source) {
		return &ReactInfo{}
	}

	info = &ReactInfo{}
	info.IsReactNative = IsReactNativeFile(source)

	// Detect components
	lines := strings.Split(source, "\n")
	info.Components = a.detectComponents(source, lines, filePath)

	// Mark RN-specific component properties
	if info.IsReactNative {
		for i := range info.Components {
			info.Components[i].IsNative = true
			// Detect platform from filename
			if strings.Contains(filePath, ".ios.") {
				info.Components[i].Platform = "ios"
			} else if strings.Contains(filePath, ".android.") {
				info.Components[i].Platform = "android"
			}
			// Check if it's a screen (name contains Screen or file in screens/)
			name := info.Components[i].Name
			if strings.HasSuffix(name, "Screen") || strings.Contains(filePath, "screens/") || strings.Contains(filePath, "screens\\") {
				info.Components[i].IsScreen = true
			}
		}
	}

	// Detect hooks
	info.Hooks = a.detectHooks(source, filePath)

	// Detect contexts
	info.Contexts = a.detectContexts(source, filePath)

	// React Native specific detections
	if info.IsReactNative {
		info.NativeStyles = a.detectStyleSheets(source, lines, filePath)
		info.Screens = a.detectScreenRegistrations(source, filePath)
		info.Navigation = a.detectNavigators(source, filePath)
		info.NativeModules = a.detectNativeModules(source, filePath)
	}

	return info
}

// detectComponents finds React functional and class components
func (a *Analyzer) detectComponents(source string, lines []string, filePath string) []Component {
	var components []Component

	// Functional components (functions starting with uppercase that return JSX)
	for _, match := range reFuncComponent.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[6]:match[7]]
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		isDefault := match[4] != -1 && source[match[4]:match[5]] != ""

		line := strings.Count(source[:match[0]], "\n") + 1

		// Check if it has JSX in the body (look ahead a reasonable amount)
		bodyEnd := match[1] + 2000
		if bodyEnd > len(source) {
			bodyEnd = len(source)
		}
		body := source[match[1]:bodyEnd]
		hasJSX := reJSXElement.MatchString(body)

		if !hasJSX {
			continue // Skip non-component functions
		}

		endLine := findClosingBrace(lines, line-1)

		// Extract props
		props := a.extractProps(source[match[0]:min(match[1]+200, len(source))])

		// Detect hooks used in component
		bodyStart := match[0]
		bodyEndIdx := bodyStart
		for i := line - 1; i < len(lines); i++ {
			bodyEndIdx += len(lines[i]) + 1
			if i+1 >= endLine {
				break
			}
		}
		componentBody := ""
		if bodyEndIdx <= len(source) {
			componentBody = source[bodyStart:bodyEndIdx]
		}
		hooks := a.extractHookNames(componentBody)

		components = append(components, Component{
			Name:       name,
			Type:       "functional",
			Props:      props,
			Hooks:      hooks,
			IsExported: exported,
			IsDefault:  isDefault,
			HasJSX:     hasJSX,
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		})
	}

	// Class components
	for _, match := range reClassComponent.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1
		endLine := findClosingBrace(lines, line-1)

		components = append(components, Component{
			Name:      name,
			Type:      "class",
			HasJSX:    true,
			FilePath:  filePath,
			StartLine: line,
			EndLine:   endLine,
		})
	}

	return components
}

// detectHooks finds all React/RN hook calls
func (a *Analyzer) detectHooks(source string, filePath string) []HookUsage {
	var hooks []HookUsage

	for _, match := range reHookCall.FindAllStringIndex(source, -1) {
		hookStr := source[match[0] : match[1]-1] // remove trailing (
		line := strings.Count(source[:match[0]], "\n") + 1

		isRN := rnHooks[hookStr]
		isCustom := !builtinHooks[hookStr] && !isRN

		hooks = append(hooks, HookUsage{
			Name:     hookStr,
			IsCustom: isCustom,
			IsRN:     isRN,
			FilePath: filePath,
			Line:     line,
		})
	}

	return hooks
}

// detectContexts finds React.createContext calls
func (a *Analyzer) detectContexts(source string, filePath string) []Context {
	var contexts []Context

	for _, match := range reCreateContext.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1

		contexts = append(contexts, Context{
			Name:     name,
			FilePath: filePath,
			Line:     line,
		})
	}

	return contexts
}

// extractProps extracts prop names from component parameter
func (a *Analyzer) extractProps(header string) []string {
	// Destructured: ({ name, age, onClick })
	if m := rePropsType.FindStringSubmatch(header); m != nil {
		var props []string
		for _, p := range strings.Split(m[1], ",") {
			p = strings.TrimSpace(p)
			// Strip type annotation
			if idx := strings.Index(p, ":"); idx != -1 {
				p = strings.TrimSpace(p[:idx])
			}
			// Strip defaults
			if idx := strings.Index(p, "="); idx != -1 {
				p = strings.TrimSpace(p[:idx])
			}
			p = strings.TrimPrefix(p, "...")
			if p != "" {
				props = append(props, p)
			}
		}
		return props
	}
	return nil
}

// extractHookNames extracts hook names from a component body
func (a *Analyzer) extractHookNames(body string) []string {
	seen := make(map[string]bool)
	var hooks []string

	for _, match := range reHookCall.FindAllString(body, -1) {
		hookName := strings.TrimSuffix(match, "(")
		hookName = strings.TrimSpace(hookName)
		if !seen[hookName] {
			seen[hookName] = true
			hooks = append(hooks, hookName)
		}
	}

	return hooks
}

// findClosingBrace finds closing brace line (1-indexed)
func findClosingBrace(lines []string, startLine int) int {
	depth := 0
	for i := startLine; i < len(lines); i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
	}
	return startLine + 1
}

// detectStyleSheets finds StyleSheet.create() calls and their keys
func (a *Analyzer) detectStyleSheets(source string, lines []string, filePath string) []NativeStyle {
	var styles []NativeStyle

	for _, match := range reStyleSheet.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		line := strings.Count(source[:match[0]], "\n") + 1

		// Extract style keys from the object
		endLine := findClosingBrace(lines, line-1)
		var keys []string
		for i := line; i < endLine && i < len(lines); i++ {
			if m := reStyleKey.FindStringSubmatch(lines[i]); m != nil {
				keys = append(keys, m[1])
			}
		}

		styles = append(styles, NativeStyle{
			Name:     name,
			Keys:     keys,
			FilePath: filePath,
			Line:     line,
		})
	}

	return styles
}

// detectScreenRegistrations finds <Stack.Screen name="..." /> patterns
func (a *Analyzer) detectScreenRegistrations(source string, filePath string) []Screen {
	var screens []Screen

	for _, match := range reScreenReg.FindAllStringSubmatchIndex(source, -1) {
		navigator := source[match[2]:match[3]]
		screenName := source[match[4]:match[5]]
		component := ""
		if match[6] != -1 {
			component = source[match[6]:match[7]]
		}
		line := strings.Count(source[:match[0]], "\n") + 1

		screens = append(screens, Screen{
			Name:       screenName,
			ScreenName: screenName,
			Component:  component,
			Navigator:  navigator,
			FilePath:   filePath,
			Line:       line,
		})
	}

	return screens
}

// detectNavigators finds createXxxNavigator() calls
func (a *Analyzer) detectNavigators(source string, filePath string) []NavigationRef {
	var navs []NavigationRef

	for _, match := range reNavigator.FindAllStringSubmatchIndex(source, -1) {
		name := source[match[2]:match[3]]
		navType := source[match[4]:match[5]]
		line := strings.Count(source[:match[0]], "\n") + 1

		navs = append(navs, NavigationRef{
			Type:     navType,
			Name:     name,
			FilePath: filePath,
			Line:     line,
		})
	}

	return navs
}

// detectNativeModules finds RN native module usage
func (a *Analyzer) detectNativeModules(source string, filePath string) []NativeModule {
	var modules []NativeModule
	seen := make(map[string]bool)

	for _, match := range reNativeModule.FindAllStringSubmatchIndex(source, -1) {
		modName := source[match[2]:match[3]]
		if seen[modName] {
			continue
		}
		seen[modName] = true

		category := rnModules[modName]
		if category == "" {
			category = "other"
		}
		line := strings.Count(source[:match[0]], "\n") + 1

		modules = append(modules, NativeModule{
			Module:   modName,
			Category: category,
			FilePath: filePath,
			Line:     line,
		})
	}

	return modules
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
